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

func TestDirectorySuppressionLiftedByNewerUpdate(t *testing.T) {
	now := time.Unix(1000, 0)
	d := newDirectoryCapacity(func() time.Time { return now })
	p := &Proxy{suppressed: map[inflightKey]time.Time{}, now: func() time.Time { return now }}
	p.capacity = d
	hot := dnode("n", []string{"m"}, nil, 5)
	d.update("ollama", []noderec.DirectoryNode{hot})
	now = now.Add(time.Second)
	p.suppressed[inflightKey{"ollama", "m", "n"}] = now
	now = now.Add(time.Second)
	d.update("ollama", []noderec.DirectoryNode{hot}) // unrelated resend: same inventory stamp
	if p.eligible("n", "ollama", "m") {
		t.Fatal("stale resend must not lift suppression")
	}
	hot.ModelsObservedAt = 6
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
