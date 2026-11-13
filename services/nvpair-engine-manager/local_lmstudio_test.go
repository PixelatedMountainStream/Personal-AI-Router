// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLocalLMStudioModelsThroughBundledManifest (step-4 characterization, LM
// Studio parity): the bundled lmstudio manifest reports the inventory by key,
// each loaded instance as its own loaded model with its own slots, jit_load
// false by default, and node-settings overrides keyed on the instance id.
func TestLocalLMStudioModelsThroughBundledManifest(t *testing.T) {
	inst := func(id string, par int) string {
		return fmt.Sprintf(`{"id":%q,"config":{"parallel":%d,"context_length":8192}}`, id, par)
	}
	body := `{"models":[{"key":"a","size_bytes":10,"loaded_instances":[` + inst("a", 4) + `,` + inst("a:2", 2) +
		`]},{"key":"b","size_bytes":20,"loaded_instances":[]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	reg := NewRegistry()
	reg.engines["lmstudio"] = bundledManifestSet(t)["lmstudio"]
	ex := NewExecutor(reg, NewReporter(nil), func(string, any) {}, t.TempDir())
	st, err := ex.state("lmstudio")
	if err != nil {
		t.Skipf("no host platform block for lmstudio: %v", err)
	}
	u, _ := url.Parse(srv.URL)
	_, _ = fmt.Sscanf(u.Port(), "%d", &st.port)
	st.running, st.installed = true, true

	res := ex.ModelsResult(context.Background())
	assert.Equal(t, []string{"a", "b"}, res.ByEngine["lmstudio"])
	assert.Equal(t, []string{"a", "a:2"}, res.LoadedByEngine["lmstudio"])
	assert.Equal(t, map[string]SlotInfo{"a": {Slots: 4, CtxPerSlot: 8192}, "a:2": {Slots: 2, CtxPerSlot: 8192}},
		res.SlotsByEngine["lmstudio"])
	assert.Equal(t, map[string]bool{"lmstudio": false}, res.JITLoadByEngine)

	ex.settingsOverrides = func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"jit_load.lmstudio": json.RawMessage(`true`),
			"engine_slots.lmstudio.a:2": json.RawMessage(`6`), "engine_slots.lmstudio.b": json.RawMessage(`3`)}
	}
	res = ex.ModelsResult(context.Background())
	assert.True(t, res.JITLoadByEngine["lmstudio"], "jit override")
	assert.Equal(t, 6, res.SlotsByEngine["lmstudio"]["a:2"].Slots, "instance override")
	assert.NotContains(t, res.SlotsByEngine["lmstudio"], "b", "override on a model not loaded")
}
