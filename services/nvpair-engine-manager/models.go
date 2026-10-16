// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"nvpair-shared/noderec"
)

// modelsTimeout bounds the whole engine:models sweep so one hung engine can't
// stall the model list. list_models is a cheap loopback GET, so this is short
// (unlike the 30-minute engine:action ceiling that also has to cover pulls).
const modelsTimeout = 5 * time.Second

// ModelsResult is the model payload the LAN HTTP surface (/v1/models) and the
// engine:models RPC return: the flat, de-duplicated union of every running
// engine's models plus the per-engine attribution keyed by engine name (e.g.
// "ollama", "lmstudio"). Models is retained unchanged for consumers that only
// need the node-level set; ByEngine is the additive attribution a per-engine
// consumer needs (a node's engine card, and the proxies' own-engine model-owner
// ranking). ByEngine includes every running engine whose inventory was
// successfully queried, so an empty list means "running, no models available"
// while a missing key means "not running / not queryable".
//
// LoadedByEngine names the models currently resident in memory, per engine —
// normally a subset of that engine's ByEngine list. It is populated for every
// running engine that declares a loaded_models action AND was successfully
// queried, so an engine key with an empty list means "running, nothing loaded"
// while a MISSING key means "not running / not queryable / no loaded endpoint" —
// a distinction a consumer needs to reflect real state. The subset
// relationship is best-effort: list_models and loaded_models are queried
// independently, so in the rare sweep where an engine's list_models fails but
// loaded_models succeeds, a loaded name can appear here without a matching
// ByEngine/Models entry until the next successful sweep. Its own omitempty drops
// the whole map when no engine reports loaded state.
//
// SlotsByEngine carries typed per-slot capacity for each engine's loaded models
// (engine -> model -> SlotInfo). It is populated only for engines declaring an
// optional slots action that was queried successfully; a missing engine or model
// means "capacity unknown", never "one slot".
type ModelsResult struct {
	Models         []string                       `json:"models"`
	ByEngine       map[string][]string            `json:"modelsByEngine,omitempty"`
	LoadedByEngine map[string][]string            `json:"loadedByEngine,omitempty"`
	SlotsByEngine  map[string]map[string]SlotInfo `json:"slotsByEngine,omitempty"`
	// JITLoadByEngine is whether each reporting engine loads a missing model on
	// demand: the node-settings jit_load.<engine> override, else the engine
	// default (noderec.DefaultJITLoad). Present only for engines that reported.
	JITLoadByEngine map[string]bool `json:"jitLoadByEngine,omitempty"`
}

// SlotInfo is a loaded model's parallel capacity: concurrent request slots and
// the context window each slot gets (0 when the engine does not report it).
type SlotInfo = noderec.SlotInfo

// Models returns the union of model names served by every installed, running
// engine that declares a list_models action with a result-extraction spec —
// the flat node-level list. It is a thin wrapper over {@link ModelsResult} kept
// for callers that only need the union (and the existing test surface).
func (e *Executor) Models(ctx context.Context) []string {
	return e.ModelsResult(ctx).Models
}

