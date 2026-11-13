// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"testing"
)

// TestLocalDirectoryNodeRoundTrip (step-4 characterization, shared contract):
// the fork's fields survive a JSON round trip of a DirectoryNode next to the
// upstream model fields.
func TestLocalDirectoryNodeRoundTrip(t *testing.T) {
	in := DirectoryNode{HostUUID: "h", LoadedByEngine: map[string][]string{"lmstudio": {"a"}},
		LocalNodeExt: LocalNodeExt{SlotsByEngine: map[string]map[string]SlotInfo{"lmstudio": {"a": {Slots: 2, CtxPerSlot: 4096}}},
			JITLoadByEngine: map[string]bool{"lmstudio": false}, ModelsObservedAt: 7}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out DirectoryNode
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.SlotsByEngine["lmstudio"]["a"] != (SlotInfo{Slots: 2, CtxPerSlot: 4096}) || out.JITLoadByEngine["lmstudio"] ||
		out.ModelsObservedAt != 7 || out.LoadedByEngine["lmstudio"][0] != "a" {
		t.Fatalf("round trip lost fields: %s -> %+v", b, out)
	}
	if _, ok := out.JITLoadByEngine["lmstudio"]; !ok {
		t.Fatal("explicit jit_load false dropped")
	}
}
