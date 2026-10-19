// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"testing"
	"time"

	"nvpair-shared/noderec"
)

func dnode(id string, loaded []string, slots map[string]noderec.SlotInfo, obs int64) noderec.DirectoryNode {
	n := noderec.DirectoryNode{HostUUID: id, ModelsObservedAt: obs}
	if loaded != nil {
		n.LoadedByEngine = map[string][]string{"ollama": loaded}
	}
	if slots != nil {
		n.SlotsByEngine = map[string]map[string]noderec.SlotInfo{"ollama": slots}
	}
	return n
}

func TestDirectoryResidencyAndProjection(t *testing.T) {
	d := newDirectoryCapacity(nil)
	d.update("ollama", []noderec.DirectoryNode{
		dnode("legacy", nil, nil, 0),
		dnode("empty", []string{}, nil, 1),
		dnode("hot", []string{"m"}, map[string]noderec.SlotInfo{"m": {Slots: 3, CtxPerSlot: 4096}}, 1),
	})
	for id, want := range map[string]Residency{"legacy": ResidencyUnknown, "empty": ResidencyKnownEmpty, "hot": ResidencyLoaded, "gone": ResidencyUnknown} {
		if got, _ := d.Residency(id, "ollama", "m"); got != want {
			t.Errorf("%s residency = %v, want %v", id, got, want)
		}
	}
	if n, ok := d.Slots("hot", "ollama", "m"); !ok || n != 3 {
		t.Errorf("slots = %d %v", n, ok)
	}
	if n, ok := d.CtxPerSlot("hot", "ollama", "m"); !ok || n != 4096 {
		t.Errorf("ctx = %d %v", n, ok)
	}
	if _, ok := d.Slots("empty", "ollama", "m"); ok {
		t.Error("slots for non-loaded model")
	}
}

func TestDirectorySuppressionLiftedByNewerObservation(t *testing.T) {
	now := time.Unix(1000, 0)
	d := newDirectoryCapacity(nil)
	p := &Proxy{suppressed: map[inflightKey]time.Time{}, now: func() time.Time { return now }}
	p.capacity = d
	hot := dnode("n", []string{"m"}, nil, now.Add(-time.Second).UnixMilli())
	p.suppressed[inflightKey{"ollama", "m", "n"}] = now
	d.update("ollama", []noderec.DirectoryNode{hot}) // delivered late, observed before the refusal
	if p.eligible("n", "ollama", "m") {
		t.Fatal("sweep observed before the refusal must not lift suppression")
	}
	hot.ModelsObservedAt = now.Add(time.Second).UnixMilli()
	d.update("ollama", []noderec.DirectoryNode{hot})
	if !p.eligible("n", "ollama", "m") {
		t.Fatal("newer sweep showing Loaded must lift suppression")
	}
}

func TestMinContextLength(t *testing.T) {
	d := newDirectoryCapacity(nil)
	info := func(c int) map[string]noderec.SlotInfo {
		return map[string]noderec.SlotInfo{"m": {Slots: 1, CtxPerSlot: c}}
	}
	d.update("ollama", []noderec.DirectoryNode{
		dnode("a", []string{"m"}, info(8192), 1),
		dnode("b", []string{"m"}, info(4096), 1),
		dnode("nov", []string{"m"}, map[string]noderec.SlotInfo{"m": {Slots: 1}}, 1),
		dnode("cold", []string{}, nil, 1),
	})
	d.update("lmstudio", nil)
	p := &Proxy{capacity: overrideCapacity{over: settingsCapacity{jit: map[string]bool{"ollama": false}}, base: d}}
	if n, ok := p.minContextLength("ollama", "m", []string{"a", "b", "cold"}); !ok || n != 4096 {
		t.Fatalf("min = %d %v (cold, non-JIT node must be ignored)", n, ok)
	}
	if _, ok := p.minContextLength("ollama", "m", []string{"a", "nov"}); ok {
		t.Fatal("eligible node without a value must omit the field")
	}
	raw := withContextLength(json.RawMessage(`{"id":"m"}`), 4096)
	if string(raw) != `{"context_length":4096,"id":"m"}` {
		t.Fatalf("raw = %s", raw)
	}
}

func TestJITBlockedWhenOtherEngineHoldsModel(t *testing.T) {
	d := newDirectoryCapacity(nil)
	busy := dnode("busy", []string{}, nil, 1)
	busy.LoadedByEngine["lmstudio"] = []string{"big"}
	free := dnode("free", []string{}, nil, 1)
	free.LoadedByEngine["lmstudio"] = []string{}
	d.update("ollama", []noderec.DirectoryNode{busy, free})
	p := &Proxy{capacity: d}
	if p.eligible("busy", "ollama", "m") {
		t.Fatal("JIT must not add a second model beside another engine's")
	}
	if !p.eligible("free", "ollama", "m") {
		t.Fatal("JIT is fine when no other engine has a model loaded")
	}
}

