// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"slices"
	"testing"

	"nvpair-shared/noderec"
)

// vLLM advertises its available inventory (also while stopped), so only the
// loaded root, translated to its served alias, counts as resident.
func TestServedLoaded_AvailableVLLMOnlyLoaded(t *testing.T) {
	n := noderec.DirectoryNode{
		ModelsByEngine:     map[string][]string{"vllm": {"alias"}},
		DownloadedByEngine: map[string][]string{"vllm": {"org/root", "org/cold"}},
	}
	if got := ServedLoaded(n, "vllm", []string{"org/root"}); !slices.Equal(got, []string{"alias"}) {
		t.Fatalf("loaded root = %v, want [alias]", got)
	}
	n.ModelsByEngine["vllm"] = []string{"org/root", "org/cold"}
	if got := ServedLoaded(n, "vllm", []string{"org/root"}); !slices.Equal(got, []string{"org/root"}) {
		t.Fatalf("cold model leaked into loaded set: %v", got)
	}
	if got := ServedLoaded(n, "vllm", []string{}); len(got) != 0 {
		t.Fatalf("stopped engine loaded = %v, want empty", got)
	}
}

// A served name equal to another downloaded id (root org/root served as
// org/cold) must come from the relayed served names, not alias inference.
func TestServedLoaded_UsesRelayedServedNames(t *testing.T) {
	n := noderec.DirectoryNode{
		ModelsByEngine:     map[string][]string{"vllm": {"org/cold"}},
		DownloadedByEngine: map[string][]string{"vllm": {"org/root", "org/cold"}},
	}
	n.ServedLoadedByEngine = map[string][]string{"vllm": {"org/cold"}}
	if got := ServedLoaded(n, "vllm", []string{"org/root"}); !slices.Equal(got, []string{"org/cold"}) {
		t.Fatalf("served = %v, want [org/cold]", got)
	}
}
