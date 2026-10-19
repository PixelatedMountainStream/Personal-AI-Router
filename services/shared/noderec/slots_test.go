// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDirectoryNodeSlotsRoundTrip(t *testing.T) {
	orig := DirectoryNode{HostUUID: "h", SlotsByEngine: map[string]map[string]SlotInfo{"ollama": {"q": {Slots: 4, CtxPerSlot: 8192}}}, JITLoadByEngine: map[string]bool{"lmstudio": false}}
	b, _ := json.Marshal(orig)
	var got DirectoryNode
	if err := json.Unmarshal(b, &got); err != nil || !reflect.DeepEqual(got, orig) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	// Back-compat: a record without the fields decodes to nil, and a nil field is omitted.
	var old DirectoryNode
	if err := json.Unmarshal([]byte(`{"hostUuid":"h","loadedByEngine":{"ollama":["q"]}}`), &old); err != nil || old.SlotsByEngine != nil || old.JITLoadByEngine != nil {
		t.Fatalf("legacy decode = %+v, %v", old, err)
	}
	if b, _ := json.Marshal(DirectoryNode{HostUUID: "h"}); strings.Contains(string(b), "slotsByEngine") || strings.Contains(string(b), "jitLoadByEngine") {
		t.Fatalf("nil fields must be omitted: %s", b)
	}
}

func TestApplyCapacityOverrides(t *testing.T) {
	raw := map[string]json.RawMessage{
		"engine_slots.lmstudio.qwen3.8-27b-mlx":        json.RawMessage(`4`),
		"engine_ctx_per_slot.lmstudio.qwen3.8-27b-mlx": json.RawMessage(`"32768"`),
		"engine_slots.lmstudio.not-loaded":             json.RawMessage(`2`),
		"engine_slots.ollama.q":                        json.RawMessage(`0`),
		"jit_load.lmstudio":                            json.RawMessage(`true`),
	}
	loaded := map[string][]string{"lmstudio": {"qwen3.8-27b-mlx"}, "ollama": {"q"}}
	reported := map[string]map[string]SlotInfo{"ollama": {"q": {Slots: 2}}}
	slots, jit := ApplyCapacityOverrides(raw, []string{"lmstudio", "ollama", "llamacpp"}, loaded, reported)
	want := map[string]map[string]SlotInfo{"lmstudio": {"qwen3.8-27b-mlx": {Slots: 4, CtxPerSlot: 32768}}, "ollama": {"q": {Slots: 2}}}
	if !reflect.DeepEqual(slots, want) {
		t.Errorf("slots = %v, want %v", slots, want)
	}
	if !reflect.DeepEqual(jit, map[string]bool{"lmstudio": true, "ollama": true, "llamacpp": true}) {
		t.Errorf("jit = %v", jit)
	}
	if reported["ollama"]["q"].Slots != 2 {
		t.Error("input mutated")
	}
	if _, jit := ApplyCapacityOverrides(nil, []string{"lmstudio"}, nil, nil); jit["lmstudio"] {
		t.Error("lmstudio default must be false")
	}
}
