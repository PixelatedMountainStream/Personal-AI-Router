// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"testing"
)

// TestApplyCapacityOverridesLoadedOnlyNeverJIT: a loaded-only engine (vLLM)
// keeps jit_load false even when a setting asks for it.
func TestApplyCapacityOverridesLoadedOnlyNeverJIT(t *testing.T) {
	raw := map[string]json.RawMessage{"jit_load.vllm": json.RawMessage(`true`), "jit_load.lmstudio": json.RawMessage(`true`)}
	_, jit := ApplyCapacityOverrides(raw, []string{"vllm", "lmstudio"}, nil, nil, map[string]bool{"vllm": true})
	if jit["vllm"] || !jit["lmstudio"] {
		t.Fatalf("jit = %v", jit)
	}
}
