// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const gib = int64(1) << 30

// fakeOps simulates engines in memory and records the call order.
type fakeOps struct {
	mu        sync.Mutex
	res       []residentModel
	calls     []string
	unloadErr error
	stuck     bool // unload succeeds but the model stays resident
	shrink    bool // ... and then reports a small size
	loadErr   error
	snapErr   error
	loadHook  func()
}

func (f *fakeOps) ops() *pinnedOps {
	return &pinnedOps{
		resident: func(context.Context) ([]residentModel, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.snapErr != nil {
				return nil, f.snapErr
			}
			return append([]residentModel(nil), f.res...), nil
		},
		unload: func(_ context.Context, engine, model string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, "unload "+engine+"/"+model)
			if f.unloadErr != nil {
				return f.unloadErr
			}
			if f.stuck && f.shrink {
				for i := range f.res {
					f.res[i].Bytes = gib
				}
			}
			if !f.stuck {
				for i, r := range f.res {
					if r.Engine == engine && r.Model == model {
						f.res = append(f.res[:i], f.res[i+1:]...)
						break
					}
				}
			}
			return nil
		},
		load: func(_ context.Context, engine, model string, _ LoadOpts) error {
			if f.loadHook != nil {
				f.loadHook()
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, "load "+engine+"/"+model)
			return f.loadErr
		},
		applied: func(context.Context, string, string) (SlotInfo, bool) { return SlotInfo{}, false },
	}
}

func exclusiveExec(t *testing.T, f *fakeOps) *Executor {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "lmstudio"
	ex := newTestExecutor(t, m)
	ex.ops = f.ops()
	ex.unloadConfirmTimeout, ex.unloadPollInterval = 20*time.Millisecond, time.Millisecond
	return ex
}

func TestLargeRule(t *testing.T) {
	thr := 12 * gib
	cases := []struct {
		bytes int64
		name  string
		want  bool
	}{
		{20 * gib, "tiny", true},        // by bytes
		{2 * gib, "qwen3.8:27b", true},  // a >=20b name token is large whatever the bytes
		{2 * gib, "llama3.2:3b", false}, // small bytes, small token
		{0, "qwen3.8:27b", true},        // by name
		{0, "llama3.2:3b", false},       // small by name
		{0, "mystery-model", true},      // unknown -> large
	}
	for _, c := range cases {
		if got := isLargeModel(c.bytes, c.name, thr); got != c.want {
			t.Errorf("isLargeModel(%d,%q)=%v want %v", c.bytes, c.name, got, c.want)
		}
	}
}

func TestVictims(t *testing.T) {
	ex := newTestExecutor(t, testEngineManifest(fakeEngineBin))
	ex.largeModelBytes = 12 * gib
	snap := []residentModel{
		{Engine: "lmstudio", Model: "qwen3.8:27b", Bytes: 16 * gib, HasSlots: true, Slots: SlotInfo{Slots: 1, CtxPerSlot: 16384}},
		{Engine: "ollama", Model: "qwen-big", Bytes: 20 * gib},
		{Engine: "ollama", Model: "llama3.2:3b", Bytes: 2 * gib},
	}
	// Target on llama.cpp: both large models are victims; the 3b is kept.
	v, same := ex.victimsOf(snap, PinnedRequest{Engine: "llamacpp", Model: "other-30b"})
	if same || len(v) != 2 || v[0].Model != "qwen3.8:27b" || v[1].Model != "qwen-big" {
		t.Fatalf("victims %v same=%v", v, same)
	}
	// Same model, same params: no-op; the other large model is still a victim.
	req := PinnedRequest{Engine: "lmstudio", Model: "qwen3.8:27b", LoadOpts: LoadOpts{ContextLength: 16384}}
	v, same = ex.victimsOf(snap, req)
	if !same || len(v) != 1 || v[0].Model != "qwen-big" {
		t.Fatalf("same-params victims %v same=%v", v, same)
	}
	// Different params: the target itself is listed.
	req.ContextLength = 32768
	v, same = ex.victimsOf(snap, req)
	if same || len(v) != 2 || v[0].Model != "qwen3.8:27b" {
		t.Fatalf("diff-params victims %v same=%v", v, same)
	}
	// A small target evicts nothing.
	if v, _ = ex.victimsOf(snap, PinnedRequest{Engine: "ollama", Model: "phi:3b"}); len(v) != 0 {
		t.Fatalf("small target victims %v", v)
	}
}