// ModelsResult queries each installed, running engine's loopback list_models
// endpoint (reusing the manifest action machinery), normalizes the
// engine-specific shapes, and returns both the flat de-duplicated union and the
// per-engine attribution — from a single sweep, so a caller needing both
// (the /v1/models HTTP surface, the engine:models RPC) never sweeps twice.
// Stopped engines contribute nothing — their model list isn't queryable while
// the server is down. Best-effort: a per-engine failure is logged and skipped
// so the engines that did answer still surface their models.
//
// The per-engine queries run CONCURRENTLY. A host running several engines (e.g.
// Ollama + LM Studio) must not have the later engines squeezed out of a single
// shared deadline: sequential queries under one budget meant a slow first engine
// left little for the second, and the peer's discovery daemon fetches this over
// HTTP with its own (shorter) timeout, so a squeezed second engine silently
// dropped its models for peers. Concurrency bounds the sweep by the slowest
// single engine, not the sum.
func (e *Executor) ModelsResult(ctx context.Context) ModelsResult {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()

	engineNames := e.reg.Names()
	// Results indexed by engine position so the flattened output stays
	// deterministic (registration order) despite concurrent completion.
	perEngine := make([][]string, len(engineNames))
	listOK := make([]bool, len(engineNames))
	loaded := make([][]string, len(engineNames))
	loadedOK := make([]bool, len(engineNames))
	slots := make([]map[string]SlotInfo, len(engineNames))
	var wg sync.WaitGroup
	for i, name := range engineNames {
		mf, ok := e.reg.Get(name)
		if !ok {
			continue
		}
		var listSpec, loadedSpec *ActionResult
		var slotsAct *Action
		if act, ok := mf.Actions["list_models"]; ok {
			listSpec = act.Result
		}
		if act, ok := mf.Actions["loaded_models"]; ok {
			loadedSpec = act.Result
		}
		if act, ok := mf.Actions["slots"]; ok && act.Result != nil && act.Result.Record != nil {
			slotsAct = &act
		}
		if listSpec == nil && loadedSpec == nil {
			continue
		}
		wg.Add(1)
		go func(i int, name string, listSpec, loadedSpec *ActionResult, slotsAct *Action) {
			defer wg.Done()
			st, err := e.Status(name)
			if err != nil || !st.Running {
				return
			}
			if listSpec != nil {
				if raw, err := e.Action(ctx, name, "list_models", nil); err != nil {
					slog.Debug("engine:models list_models failed", "engine", name, "err", err)
				} else if models, ok := extractStringsResult(raw, listSpec); ok {
					perEngine[i] = models
					listOK[i] = true
				} else {
					slog.Debug("engine:models list_models returned an invalid inventory", "engine", name)
				}
			}
			if loadedSpec != nil {
				if raw, err := e.Action(ctx, name, "loaded_models", nil); err != nil {
					slog.Debug("engine:models loaded_models failed", "engine", name, "err", err)
				} else if models, ok := extractStringsResult(raw, loadedSpec); ok {
					// A successful query — even an empty result — records the key
					// so a consumer can tell "running, nothing loaded" apart from
					// "unknown". An action or shape error leaves loadedOK[i] false.
					loaded[i] = models
					loadedOK[i] = true
				} else {
					slog.Debug("engine:models loaded_models returned an invalid inventory", "engine", name)
				}
			}
			if slotsAct != nil && loadedOK[i] {
				slots[i] = e.querySlots(ctx, name, slotsAct, loaded[i])
			}
		}(i, name, listSpec, loadedSpec, slotsAct)
	}
	wg.Wait()

	res := ModelsResult{Models: []string{}}
	seen := map[string]bool{}
	for i, models := range perEngine {
		if listOK[i] {
			if res.ByEngine == nil {
				res.ByEngine = make(map[string][]string)
			}
			// Normalize nil -> [] so the wire distinguishes a successful empty
			// inventory from a missing/not-queryable engine.
			if models == nil {
				models = []string{}
			}
			res.ByEngine[engineNames[i]] = models
			for _, m := range models {
				if m != "" && !seen[m] {
					seen[m] = true
					res.Models = append(res.Models, m)
				}
			}
		}
		if loadedOK[i] {
			if res.LoadedByEngine == nil {
				res.LoadedByEngine = make(map[string][]string)
			}
			// Normalize nil -> [] so the wire carries an empty array ("queried,
			// nothing resident"), never null.
			ld := loaded[i]
			if ld == nil {
				ld = []string{}
			}
			res.LoadedByEngine[engineNames[i]] = ld
		}
		if slots[i] != nil {
			if res.SlotsByEngine == nil {
				res.SlotsByEngine = make(map[string]map[string]SlotInfo)
			}
			res.SlotsByEngine[engineNames[i]] = slots[i]
		}
	}
	res.applySettingsOverrides(e.settingsOverrides)
	return res
}

// applySettingsOverrides merges node-settings engine overrides (slots, ctx per
// slot for loaded models) into the result and resolves jit_load for every
// engine that reported. A nil source still resolves the jit_load defaults.
func (r *ModelsResult) applySettingsOverrides(src func() map[string]json.RawMessage) {
	var raw map[string]json.RawMessage
	if src != nil {
		raw = src()
	}
	reported := map[string]bool{}
	for n := range r.ByEngine {
		reported[n] = true
	}
	for n := range r.LoadedByEngine {
		reported[n] = true
	}
	names := make([]string, 0, len(reported))
	for n := range reported {
		names = append(names, n)
	}
	r.SlotsByEngine, r.JITLoadByEngine = noderec.ApplyCapacityOverrides(raw, names, r.LoadedByEngine, r.SlotsByEngine)
}

