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

import (
	"context"
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
}

func (r residentModel) victim() Victim {
	return Victim{Engine: r.Engine, Model: r.Model, Bytes: r.Bytes}
}

var paramTokenRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.])(\d+(?:\.\d+)?)b(?:$|[^a-z0-9])`)

// isLargeModel: by resident bytes when known; else by the largest "<n>b"
// parameter token in the name; with neither, large (fail safe).
func isLargeModel(bytes int64, name string, thresholdBytes int64) bool {
	if bytes > 0 {
		return bytes >= thresholdBytes
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
	return !found
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
}

func (e *Executor) pinnedOps() *pinnedOps {
	if e.ops != nil {
		return e.ops
	}
	return &pinnedOps{
		resident: func(ctx context.Context) ([]residentModel, error) {
			ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
			defer cancel()
			return e.residentFrom(ctx, e.ModelsResult(ctx))
		},
		unload: func(ctx context.Context, engine, model string) error {
			_, err := e.ModelUnload(ctx, engine, model)
			return err
		},
		load: func(ctx context.Context, engine, model string, o LoadOpts) error {
			_, err := e.ModelLoadWith(ctx, engine, model, o)
			return err
		},
		applied: func(ctx context.Context, engine, model string) (SlotInfo, bool) {
			si, ok := e.ModelsResult(ctx).SlotsByEngine[engine][model]
			return si, ok
		},
	}
}

// residentFrom turns a models sweep into the resident list, adding byte sizes
// from each engine's model_sizes action. It fails closed: a running engine that
// declares loaded_models but did not report it leaves residency unknown.
func (e *Executor) residentFrom(ctx context.Context, res ModelsResult) ([]residentModel, error) {
	var out []residentModel
	for _, name := range e.reg.Names() {
		mf, ok := e.reg.Get(name)
		if !ok {
			continue
		}
		if _, declares := mf.Actions["loaded_models"]; declares {
			if st, err := e.Status(name); err == nil && st.Running {
				if _, reported := res.LoadedByEngine[name]; !reported {
					return nil, fmt.Errorf("cannot determine which models engine %q has loaded", name)
				}
			}
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
	return out, nil
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
	// Node-wide: pinned loads on different engines must not interleave.
	if !e.loadMu.TryLock() {
		return PinnedResult{}, &pinnedError{Status: http.StatusConflict, Message: "another pinned load is in progress on this node"}
	}
	defer e.loadMu.Unlock()

	ops := e.pinnedOps()
	snap, err := ops.resident(ctx)
	if err != nil {
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
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
		same, err = e.confirmGone(ctx, ops, req)
		if err != nil {
			return PinnedResult{}, err
		}
	}
	if same {
		res.AlreadyLoaded = true
		return res, nil
	}
	if err := ops.load(ctx, req.Engine, req.Model, req.LoadOpts); err != nil {
		if pinnedStatus(err) != http.StatusInternalServerError {
			return PinnedResult{}, err
		}
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	e.reportApplied(ctx, ops, req, &res)
	return res, nil
}

// confirmGone polls until no victim remains resident (a model that appears
// meanwhile, e.g. JIT, counts too) or the confirm timeout expires. It returns
// whether the target is now resident with the requested parameters.
func (e *Executor) confirmGone(ctx context.Context, ops *pinnedOps, req PinnedRequest) (bool, error) {
	deadline := time.Now().Add(e.unloadConfirmTimeout)
	for {
		snap, err := ops.resident(ctx)
		if err != nil {
			return false, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
		}
		remain, same := e.victimsOf(snap, req)
		if len(remain) == 0 {
			return same, nil
		}
		if !time.Now().Before(deadline) {
			return false, &pinnedError{Status: http.StatusGatewayTimeout, Victims: remain,
				Message: "timed out waiting for unload (nothing loaded): " + describeVictims(remain)}
		}
		select {
		case <-ctx.Done():
			return false, &pinnedError{Status: http.StatusGatewayTimeout, Message: ctx.Err().Error()}
		case <-time.After(e.unloadPollInterval):
		}
	}
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
	snap, err := e.residentFrom(ctx, res)
	if err != nil {
		return
	}
	var large []string
	for _, r := range snap {
		if e.isLarge(r) {
			large = append(large, r.Engine+"/"+r.Model)
		}
	}
	e.observeLarge(large)
}
