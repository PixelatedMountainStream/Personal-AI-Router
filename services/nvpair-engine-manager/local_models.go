// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"nvpair-shared/noderec"
)

// LocalModelsExt is the fork's extension of ModelsResult (seam E11). It is
// embedded, so encoding/json flattens it and the wire keys are unchanged; a
// zero value adds no keys.
//
// SlotsByEngine carries typed per-slot capacity for each engine's loaded models
// (engine -> model -> SlotInfo). It is populated only for engines declaring an
// optional slots action that was queried successfully; a missing engine or model
// means "capacity unknown", never "one slot".
type LocalModelsExt struct {
	SlotsByEngine map[string]map[string]SlotInfo `json:"slotsByEngine,omitempty"`
	// JITLoadByEngine is whether each reporting engine loads a missing model on
	// demand: the node-settings jit_load.<engine> override, else the engine
	// default (noderec.DefaultJITLoad). Present only for engines that reported.
	JITLoadByEngine map[string]bool `json:"jitLoadByEngine,omitempty"`
}

// SlotInfo is a loaded model's parallel capacity: concurrent request slots and
// the context window each slot gets (0 when the engine does not report it).
type SlotInfo = noderec.SlotInfo

// localSlotSweep is the slots part of one ModelsResult sweep, indexed by engine
// position like upstream's per-engine slices. Every method is nil-safe; a nil
// sweep leaves ModelsResult on upstream's code path.
type localSlotSweep struct {
	acts  []*Action
	slots []map[string]SlotInfo
}

func newLocalSlotSweep(n int) *localSlotSweep {
	return &localSlotSweep{acts: make([]*Action, n), slots: make([]map[string]SlotInfo, n)}
}

// prepare records engine i's optional slots action (one with a record spec).
func (s *localSlotSweep) prepare(i int, mf *Manifest) {
	if s == nil {
		return
	}
	if act, ok := mf.Actions["slots"]; ok && act.Result != nil && act.Result.Record != nil {
		s.acts[i] = &act
	}
}

// collect queries engine i's slots once its loaded_models query succeeded.
// Called from engine i's sweep goroutine; each goroutine writes only index i.
func (s *localSlotSweep) collect(ctx context.Context, e *Executor, i int, name string, loadedOK bool, loaded []string) {
	if s == nil || s.acts[i] == nil || !loadedOK {
		return
	}
	s.slots[i] = e.querySlots(ctx, name, s.acts[i], loaded)
}

// finish merges the collected slots into res and applies the node-settings
// overrides (which also resolve jit_load for every reporting engine).
func (s *localSlotSweep) finish(e *Executor, res *ModelsResult, engineNames []string) {
	if s == nil {
		return
	}
	for i, sl := range s.slots {
		if sl == nil {
			continue
		}
		if res.SlotsByEngine == nil {
			res.SlotsByEngine = make(map[string]map[string]SlotInfo)
		}
		res.SlotsByEngine[engineNames[i]] = sl
	}
	res.applySettingsOverrides(e.settingsOverrides)
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
	names := slices.Collect(maps.Keys(reported))
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

// expandInstances splits each row into one row per entry of spec.Each, with
// spec.Field set to the instance's id and loaded_instances narrowed to it. The
// row's size_bytes describes only the selected variant; a sibling instance
// (id != key) keeps it only as a conservative upper bound, so a sibling of a
// large key is never mistaken for a small model. With no key size, the sibling's
// size stays unknown. An instance with a missing or invalid id is an error when
// strict, and skipped otherwise.
func expandInstances(arr []map[string]json.RawMessage, spec *ActionResult, strict bool) ([]map[string]json.RawMessage, error) {
	if spec.Each == "" {
		return arr, nil
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
		for _, in := range insts {
			var m map[string]json.RawMessage
			var id string
			if json.Unmarshal(in, &m) != nil || json.Unmarshal(m[idf], &id) != nil || id == "" {
				if strict {
					return nil, errors.New("loaded instance has a missing or invalid id")
				}
				continue
			}
			c := maps.Clone(el)
			c[spec.Field], _ = json.Marshal(id)
			c[spec.Each], _ = json.Marshal([]json.RawMessage{in})
			out = append(out, c)
		}
	}
	return out, nil
}

// extractRecords builds a SlotInfo per row of the required top-level spec.Array,
// keyed by the row's spec.Field string, honoring spec.Match. Rows whose capacity
// cannot be extracted (missing or wrong-typed path) are skipped. A missing or
// non-array spec.Array returns ok=false.
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
	rows, _ := expandInstances(arr, spec, false)
	for _, el := range rows {
		if spec.Match != nil {
			if match, _ := classifyRow(el, spec.Match); !match {
				continue
			}
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
	for part := range strings.SplitSeq(path, ".") {
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

// classifyRow is upstream's matchRow with a second result: ok=false when the
// row cannot be classified (path missing, wrong-typed, or JSON null), which
// residency checks treat as a failure instead of "not loaded".
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
	return slices.Contains(m.In, s), true
}
