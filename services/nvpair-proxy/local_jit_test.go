// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"nvpair-shared/noderec"
)

// TestLocalJITFallbackLMStudioParity (step-4 characterization): with no
// jit_load key, LM Studio never loads on demand and Ollama and llama.cpp do,
// in both the env settings and the directory capacity providers.
func TestLocalJITFallbackLMStudioParity(t *testing.T) {
	d := newDirectoryCapacity(nil)
	for _, e := range []string{"lmstudio", "ollama", "llamacpp"} {
		d.update(e, []noderec.DirectoryNode{{HostUUID: "n"}})
	}
	for e, want := range map[string]bool{"lmstudio": false, "ollama": true, "llamacpp": true} {
		if got := (settingsCapacity{}).JITLoad("n", e); got != want {
			t.Errorf("settings JITLoad(%s) = %v, want %v", e, got, want)
		}
		if got := d.JITLoad("n", e); got != want {
			t.Errorf("directory JITLoad(%s) = %v, want %v", e, got, want)
		}
	}
}

// TestLocalVLLMDownloadedOnlyNeverEligible: NVIDIA owns vLLM's model switch,
// so a model a vLLM node has only downloaded (downloadedByEngine, not served)
// is never eligible, whether the node said jit_load false or nothing; the
// served name (modelsByEngine) is eligible as Loaded although loadedByEngine
// names the --model root; a stopped vLLM serves nothing.
func TestLocalVLLMDownloadedOnlyNeverEligible(t *testing.T) {
	if (settingsCapacity{}).JITLoad("n", "vllm") {
		t.Fatal("settings fallback must not JIT vLLM")
	}
	vllm := func(id string, served, loaded []string, jit map[string]bool) noderec.DirectoryNode {
		n := noderec.DirectoryNode{HostUUID: id,
			LoadedByEngine:     map[string][]string{"vllm": loaded},
			DownloadedByEngine: map[string][]string{"vllm": {"org/m", "org/other"}},
			LocalNodeExt:       noderec.LocalNodeExt{JITLoadByEngine: jit}}
		if served != nil {
			n.ModelsByEngine = map[string][]string{"vllm": served}
		}
		return n
	}
	d := newDirectoryCapacity(nil)
	d.update("vllm", []noderec.DirectoryNode{
		vllm("quiet", []string{"qwen-api"}, []string{"org/m"}, nil),
		vllm("said", []string{"qwen-api"}, []string{"org/m"}, map[string]bool{"vllm": false}),
		vllm("stopped", nil, []string{}, map[string]bool{"vllm": false}),
	})
	p := &Proxy{suppressed: map[inflightKey]time.Time{}, now: time.Now}
	p.capacity = d
	for _, id := range []string{"quiet", "said", "stopped"} {
		for _, m := range []string{"org/other", "org/m"} {
			if p.eligible(id, "vllm", m) {
				t.Errorf("%s: downloaded-only %s must never be eligible", id, m)
			}
		}
	}
	for _, id := range []string{"quiet", "said"} {
		if r, _ := d.Residency(id, "vllm", "qwen-api"); r != ResidencyLoaded || !p.eligible(id, "vllm", "qwen-api") {
			t.Errorf("%s: served name residency %v", id, r)
		}
	}
}

// TestLocalVLLMServedAliasWithoutDownloadedInventory: a failed HF-cache read
// omits downloadedByEngine.vllm, but the served alias is still Loaded (not
// compared with the --model root) and eligible.
func TestLocalVLLMServedAliasWithoutDownloadedInventory(t *testing.T) {
	d := newDirectoryCapacity(nil)
	d.update("vllm", []noderec.DirectoryNode{{HostUUID: "n",
		ModelsByEngine: map[string][]string{"vllm": {"qwen-api"}},
		LoadedByEngine: map[string][]string{"vllm": {"org/model"}}}})
	p := &Proxy{suppressed: map[inflightKey]time.Time{}, now: time.Now}
	p.capacity = d
	if r, _ := d.Residency("n", "vllm", "qwen-api"); r != ResidencyLoaded || !p.eligible("n", "vllm", "qwen-api") {
		t.Fatalf("served alias residency %v", r)
	}
	if p.eligible("n", "vllm", "org/other") {
		t.Fatal("unserved model must not be eligible")
	}
}