func TestEvictFalseConflicts(t *testing.T) {
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b"}}}
	ex := exclusiveExec(t, f)
	_, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b"})
	var pe *pinnedError
	if !errors.As(err, &pe) || pe.Status != 409 || len(pe.Victims) != 1 || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestEvictOrderAndFailure(t *testing.T) {
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b"}}}
	ex := exclusiveExec(t, f)
	res, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b", Evict: true})
	if err != nil || !reflect.DeepEqual(f.calls, []string{"unload ollama/qwen-30b", "load lmstudio/big-27b"}) || len(res.Unloaded) != 1 {
		t.Fatalf("res=%+v err=%v calls=%v", res, err, f.calls)
	}
	f = &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b"}}, unloadErr: errors.New("boom")}
	ex = exclusiveExec(t, f)
	_, err = ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b", Evict: true})
	if pinnedStatus(err) != 502 || !reflect.DeepEqual(f.calls, []string{"unload ollama/qwen-30b"}) {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestConfirmTimeout(t *testing.T) {
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b"}}, stuck: true}
	ex := exclusiveExec(t, f)
	_, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b", Evict: true})
	if pinnedStatus(err) != 504 || len(f.calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestConcurrentLoadsConflict(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := &fakeOps{loadHook: func() { close(entered); <-release }}
	ex := exclusiveExec(t, f)
	done := make(chan error, 1)
	go func() {
		_, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b"})
		done <- err
	}()
	<-entered
	_, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "other-27b"})
	close(release)
	if pinnedStatus(err) != 409 || <-done != nil {
		t.Fatalf("second load err=%v", err)
	}
}

func TestMultiLargeWarningOnce(t *testing.T) {
	ex := newTestExecutor(t, testEngineManifest(fakeEngineBin))
	warnings := 0
	ex.onMultiLarge = func([]string) { warnings++ }
	two := []string{"a/x-30b", "b/y-70b"}
	ex.observeLarge(two)
	ex.observeLarge(two)
	if warnings != 1 {
		t.Fatalf("warnings=%d want 1", warnings)
	}
	ex.observeLarge(two[:1]) // one unloaded: cleared
	ex.observeLarge(two)
	if warnings != 2 {
		t.Fatalf("warnings=%d want 2 after re-arm", warnings)
	}
}

func TestLegacyLoadConflicts(t *testing.T) {
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b"}}}
	ex := exclusiveExec(t, f)
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"engine":"lmstudio","model":"big-27b"}`)
	(&controlServer{exec: ex}).handleModelAction(w, httptest.NewRequest("POST", "/v1/models/load", body), "load")
	if w.Code != 409 || len(f.calls) != 0 {
		t.Fatalf("code=%d calls=%v", w.Code, f.calls)
	}
}

func TestUncertainLoadBlocksNext(t *testing.T) {
	f := &fakeOps{loadErr: errors.New("transport closed")}
	ex := exclusiveExec(t, f)
	req := PinnedRequest{Engine: "lmstudio", Model: "big-27b"}
	if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 502 {
		t.Fatalf("first err=%v", err)
	}
	f.snapErr = errors.New("down")
	if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 409 {
		t.Fatalf("blocked err=%v", err)
	}
	f.snapErr, f.loadErr = nil, nil
	if _, err := ex.PinnedLoad(context.Background(), req); err != nil {
		t.Fatalf("after confirm err=%v", err)
	}
}

func TestStrictResidencyFailsClosed(t *testing.T) {
	spec := &ActionResult{Array: "data", Field: "id", Match: &ResultMatch{Field: "status.value", In: []string{"loaded"}}}
	ok := `{"data":[{"id":"a","status":{"value":"loaded"}},{"id":"b","status":{"value":"unloaded"}}]}`
	if got, err := extractLoadedStrict([]byte(ok), spec); err != nil || len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v err=%v", got, err)
	}
	if _, err := extractLoadedStrict([]byte(`{"data":[{"id":"a"}]}`), spec); err == nil {
		t.Fatal("row without status must fail closed")
	}
}

func TestVictimStillResidentTimesOut(t *testing.T) {
	// The stuck victim later reports a small size; it must still count.
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "mystery", Bytes: 20 * gib}}, stuck: true, shrink: true}
	ex := exclusiveExec(t, f)
	_, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b", Evict: true})
	if pinnedStatus(err) != 504 || len(f.calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}