// querySlots runs an engine's optional slots action and returns capacity keyed
// by model, or nil when the engine could not be queried. An array-shaped spec is
// one call; a query-shaped spec (empty Array) is one call per loaded model with
// {"model": m}. A model whose capacity cannot be extracted is omitted.
func (e *Executor) querySlots(ctx context.Context, engine string, act *Action, loaded []string) map[string]SlotInfo {
	spec := act.Result
	if spec.Array != "" {
		raw, err := e.Action(ctx, engine, "slots", nil)
		if err != nil {
			slog.Debug("engine:models slots failed", "engine", engine, "err", err)
			return nil
		}
		out, ok := extractRecords(raw, spec)
		if !ok {
			slog.Debug("engine:models slots returned an invalid response", "engine", engine)
			return nil
		}
		return out
	}
	out := map[string]SlotInfo{}
	for _, m := range loaded {
		params, _ := json.Marshal(map[string]string{"model": m})
		raw, err := e.Action(ctx, engine, "slots", params)
		if err != nil {
			slog.Debug("engine:models slots failed", "engine", engine, "model", m, "err", err)
			continue
		}
		if si, ok := extractRecord(raw, spec.Record); ok {
			out[m] = si
		}
	}
	return out
}

// extractRecords builds a SlotInfo per row of the required top-level spec.Array,
// keyed by the row's spec.Field string, honoring spec.Match. Rows whose capacity
// cannot be extracted (missing or wrong-typed path) are skipped. A missing or
// non-array spec.Array returns ok=false.
// expandInstances splits each row into one row per entry of spec.Each, with
// spec.Field set to the instance's id and loaded_instances narrowed to it. The
// row's size_bytes describes only the selected variant, so it is kept only for
// the instance whose id equals the key and dropped (unknown) otherwise.
func expandInstances(arr []map[string]json.RawMessage, spec *ActionResult) []map[string]json.RawMessage {
	if spec.Each == "" {
		return arr
	}
	idf := spec.EachField
	if idf == "" {
		idf = "id"
	}
	var out []map[string]json.RawMessage
	for _, el := range arr {
		var insts []json.RawMessage
		if json.Unmarshal(el[spec.Each], &insts) != nil || len(insts) == 0 {
			out = append(out, el)
			continue
		}
		var key string
		_ = json.Unmarshal(el[spec.Field], &key)
		for _, in := range insts {
			var m map[string]json.RawMessage
			var id string
			if json.Unmarshal(in, &m) != nil || json.Unmarshal(m[idf], &id) != nil || id == "" {
				out = append(out, el) // unclassifiable instance: keep the row
				continue
			}
			c := map[string]json.RawMessage{}
			for k, v := range el {
				c[k] = v
			}
			c[spec.Field], _ = json.Marshal(id)
			c[spec.Each], _ = json.Marshal([]json.RawMessage{in})
			if id != key {
				delete(c, "size_bytes")
			}
			out = append(out, c)
		}
	}
	return out
}

func extractRecords(raw json.RawMessage, spec *ActionResult) (map[string]SlotInfo, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(obj[spec.Array], &arr); err != nil || arr == nil {
		return nil, false
	}
	out := map[string]SlotInfo{}
	for _, el := range expandInstances(arr, spec) {
		if spec.Match != nil && !matchRow(el, spec.Match) {
			continue
		}
		var key string
		if err := json.Unmarshal(el[spec.Field], &key); err != nil || key == "" {
			continue
		}
		row, _ := json.Marshal(el)
		if si, ok := extractRecord(row, spec.Record); ok {
			out[key] = si
		}
	}
	return out, true
}

// extractRecord builds one SlotInfo from raw. Slots (>= 1) is required; ctx is
// optional, but a present ctx of the wrong type or negative invalidates the record.
func extractRecord(raw json.RawMessage, rec *ResultRecord) (SlotInfo, bool) {
	var si SlotInfo
	if rec.declares("slots") || !rec.declares("bytes") {
		slots, ok := recordInt(raw, rec, "slots")
		if !ok || slots < 1 {
			return SlotInfo{}, false
		}
		si.Slots = slots
	}
	if rec.declares("bytes") {
		b, ok := recordInt(raw, rec, "bytes")
		if !ok || b < 0 {
			return SlotInfo{}, false
		}
		si.Bytes = int64(b)
	}
	if ctx, ok := recordInt(raw, rec, "ctx"); ok {
		if ctx < 0 {
			return SlotInfo{}, false
		}
		si.CtxPerSlot = ctx
	} else if _, declared := rec.Fields["ctx"]; declared {
		if _, present := resolveRecordPath(raw, rec.Fields["ctx"]); present {
			return SlotInfo{}, false // present but not an integer
		}
	}
	return si, true
}

