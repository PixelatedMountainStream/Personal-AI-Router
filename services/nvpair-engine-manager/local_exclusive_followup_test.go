// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestColdTargetSizing(t *testing.T) {
	ex := newTestExecutor(t, testEngineManifest(fakeEngineBin))
	ex.largeModelBytes = 12 * gib
	snap := []residentModel{{Engine: "lmstudio", Model: "big-27b", Bytes: 16 * gib}}
	req := PinnedRequest{Engine: "lmstudio", Model: "model-14b"}
	// Unverified cold target: the 14b name token is no evidence of a small size.
	if v, _ := ex.victimsOfSized(snap, req, 0); len(v) != 1 {
		t.Fatalf("unverified cold target must be large, victims %v", v)
	}
	// Real size above the threshold: large despite the name.
	if v, _ := ex.victimsOfSized(snap, req, 15*gib); len(v) != 1 {
		t.Fatalf("15 GiB target must evict, victims %v", v)
	}
	// Verified small: coexists.
	if v, _ := ex.victimsOfSized(snap, req, 4*gib); len(v) != 0 {
		t.Fatalf("verified small target victims %v", v)
	}
}

func TestOllamaImplicitTag(t *testing.T) {
	ex := newTestExecutor(t, testEngineManifest(fakeEngineBin))
	ex.largeModelBytes = 12 * gib
	snap := []residentModel{{Engine: "ollama", Model: "model-27b:latest", Bytes: 16 * gib}}
	v, same := ex.victimsOfSized(snap, PinnedRequest{Engine: "ollama", Model: "model-27b"}, 0)
	if !same || len(v) != 0 {
		t.Fatalf("model-27b must match resident :latest: victims %v same=%v", v, same)
	}
	if !sameModel("ollama", "org/m", "org/m:latest") || sameModel("lmstudio", "m", "m:latest") {
		t.Fatal("normalisation must be Ollama-only")
	}
}
