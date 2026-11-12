// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

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
