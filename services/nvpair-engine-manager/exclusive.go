// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// exclusive.go enforces the pinned-load safety invariant: a pinned load never
// leaves two large models resident on one node. A large model resident on any
// engine (other than the target) is a "victim"; without evict the load is
// refused with 409, with evict the victims are unloaded and confirmed gone
// before anything is loaded. Loads that bypass engine-manager (LM Studio JIT,
// llama.cpp router, a direct Ollama request) cannot be prevented here; the
// watcher only warns about them.
//
// LIMITATIONS: (1) a pinned load here and a proxy JIT load are not coordinated
// across processes (the proxy claim is in-process). (2) Clearing an uncertain
// load is a bounded heuristic, not a guarantee.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLargeModelGiB = 12
	// largeParamBillions is the name-token size at or above which a model with
	// no known byte size counts as large (e.g. "27b").
	largeParamBillions = 20
)

// residentModel is one loaded model with what is known about its size and
// load-time parameters.
type residentModel struct {
	Engine   string
	Model    string
	Bytes    int64 // 0 = unknown
	Slots    SlotInfo
	HasSlots bool
	// Loading: the engine reports the model still loading. It already occupies
	// memory, so it counts as resident, but it is not yet usable.
	Loading bool
}

func (r residentModel) victim() Victim {
	return Victim{Engine: r.Engine, Model: r.Model, Bytes: r.Bytes}
}

var paramTokenRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.])(\d+(?:\.\d+)?)b(?:$|[^a-z0-9])`)

// isLargeModel: large when the resident bytes reach the threshold OR the name
// carries a "<n>b" parameter token of at least largeParamBillions (a heavily
// quantized 27b is still large). Otherwise small when bytes are known or a
// smaller token is present; with neither, large (fail safe).
func isLargeModel(bytes int64, name string, thresholdBytes int64) bool {
	if bytes > 0 && bytes >= thresholdBytes {
		return true
	}
	found := false
	for _, m := range paramTokenRe.FindAllStringSubmatch(name, -1) {
		if n, err := strconv.ParseFloat(m[1], 64); err == nil {
			found = true
			if n >= largeParamBillions {
				return true
			}
		}
	}
	return !found && bytes <= 0
}

func (e *Executor) isLarge(r residentModel) bool {
	return isLargeModel(r.Bytes, r.Model, e.largeModelBytes)
}

// sameParams reports whether a resident model already has the requested load
// parameters. Unknown current parameters never match a requested one.
func sameParams(r residentModel, o LoadOpts) bool {
	if o.ContextLength > 0 && (!r.HasSlots || r.Slots.CtxPerSlot != o.ContextLength) {
		return false
	}
	if o.Parallel > 0 && (!r.HasSlots || r.Slots.Slots != o.Parallel) {
		return false
	}
	return true
}

// pinnedOps are the executor operations PinnedLoad drives; tests replace them.
type pinnedOps struct {
	resident func(ctx context.Context) ([]residentModel, error)
	unload   func(ctx context.Context, engine, model string) error
	load     func(ctx context.Context, engine, model string, o LoadOpts) error
	applied  func(ctx context.Context, engine, model string) (SlotInfo, bool)
	// running reports whether the engine process is up; nil means "assume yes".
	running func(engine string) bool
}

func (e *Executor) pinnedOps() *pinnedOps {
	if e.ops != nil {
		return e.ops
	}
	return &pinnedOps{
		resident: func(ctx context.Context) ([]residentModel, error) {
			ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
			defer cancel()
			return e.residentStrict(ctx)
		},
		unload: func(ctx context.Context, engine, model string) error {
			_, err := e.ModelUnload(ctx, engine, model)
			return err
		},
		load: func(ctx context.Context, engine, model string, o LoadOpts) error {
			_, err := e.loadModelUnguarded(ctx, engine, model, o)
			return err
		},
		applied: func(ctx context.Context, engine, model string) (SlotInfo, bool) {
			si, ok := e.ModelsResult(ctx).SlotsByEngine[engine][model]
			return si, ok
		},
		running: func(engine string) bool {
			st, err := e.state(engine)
			if err != nil {
				return false
			}
			st.mu.Lock()
			defer st.mu.Unlock()
			return st.running
		},
	}
}

// residentStrict is the exclusivity snapshot: it queries every running engine
// that declares loaded_models and fails closed when any engine cannot be
// queried or any row cannot be classified (a partial view could hide a large
// resident model). Sizes and slots come from the helpers the watcher shares.
func (e *Executor) residentStrict(ctx context.Context) ([]residentModel, error) {
	res := ModelsResult{LoadedByEngine: map[string][]string{}, SlotsByEngine: map[string]map[string]SlotInfo{}}
	loadingSet := map[string]bool{}
	for _, name := range e.reg.Names() {
		mf, ok := e.reg.Get(name)
		if !ok {
			continue
		}
		act, declares := mf.Actions["loaded_models"]
		if !declares {
			continue
		}
		// statusNoWait: startup gating calls this while holding the starting
		// engine's lifecycle lock, and the confirm deadline must bound it.
		if st, err := e.statusNoWait(ctx, name); err != nil || !st.Running {
			if e.residencyUnqueryable(name, act) {
				return nil, fmt.Errorf("cannot query residency of engine %q while its server is stopped (it may still hold loaded models)", name)
			}
			continue
		}
		raw, err := e.Action(ctx, name, "loaded_models", nil)
		if err != nil {
			return nil, fmt.Errorf("cannot determine which models engine %q has loaded: %w", name, err)
		}
		loaded, loading, err := extractResidency(raw, act.Result)
		if err != nil {
			return nil, fmt.Errorf("engine %q loaded_models is unusable: %w", name, err)
		}
		res.LoadedByEngine[name] = append(append([]string{}, loaded...), loading...)
		for _, m := range loading {
			loadingSet[name+"/"+m] = true
		}
		if sa, ok := mf.Actions["slots"]; ok && sa.Result != nil && sa.Result.Record != nil && len(loaded) > 0 {
			res.SlotsByEngine[name] = e.querySlots(ctx, name, &sa, loaded)
		}
	}
	out := e.residentFrom(ctx, res)
	for i := range out {
		out[i].Loading = loadingSet[out[i].Engine+"/"+out[i].Model]
	}
	return out, nil
}

// residencyUnqueryable: a stopped engine normally holds nothing, but an
// installed command-mode engine (LM Studio) keeps a daemon that can hold loaded
// models with its HTTP server down, and its loaded_models needs that server.
func (e *Executor) residencyUnqueryable(engine string, act Action) bool {
	if len(act.Cmd) > 0 {
		return false
	}
	st, err := e.state(engine)
	if err != nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.installed && st.plat.Runtime.modeOrDefault() == "command"
}

// extractLoadedStrict is extractStringsResult for residency: every row must be
// classifiable (filter field present and well-typed, name a non-empty string);
// anything else is an error rather than a silently dropped row.
func extractLoadedStrict(raw json.RawMessage, spec *ActionResult) ([]string, error) {
	loaded, _, err := extractResidency(raw, spec)
	return loaded, err
}

// extractResidency also returns the models that match Match.LoadingIn (still
// loading; they occupy memory but are not loaded).
func extractResidency(raw json.RawMessage, spec *ActionResult) (loaded, loading []string, err error) {
	if spec == nil {
		return nil, nil, errors.New("no result spec")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, nil, errors.New("response is not an object")
	}
	var arr []map[string]json.RawMessage
	if arrRaw, ok := obj[spec.Array]; !ok || json.Unmarshal(arrRaw, &arr) != nil || arr == nil {
		return nil, nil, fmt.Errorf("missing or invalid %q array", spec.Array)
	}
	loaded = []string{}
	for i, el := range arr {
		if spec.Match != nil {
			match, ok := classifyRow(el, spec.Match)
			if !ok {
				return nil, nil, fmt.Errorf("row %d cannot be classified", i)
			}
			if !match {
				isLoading, ok := classifyLoading(el, spec.Match)
				if !ok {
					return nil, nil, fmt.Errorf("row %d cannot be classified", i)
				}
				if !isLoading {
					continue
				}
				var n string
				if json.Unmarshal(el[spec.Field], &n) != nil || n == "" {
					return nil, nil, fmt.Errorf("row %d has no model name", i)
				}
				loading = append(loading, n)
				continue
			}
		}
		var name string
		if json.Unmarshal(el[spec.Field], &name) != nil || name == "" {
			return nil, nil, fmt.Errorf("row %d has no model name", i)
		}
		loaded = append(loaded, name)
	}
	return loaded, loading, nil
}

// classifyLoading reports whether the row's match field holds one of LoadingIn.
func classifyLoading(el map[string]json.RawMessage, m *ResultMatch) (loading, ok bool) {
	if len(m.LoadingIn) == 0 {
		return false, true
	}
	fv, found := resolveObjectPath(el, m.Field)
	var s string
	if !found || json.Unmarshal(fv, &s) != nil {
		return false, false
	}
	for _, v := range m.LoadingIn {
		if s == v {
			return true, true
		}
	}
	return false, true
}

// residentFrom turns a models sweep into the resident list, adding byte sizes
// from each engine's model_sizes action. Engines absent from the sweep
// contribute nothing; the exclusivity caller gets completeness from
// residentStrict, the watcher tolerates gaps.
func (e *Executor) residentFrom(ctx context.Context, res ModelsResult) []residentModel {
	var out []residentModel
	for _, name := range e.reg.Names() {
		mf, ok := e.reg.Get(name)
		if !ok {
			continue
		}
		loaded := res.LoadedByEngine[name]
		if len(loaded) == 0 {
			continue
		}
		var sizes map[string]SlotInfo
		if act, ok := mf.Actions["model_sizes"]; ok && act.Result != nil {
			if raw, err := e.Action(ctx, name, "model_sizes", nil); err == nil {
				sizes, _ = extractRecords(raw, act.Result)
			} else {
				slog.Debug("model_sizes failed", "engine", name, "err", err)
			}
		}
		for _, m := range loaded {
			r := residentModel{Engine: name, Model: m, Bytes: sizes[m].Bytes}
			r.Slots, r.HasSlots = res.SlotsByEngine[name][m]
			out = append(out, r)
		}
	}
	return out
}

// victimsOf returns what must be unloaded before req can load, and whether the
// target is already resident with the requested parameters.
func (e *Executor) victimsOf(snap []residentModel, req PinnedRequest) (victims []Victim, targetSame bool) {
	targetLarge := isLargeModel(0, req.Model, e.largeModelBytes)
	for _, r := range snap {
		if r.Engine == req.Engine && r.Model == req.Model {
			targetLarge = e.isLarge(r)
			break
		}
	}
	for _, r := range snap {
		switch {
		case r.Engine == req.Engine && r.Model == req.Model:
			// A different-params instance must go, or LM Studio adds "model:2".
			if sameParams(r, req.LoadOpts) {
				targetSame = true
			} else {
				victims = append(victims, r.victim())
			}
		case targetLarge && e.isLarge(r):
			victims = append(victims, r.victim())
		}
	}
	return victims, targetSame
}

// exclusiveLoad is the guarded body of PinnedLoad. Callers have validated req.
func (e *Executor) exclusiveLoad(ctx context.Context, req PinnedRequest) (PinnedResult, error) {
	return e.exclusiveRun(ctx, req, nil)
}

// residentChecked takes the exclusivity snapshot (caller holds loadMu) and
// enforces the residency-uncertain reservation: after a load that errored or
// was cancelled post-acceptance, nothing else loads until a snapshot shows the
// target loaded, or absent (not loading) twice at least uncertainGap apart, or
// the engine has stopped.
func (e *Executor) residentChecked(ctx context.Context, ops *pinnedOps) ([]residentModel, error) {
	snap, err := ops.resident(ctx)
	if e.uncertain != "" {
		if err != nil {
			return nil, &pinnedError{Status: http.StatusConflict,
				Message: fmt.Sprintf("residency is uncertain after the failed load of %s and cannot be confirmed yet: %v", e.uncertain, err)}
		}
		if !e.confirmUncertain(ops, snap) {
			return nil, &pinnedError{Status: http.StatusConflict,
				Message: fmt.Sprintf("residency is uncertain after the failed load of %s: the model is not yet confirmed loaded or gone", e.uncertain)}
		}
	}
	if err != nil {
		return nil, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	return snap, nil
}

// confirmUncertain reports whether the uncertain reservation can be released.
func (e *Executor) confirmUncertain(ops *pinnedOps, snap []residentModel) bool {
	eng, model, _ := strings.Cut(e.uncertain, "/")
	release := func() bool {
		e.uncertain, e.uncertainAbsentSince = "", time.Time{}
		return true
	}
	for _, r := range snap {
		if r.Engine == eng && r.Model == model {
			e.uncertainAbsentSince = time.Time{}
			if !r.Loading {
				return release()
			}
			return false
		}
	}
	if ops.running != nil && !ops.running(eng) {
		return release()
	}
	gap := e.uncertainGap
	if gap <= 0 {
		gap = 5 * time.Second
	}
	now := time.Now()
	if e.uncertainAbsentSince.IsZero() {
		e.uncertainAbsentSince = now
		return false
	}
	if now.Sub(e.uncertainAbsentSince) >= gap {
		return release()
	}
	return false
}

// awaitLoaded keeps the gate held after an accepted load until the model
// reaches a terminal state: an engine that loads asynchronously (llama.cpp
// router) reports "loading", which already occupies memory. Bounded by the
// confirm deadline; a timeout leaves the reservation uncertain.
func (e *Executor) awaitLoaded(ctx context.Context, ops *pinnedOps, req PinnedRequest) error {
	key := req.Engine + "/" + req.Model
	deadline := time.Now().Add(e.unloadConfirmTimeout)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	timeout := func() error {
		e.uncertain = key
		return &pinnedError{Status: http.StatusGatewayTimeout, Message: "timed out waiting for " + key + " to finish loading"}
	}
	for first := true; ; first = false {
		snap, err := ops.resident(dctx)
		if err != nil {
			e.uncertain = key
			return &pinnedError{Status: http.StatusBadGateway, Message: "cannot confirm " + key + " finished loading: " + err.Error()}
		}
		state := "absent"
		for _, r := range snap {
			if r.Engine == req.Engine && r.Model == req.Model {
				state = "loaded"
				if r.Loading {
					state = "loading"
				}
			}
		}
		switch {
		case state == "loaded", state == "absent" && first:
			return nil // nothing to wait for (or the engine loads synchronously)
		case state == "absent":
			return &pinnedError{Status: http.StatusBadGateway, Message: key + " failed to load"}
		}
		if !time.Now().Before(deadline) {
			return timeout()
		}
		select {
		case <-dctx.Done():
			return timeout()
		case <-time.After(e.unloadPollInterval):
		}
	}
}

// exclusiveRun runs a load under the node-wide gate. load performs the engine
// call; nil means ops.load with req's parameters.
func (e *Executor) exclusiveRun(ctx context.Context, req PinnedRequest, load func(context.Context) error) (PinnedResult, error) {
	// Node-wide: pinned loads on different engines must not interleave.
	if !e.loadMu.TryLock() {
		return PinnedResult{}, &pinnedError{Status: http.StatusConflict, Message: "another pinned load is in progress on this node"}
	}
	defer e.loadMu.Unlock()

	ops := e.pinnedOps()
	if load == nil {
		load = func(ctx context.Context) error { return ops.load(ctx, req.Engine, req.Model, req.LoadOpts) }
	}
	snap, err := e.residentChecked(ctx, ops)
	if err != nil {
		return PinnedResult{}, err
	}
	victims, same := e.victimsOf(snap, req)
	res := PinnedResult{Engine: req.Engine, Model: req.Model, Applied: req.LoadOpts, Unloaded: []Victim{}}
	if len(victims) == 0 && same {
		res.AlreadyLoaded = true
		return res, nil
	}
	if len(victims) > 0 {
		if !req.Evict {
			return PinnedResult{}, &pinnedError{Status: http.StatusConflict, Victims: victims,
				Message: "loading would leave two large models resident; resend with evict=true to unload: " + describeVictims(victims)}
		}
		for _, v := range victims {
			if err := ops.unload(ctx, v.Engine, v.Model); err != nil {
				return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway,
					Message: fmt.Sprintf("unload %s/%s failed (nothing loaded): %v", v.Engine, v.Model, err)}
			}
			res.Unloaded = append(res.Unloaded, v)
		}
		same, err = e.confirmGone(ctx, ops, req, victims)
		if err != nil {
			return PinnedResult{}, err
		}
	}
	if same {
		res.AlreadyLoaded = true
		return res, nil
	}
	if err := load(ctx); err != nil {
		if pinnedStatus(err) != http.StatusBadRequest {
			// The engine may have accepted the load before the error or
			// cancellation; keep the reservation until a snapshot confirms.
			e.uncertain = req.Engine + "/" + req.Model
		}
		if pinnedStatus(err) != http.StatusInternalServerError {
			return PinnedResult{}, err
		}
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	if err := e.awaitLoaded(ctx, ops, req); err != nil {
		return PinnedResult{}, err
	}
	e.reportApplied(ctx, ops, req, &res)
	return res, nil
}

// confirmGone polls until every original victim is absent and nothing else
// large is resident, all within one confirm deadline that also bounds each
// snapshot. An empty result after the deadline does not count. It returns
// whether the target is now resident with the requested parameters.
func (e *Executor) confirmGone(ctx context.Context, ops *pinnedOps, req PinnedRequest, victims []Victim) (bool, error) {
	deadline := time.Now().Add(e.unloadConfirmTimeout)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	timeout := func(remain []Victim) error {
		return &pinnedError{Status: http.StatusGatewayTimeout, Victims: remain,
			Message: "timed out waiting for unload (nothing loaded): " + describeVictims(remain)}
	}
	for {
		snap, err := ops.resident(dctx)
		expired := !time.Now().Before(deadline)
		if err != nil {
			if expired {
				return false, timeout(victims)
			}
			return false, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
		}
		remain, same := e.victimsOf(snap, req)
		seen := map[string]bool{}
		for _, v := range remain {
			seen[v.Engine+"/"+v.Model] = true
		}
		for _, r := range snap {
			for _, v := range victims {
				if r.Engine == v.Engine && r.Model == v.Model && !seen[v.Engine+"/"+v.Model] {
					seen[v.Engine+"/"+v.Model] = true
					remain = append(remain, r.victim())
				}
			}
		}
		if len(remain) == 0 && !expired {
			return same, nil
		}
		if expired {
			return false, timeout(remain)
		}
		select {
		case <-dctx.Done():
			return false, timeout(remain)
		case <-time.After(e.unloadPollInterval):
		}
	}
}

// loadActions are the manifest actions that load a model into memory.
var loadActions = map[string]bool{"load_model": true, "run_model": true}

// guardedLoadAction runs a manifest load action (engine:action from the TUI)
// through the same exclusivity gate without evicting: a conflict is a 409.
func (e *Executor) guardedLoadAction(ctx context.Context, engine, action string, params json.RawMessage) (json.RawMessage, error) {
	model := modelFromParams(params)
	if _, ok := e.reg.Get(engine); !ok || model == "" {
		return nil, badParam(fmt.Sprintf("a load needs a known engine and a model (engine %q)", engine))
	}
	opts, err := loadOptsFromParams(engine, model, params)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	res, err := e.exclusiveRun(ctx, PinnedRequest{Engine: engine, Model: model, LoadOpts: opts}, func(ctx context.Context) (err error) {
		out, err = e.Action(ctx, engine, action, params)
		return err
	})
	if err == nil && out == nil {
		out, _ = json.Marshal(res)
	}
	return out, err
}

// loadOptsFromParams reads and validates the load options a TUI load action
// carries (context_length, parallel, Ollama options.num_ctx) so the gate sees
// the same request a pinned load would.
func loadOptsFromParams(engine, model string, params json.RawMessage) (LoadOpts, error) {
	var p struct {
		ContextLength int `json:"context_length"`
		Parallel      int `json:"parallel"`
		Options       struct {
			NumCtx int `json:"num_ctx"`
		} `json:"options"`
	}
	if len(params) > 0 && json.Unmarshal(params, &p) != nil {
		return LoadOpts{}, badParam("invalid load params: context_length, parallel and options.num_ctx must be integers")
	}
	o := LoadOpts{ContextLength: p.ContextLength, Parallel: p.Parallel}
	if o.ContextLength == 0 {
		o.ContextLength = p.Options.NumCtx
	}
	if _, _, err := modelLoadWire(engine, model, o); err != nil {
		return LoadOpts{}, err
	}
	return o, nil
}

// canonicalUnload runs unload_model with parameters built from the model name
// alone. Forwarding caller params would let an Ollama generate request without
// keep_alive:0 preload instead of unload.
func (e *Executor) canonicalUnload(ctx context.Context, engine string, params json.RawMessage) (json.RawMessage, error) {
	model := modelFromParams(params)
	if model == "" {
		return nil, badParam("unload_model needs a model")
	}
	return e.ModelUnload(ctx, engine, model)
}

// launchLoadsModel reports whether engine launch args name a model to load at
// startup (llama-server -m/-hf and friends).
func launchLoadsModel(args []string) bool {
	for _, a := range args {
		flag, _, _ := strings.Cut(a, "=")
		switch flag {
		case "-m", "--model", "-hf", "-hfr", "--hf-repo", "-mu", "--model-url":
			return true
		}
	}
	return false
}

// gateStartupLoad gates an engine start whose launch args load a model (an
// auto-start or custom launch): it refuses with 409 when a large model is
// already resident, else holds loadMu until the returned release runs so no
// pinned load interleaves with the start.
func (e *Executor) gateStartupLoad(ctx context.Context, engine string, args []string) (func(), error) {
	noop := func() {}
	if !launchLoadsModel(args) {
		return noop, nil
	}
	if !e.loadMu.TryLock() {
		return noop, &pinnedError{Status: http.StatusConflict, Message: "another model load is in progress on this node"}
	}
	snap, err := e.residentChecked(ctx, e.pinnedOps())
	if err != nil {
		e.loadMu.Unlock()
		return noop, err
	}
	if victims, _ := e.victimsOf(snap, PinnedRequest{Engine: engine}); len(victims) > 0 {
		e.loadMu.Unlock()
		return noop, &pinnedError{Status: http.StatusConflict, Victims: victims,
			Message: "starting " + engine + " with a model would leave two large models resident: " + describeVictims(victims)}
	}
	return e.loadMu.Unlock, nil
}

// reportApplied fills Applied from what the engine reports it actually loaded
// and warns when a requested value did not take effect.
func (e *Executor) reportApplied(ctx context.Context, ops *pinnedOps, req PinnedRequest, res *PinnedResult) {
	si, ok := ops.applied(ctx, req.Engine, req.Model)
	if !ok {
		return
	}
	if req.ContextLength > 0 && si.CtxPerSlot > 0 {
		res.Applied.ContextLength = si.CtxPerSlot
		if si.CtxPerSlot != req.ContextLength {
			res.Warnings = append(res.Warnings, fmt.Sprintf("requested context_length %d but engine reports %d", req.ContextLength, si.CtxPerSlot))
		}
	}
	if req.Parallel > 0 && si.Slots > 0 {
		res.Applied.Parallel = si.Slots
		if si.Slots != req.Parallel {
			res.Warnings = append(res.Warnings, fmt.Sprintf("requested parallel %d but engine reports %d", req.Parallel, si.Slots))
		}
	}
}

func describeVictims(vs []Victim) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.Engine + "/" + v.Model
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// observeLarge is the watcher's state machine: warn once when two or more large
// models become resident, and re-arm when the count drops below two. It never
// unloads anything.
func (e *Executor) observeLarge(names []string) {
	if len(names) < 2 {
		e.multiLargeWarned = false
		return
	}
	if e.multiLargeWarned {
		return
	}
	e.multiLargeWarned = true
	sort.Strings(names)
	if e.onMultiLarge != nil {
		e.onMultiLarge(names)
		return
	}
	slog.Warn("multiple large models are resident on this node; loads that bypass engine-manager (JIT/inference) are not prevented",
		"models", names)
}

// checkMultiLarge feeds observeLarge from a models sweep. Size lookups only
// happen when at least two models are resident.
func (e *Executor) checkMultiLarge(ctx context.Context, res ModelsResult) {
	total := 0
	for _, l := range res.LoadedByEngine {
		total += len(l)
	}
	if total < 2 {
		e.observeLarge(nil)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	snap := e.residentFrom(ctx, res)
	var large []string
	for _, r := range snap {
		if e.isLarge(r) {
			large = append(large, r.Engine+"/"+r.Model)
		}
	}
	e.observeLarge(large)
}
