// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLocalNodeExtJSONKeys: the fork's capacity fields keep their wire keys on
// DirectoryNode (a same-named upstream field would silently drop them).
func TestLocalNodeExtJSONKeys(t *testing.T) {
	var n DirectoryNode
	n.SlotsByEngine = map[string]map[string]SlotInfo{"e": {"m": {Slots: 1}}}
	n.JITLoadByEngine = map[string]bool{"e": true}
	n.ModelsObservedAt = 7
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"slotsByEngine":`, `"jitLoadByEngine":`, `"modelsObservedAt":7`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("missing %s in %s", k, b)
		}
	}
}
