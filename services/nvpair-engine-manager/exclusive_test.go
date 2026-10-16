// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const gib = int64(1) << 30

// fakeOps simulates engines in memory and records the call order.
type fakeOps struct {
	mu         sync.Mutex
	res        []residentModel
	calls      []string
	unloadErr  error
	stuck      bool // unload succeeds but the model stays resident
	shrink     bool // ... and then reports a small size
	loadErr    error
	snapErr    error
	loadHook   func()
	loadingFor int // resident calls before a Loading model becomes loaded
}

func (f *fakeOps) ops() *pinnedOps {
	return &pinnedOps{
		resident: func(context.Context) ([]residentModel, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.snapErr != nil {
				return nil, f.snapErr
			}
			if f.loadingFor > 0 {
				if f.loadingFor--; f.loadingFor == 0 {
					for i := range f.res {
						f.res[i].Loading = false
					}
				}
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
	ex.uncertainGap = time.Nanosecond
	req := PinnedRequest{Engine: "lmstudio", Model: "big-27b"}
	if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 502 {
		t.Fatalf("first err=%v", err)
	}
	f.snapErr = errors.New("down")
	if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 409 {
		t.Fatalf("blocked err=%v", err)
	}
	f.snapErr, f.loadErr = nil, nil
	// Still loading: never cleared, however many snapshots.
	f.res = []residentModel{{Engine: "lmstudio", Model: "big-27b", Loading: true}}
	for i := 0; i < 2; i++ {
		if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 409 {
			t.Fatalf("loading i=%d err=%v unc=%q", i, err, ex.uncertain)
		}
	}
	// Absent once is not enough; absent twice releases.
	f.res = nil
	if _, err := ex.PinnedLoad(context.Background(), req); pinnedStatus(err) != 409 {
		t.Fatalf("first absent err=%v", err)
	}
	if _, err := ex.PinnedLoad(context.Background(), req); err != nil {
		t.Fatalf("second absent err=%v", err)
	}
}

func TestLoadingCountsAndGateHeldUntilLoaded(t *testing.T) {
	f := &fakeOps{res: []residentModel{{Engine: "ollama", Model: "qwen-30b", Loading: true}}}
	ex := exclusiveExec(t, f)
	if _, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b"}); pinnedStatus(err) != 409 {
		t.Fatalf("a loading large model must conflict, err=%v", err)
	}
	f = &fakeOps{}
	ex = exclusiveExec(t, f)
	f.loadingFor = 2
	f.loadHook = func() { f.res = append(f.res, residentModel{Engine: "lmstudio", Model: "big-27b", Loading: true}) }
	if _, err := ex.PinnedLoad(context.Background(), PinnedRequest{Engine: "lmstudio", Model: "big-27b"}); err != nil {
		t.Fatalf("err=%v", err)
	}
	if f.loadingFor != 0 {
		t.Fatalf("returned before the model finished loading (loadingFor=%d)", f.loadingFor)
	}
}

func TestStatusNoWaitDoesNotDeadlockUnderLifecycleLock(t *testing.T) {
	ex := exclusiveExec(t, &fakeOps{})
	st, err := ex.state("lmstudio")
	if err != nil {
		t.Fatal(err)
	}
	st.opMu.Lock()
	defer st.opMu.Unlock()
	done := make(chan struct{})
	go func() { _, _ = ex.statusNoWait(context.Background(), "lmstudio"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("statusNoWait blocked on the lifecycle lock")
	}
}

func TestUnloadParamsAreCanonical(t *testing.T) {
	_, p, err := modelActionWire("ollama", "unload", "big-27b")
	if err != nil || !strings.Contains(string(p), `"keep_alive":0`) {
		t.Fatalf("params=%s err=%v", p, err)
	}
	ex := exclusiveExec(t, &fakeOps{})
	if _, err := ex.canonicalUnload(context.Background(), "ollama", []byte(`{"stream":false}`)); pinnedStatus(err) != 400 {
		t.Fatalf("model-less unload must be 400, err=%v", err)
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
	if _, err := extractLoadedStrict([]byte(`{"data":[{"id":"a","status":{"value":null}}]}`), spec); err == nil {
		t.Fatal("null filter value must fail closed")
	}
	ne := &ActionResult{Array: "models", Field: "key", Match: &ResultMatch{Field: "loaded_instances", Nonempty: true}}
	if _, err := extractLoadedStrict([]byte(`{"models":[{"key":"a","loaded_instances":null}]}`), ne); err == nil {
		t.Fatal("null loaded_instances must fail closed")
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

// LM Studio groups variants under one model key; a second loaded variant must
// be its own resident instance and a victim of a pinned load.
func TestLMStudioInstancesAreResidentAndVictims(t *testing.T) {
	data, err := os.ReadFile("manifests/lmstudio.json")
	if err != nil {
		t.Fatal(err)
	}
	var mf Manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatal(err)
	}
	inst := func(id string, par int) string {
		return `{"id":"` + id + `","config":{"parallel":` + strconv.Itoa(par) + `,"context_length":8192}}`
	}
	shapes := map[string]string{
		"one key two instances": `{"models":[{"key":"qwen/qwen3.8-27b","size_bytes":16081678492,"loaded_instances":[` +
			inst("qwen/qwen3.8-27b", 4) + `,` + inst("qwen3.8-27b-mlx", 4) + `]}]}`,
		"two keys": `{"models":[{"key":"qwen/qwen3.8-27b","size_bytes":16081678492,"loaded_instances":[` + inst("qwen/qwen3.8-27b", 4) + `]},` +
			`{"key":"qwen3.8-27b-mlx","size_bytes":29500000000,"loaded_instances":[` + inst("qwen3.8-27b-mlx", 4) + `]}]}`,
	}
	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			raw := json.RawMessage(body)
			loaded, err := extractLoadedStrict(raw, mf.Actions["loaded_models"].Result)
			if err != nil || len(loaded) != 2 {
				t.Fatalf("loaded %v err=%v", loaded, err)
			}
			sizes, _ := extractRecords(raw, mf.Actions["model_sizes"].Result)
			ex := newTestExecutor(t, testEngineManifest(fakeEngineBin))
			ex.largeModelBytes = 12 * gib
			var snap []residentModel
			for _, m := range loaded {
				snap = append(snap, residentModel{Engine: "lmstudio", Model: m, Bytes: sizes[m].Bytes,
					HasSlots: true, Slots: SlotInfo{Slots: 4, CtxPerSlot: 8192}})
			}
			v, same := ex.victimsOf(snap, PinnedRequest{Engine: "lmstudio", Model: "qwen/qwen3.8-27b", LoadOpts: LoadOpts{Parallel: 4}})
			if !same || len(v) != 1 || v[0].Model != "qwen3.8-27b-mlx" {
				t.Fatalf("victims %v same=%v", v, same)
			}
		})
	}

	loadedSpec, sizeSpec, slotSpec := mf.Actions["loaded_models"].Result, mf.Actions["model_sizes"].Result, mf.Actions["slots"].Result
	t.Run("P1 sibling with 8b name is large", func(t *testing.T) {
		raw := json.RawMessage(`{"models":[{"key":"qwen/qwen3.8-27b","size_bytes":16081678492,"loaded_instances":[` +
			inst("qwen/qwen3.8-27b", 4) + `,` + inst("model-8b:2", 4) + `]}]}`)
		sizes, _ := extractRecords(raw, sizeSpec)
		if !isLargeModel(sizes["model-8b:2"].Bytes, "model-8b:2", 12*gib) {
			t.Fatalf("sibling classified small: %v", sizes)
		}
	})
	t.Run("P3 missing id fails closed", func(t *testing.T) {
		raw := json.RawMessage(`{"models":[{"key":"k","loaded_instances":[` + inst("k", 4) + `,{"config":{}}]}]}`)
		if _, err := extractLoadedStrict(raw, loadedSpec); err == nil {
			t.Fatal("unidentifiable instance must fail closed")
		}
		if got, ok := extractStringsResult(raw, loadedSpec); !ok || len(got) != 1 || got[0] != "k" {
			t.Fatalf("non-strict should skip it: %v %v", got, ok)
		}
	})
	t.Run("P2 alias in loaded list with matching slots", func(t *testing.T) {
		raw := json.RawMessage(`{"models":[{"key":"K","loaded_instances":[` + inst("alias", 3) + `]}]}`)
		got, ok := extractStringsResult(raw, loadedSpec)
		slots, _ := extractRecords(raw, slotSpec)
		if !ok || len(got) != 1 || got[0] != "alias" || slots["alias"].Slots != 3 {
			t.Fatalf("loaded=%v slots=%v", got, slots)
		}
	})
}
