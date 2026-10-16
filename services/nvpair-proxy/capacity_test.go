package main

import (
	"encoding/json"
	"testing"
	"time"
)

// fakeCap is a CapacityProvider keyed by node id.
type fakeCap struct {
	slots map[string]int
	res   map[string]Residency
	jit   map[string]bool
	seen  time.Time
}

func (f *fakeCap) Slots(id, _, _ string) (int, bool)             { n, ok := f.slots[id]; return n, ok }
func (f *fakeCap) CtxPerSlot(string, string, string) (int, bool) { return 0, false }
func (f *fakeCap) Residency(id, _, _ string) (Residency, time.Time) {
	return f.res[id], f.seen
}
func (f *fakeCap) JITLoad(id, _ string) bool { return f.jit[id] }

func TestParseCapacitySettings(t *testing.T) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"engine_slots.lmstudio.qwen3.8-27b-mlx":4,"engine_ctx_per_slot.lmstudio.m":"8192","jit_load.ollama":false}`), &raw)
	c := parseCapacitySettings(raw)
	if n, ok := c.Slots("n", "lmstudio", "qwen3.8-27b-mlx"); !ok || n != 4 {
		t.Fatalf("slots = %d %v", n, ok)
	}
	if n, ok := c.CtxPerSlot("n", "lmstudio", "m"); !ok || n != 8192 {
		t.Fatalf("ctx = %d %v", n, ok)
	}
	if c.JITLoad("n", "ollama") || c.JITLoad("n", "lmstudio") || !c.JITLoad("n", "llamacpp") {
		t.Fatal("jit defaults/override wrong")
	}
}
