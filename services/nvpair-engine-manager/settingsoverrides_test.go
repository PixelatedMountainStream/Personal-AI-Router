// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsOverridesKeepLastValidOnFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	var c overridesCache
	if got := c.get(p); got != nil {
		t.Fatalf("absent file = %v, want none", got)
	}
	if err := os.WriteFile(p, []byte(`{"engine_overrides":{"jit_load.ollama":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := c.get(p); string(got["jit_load.ollama"]) != "false" {
		t.Fatalf("valid read = %v", got)
	}
	if err := os.WriteFile(p, []byte(`{"engine_over`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := c.get(p); string(got["jit_load.ollama"]) != "false" {
		t.Fatalf("malformed read must keep last valid, got %v", got)
	}
	_ = os.Remove(p)
	if got := c.get(p); got != nil {
		t.Fatalf("absent file after valid = %v, want none", got)
	}
}