func TestJITClaimBlocksOtherModel(t *testing.T) {
	d := newDirectoryCapacity(nil)
	d.update("ollama", []noderec.DirectoryNode{dnode("n", []string{}, nil, 1)})
	p := &Proxy{capacity: d}
	res := p.takeLocked("n", "ollama", "m", false)
	if !res.jit || p.eligible("n", "ollama", "m2") || !p.eligible("n", "ollama", "m") {
		t.Fatal("claim must block a different model only")
	}
	p.dropLocked(res)
	if p.eligible("n", "ollama", "m2") {
		t.Fatal("released claim must persist until a newer sweep")
	}

}

func TestReserveRechecksEligibility(t *testing.T) {
	d := newDirectoryCapacity(nil)
	d.update("ollama", []noderec.DirectoryNode{dnode("n", []string{}, nil, 1), dnode("o", []string{"m"}, nil, 1)})
	p := affinityProxy(t, "n", "o")
	p.capacity = d
	f := p.soleFacade()
	p.takeLocked("n", "ollama", "other", false) // another model claims n
	round := []candidate{{id: "n"}, {id: "o"}}
	cand, ok := p.rescoreReserve(f, round, map[string]bool{}, &reservation{}, "m")
	if !ok || cand.id != "o" {
		t.Fatalf("got %v %v, want o", cand.id, ok)
	}
}

func TestObservedAtAndModelKey(t *testing.T) {
	d := newDirectoryCapacity(nil)
	n := dnode("n", []string{"m:latest"}, map[string]noderec.SlotInfo{"m:latest": {Slots: 2, CtxPerSlot: 99}}, 5000)
	d.update("ollama", []noderec.DirectoryNode{n})
	r, at := d.Residency("n", "ollama", "m")
	if r != ResidencyLoaded || !at.Equal(time.UnixMilli(5000)) {
		t.Fatalf("residency %v at %v", r, at)
	}
	if s, ok := d.Slots("n", "ollama", "m"); !ok || s != 2 {
		t.Fatal("slots must match m vs m:latest")
	}
}

func TestContextLengthReplacedOrRemoved(t *testing.T) {
	if got := string(withContextLength(json.RawMessage(`{"id":"m","context_length":999999}`), 4096)); got != `{"context_length":4096,"id":"m"}` {
		t.Fatal(got)
	}
	if got := string(withContextLength(json.RawMessage(`{"id":"m","context_length":9}`), 0)); got != `{"id":"m"}` {
		t.Fatal(got)
	}
}

func TestJITReleasedClaimPersistsUntilSweepOrCap(t *testing.T) {
	now := time.Now()
	d := newDirectoryCapacity(nil)
	d.update("ollama", []noderec.DirectoryNode{dnode("n", []string{}, nil, now.UnixMilli())})
	p := &Proxy{capacity: d, now: func() time.Time { return now }}
	p.dropLocked(p.takeLocked("n", "ollama", "m", false))
	if p.eligible("n", "ollama", "m2") {
		t.Fatal("claim must persist after request end")
	}
	now = now.Add(jitReleaseCap)
	if !p.eligible("n", "ollama", "m2") {
		t.Fatal("claim must expire at the cap")
	}
	p.dropLocked(p.takeLocked("n", "ollama", "m", false))
	d.update("ollama", []noderec.DirectoryNode{dnode("n", []string{}, nil, now.Add(time.Second).UnixMilli())})
	if !p.eligible("n", "ollama", "m2") {
		t.Fatal("a newer sweep must clear the claim")
	}
}

func TestSelectedNodeHonoursJITClaim(t *testing.T) {
	p := affinityProxy(t, "a")
	d := newDirectoryCapacity(nil)
	d.update("ollama", []noderec.DirectoryNode{dnode("a", []string{}, nil, 1)})
	p.capacity = d
	f := p.soleFacade()
	f.SetSelected("a")
	engine := f.profile.Name
	res := p.takeLocked("a", engine, "m", false)
	if !res.jit {
		t.Skip("engine is not JIT on this fixture")
	}
	if out, _, _ := p.reserveCandidateFor(f, reservationCandidates("a"), "", "m2"); len(out) != 0 {
		t.Fatal("selected node must honour a JIT claim for another model")
	}
	if out, _, _ := p.reserveCandidateFor(f, reservationCandidates("a"), "", "m"); len(out) != 1 {
		t.Fatal("same model stays allowed")
	}
}