func recordInt(raw json.RawMessage, rec *ResultRecord, key string) (int, bool) {
	if path, ok := rec.Fields[key]; ok {
		v, found := resolveRecordPath(raw, path)
		var n int
		if !found || json.Unmarshal(v, &n) != nil {
			return 0, false
		}
		return n, true
	}
	n, ok := rec.Static[key]
	return n, ok
}

// resolveRecordPath walks a dot path through objects and, for numeric parts,
// arrays.
func resolveRecordPath(raw json.RawMessage, path string) (json.RawMessage, bool) {
	cur := raw
	for _, part := range strings.Split(path, ".") {
		var obj map[string]json.RawMessage
		if json.Unmarshal(cur, &obj) == nil {
			v, ok := obj[part]
			if !ok {
				return nil, false
			}
			cur = v
			continue
		}
		var arr []json.RawMessage
		idx, err := strconv.Atoi(part)
		if err != nil || idx < 0 || json.Unmarshal(cur, &arr) != nil || idx >= len(arr) {
			return nil, false
		}
		cur = arr[idx]
	}
	return cur, true
}

// extractStrings is the value-only wrapper used by focused extractor tests.
// Callers that must distinguish a valid empty inventory from an invalid
// response use extractStringsResult.
func extractStrings(raw json.RawMessage, spec *ActionResult) []string {
	out, ok := extractStringsResult(raw, spec)
	if !ok || len(out) == 0 {
		return nil
	}
	return out
}

// extractStringsResult pulls spec.Field from each element of the required
// top-level spec.Array. A present empty array is a successful, authoritative
// empty inventory. A missing, null, or wrong-typed array is unknown and returns
// ok=false so callers do not label it authoritative empty. Malformed rows
// inside a valid array are skipped, preserving additive response compatibility. For an
// unfiltered inventory, however, a non-empty array with no usable names is
// malformed rather than authoritative empty.
func extractStringsResult(raw json.RawMessage, spec *ActionResult) ([]string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	arrRaw, ok := obj[spec.Array]
	if !ok || bytes.Equal(bytes.TrimSpace(arrRaw), []byte("null")) {
		return nil, false
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(arrRaw, &arr); err != nil {
		return nil, false
	}
	out := make([]string, 0)
	for _, el := range arr {
		if spec.Match != nil && !matchRow(el, spec.Match) {
			continue
		}
		fv, ok := el[spec.Field]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(fv, &s); err == nil && s != "" {
			out = append(out, s)
		}
	}
	if spec.Match == nil && len(arr) > 0 && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// matchRow reports whether an element passes an ActionResult row filter.
// Match.Field is a validated dot-separated object path. With Match.In set, the
// resolved value must decode as a JSON string equal to one of In. With
// Match.Nonempty set, it must decode as a JSON array with length > 0 (LM Studio
// /api/v1/models loaded_instances). A missing or wrong-typed path fails the
// match, so a row we cannot classify is excluded rather than counted as loaded.
func matchRow(el map[string]json.RawMessage, m *ResultMatch) bool {
	match, _ := classifyRow(el, m)
	return match
}

// classifyRow is matchRow with a second result: ok=false when the row cannot be
// classified (path missing or wrong-typed), which residency checks treat as a
// failure instead of "not loaded".
func classifyRow(el map[string]json.RawMessage, m *ResultMatch) (match, ok bool) {
	fv, found := resolveObjectPath(el, m.Field)
	if !found {
		return false, false
	}
	if bytes.Equal(bytes.TrimSpace(fv), []byte("null")) {
		return false, false // JSON null would decode as an empty value, i.e. "not loaded"
	}
	if m.Nonempty {
		var arr []json.RawMessage
		if err := json.Unmarshal(fv, &arr); err != nil {
			return false, false
		}
		return len(arr) > 0, true
	}
	var s string
	if err := json.Unmarshal(fv, &s); err != nil {
		return false, false
	}
	for _, want := range m.In {
		if s == want {
			return true, true
		}
	}
	return false, true
}

func resolveObjectPath(obj map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	parts := strings.Split(path, ".")
	value, ok := obj[parts[0]]
	for _, part := range parts[1:] {
		if !ok {
			return nil, false
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(value, &nested); err != nil {
			return nil, false
		}
		value, ok = nested[part]
	}
	return value, ok
}
