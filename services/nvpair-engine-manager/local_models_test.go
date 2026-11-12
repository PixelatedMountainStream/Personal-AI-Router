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
	"github.com/stretchr/testify/require"
)

func TestExtractRecords(t *testing.T) {
	lm := &ActionResult{Array: "models", Field: "key", LocalResultExt: LocalResultExt{Record: &ResultRecord{Fields: map[string]string{
		"slots": "loaded_instances.0.config.parallel", "ctx": "loaded_instances.0.config.context_length"}}}}
	oll := &ActionResult{Array: "models", Field: "name", LocalResultExt: LocalResultExt{Record: &ResultRecord{Static: map[string]int{"slots": 1}}}}
	llama := &ResultRecord{Fields: map[string]string{"slots": "total_slots", "ctx": "default_generation_settings.n_ctx"}}

	for _, tc := range []struct {
		name string
		raw  string
		spec *ActionResult
		want map[string]SlotInfo
	}{
		{"lmstudio nested path", `{"models":[{"key":"a","loaded_instances":[{"config":{"parallel":4,"context_length":131072}}]}]}`, lm,
			map[string]SlotInfo{"a": {Slots: 4, CtxPerSlot: 131072}}},
		{"lmstudio missing path", `{"models":[{"key":"a","loaded_instances":[]}]}`, lm, map[string]SlotInfo{}},
		{"lmstudio wrong type", `{"models":[{"key":"a","loaded_instances":[{"config":{"parallel":"4"}}]}]}`, lm, map[string]SlotInfo{}},
		{"lmstudio bad ctx type", `{"models":[{"key":"a","loaded_instances":[{"config":{"parallel":4,"context_length":"x"}}]}]}`, lm, map[string]SlotInfo{}},
		{"ollama static", `{"models":[{"name":"q"}]}`, oll, map[string]SlotInfo{"q": {Slots: 1}}},
		{"no array", `{"data":[]}`, oll, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractRecords(json.RawMessage(tc.raw), tc.spec)
			require.Equal(t, tc.want != nil, ok, "extractRecords ok")
			assert.Equal(t, tc.want, got)
		})
	}

	// llama.cpp single-object shape.
	si, ok := extractRecord(json.RawMessage(`{"total_slots":2,"default_generation_settings":{"n_ctx":8192}}`), llama)
	require.True(t, ok)
	assert.Equal(t, SlotInfo{Slots: 2, CtxPerSlot: 8192}, si)
	_, ok = extractRecord(json.RawMessage(`{"default_generation_settings":{"n_ctx":8192}}`), llama)
	assert.False(t, ok, "missing total_slots must fail")
}

// TestBundledSlotsQueryThroughManifests drives ModelsResult through the bundled
// llamacpp and ollama manifest specs against fake HTTP engines: llama.cpp
// succeeds (and its /props poll must carry autoload=false so it never reloads
// an unloaded 27B model), Ollama's /api/ps fails so its slots are omitted.
func TestBundledSlotsQueryThroughManifests(t *testing.T) {
	var propsQuery url.Values
	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m","status":{"value":"loaded"}}]}`))
		case "/props":
			propsQuery = r.URL.Query()
			_, _ = w.Write([]byte(`{"total_slots":2,"default_generation_settings":{"n_ctx":8192}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer llama.Close()
	oll := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[{"name":"q"}]}`))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer oll.Close()

	set := bundledManifestSet(t)
	reg := NewRegistry()
	reg.engines["llamacpp"] = set["llamacpp"]
	reg.engines["ollama"] = set["ollama"]
	ex := NewExecutor(reg, NewReporter(nil), func(string, any) {}, t.TempDir())
	for name, srv := range map[string]*httptest.Server{"llamacpp": llama, "ollama": oll} {
		st, err := ex.state(name)
		if err != nil {
			t.Skipf("no host platform block for %s: %v", name, err)
		}
		u, _ := url.Parse(srv.URL)
		_, _ = fmt.Sscanf(u.Port(), "%d", &st.port)
		st.running = true
	}

	res := ex.ModelsResult(context.Background())
	want := map[string]map[string]SlotInfo{"llamacpp": {"m": {Slots: 2, CtxPerSlot: 8192}}}
	assert.Equal(t, want, res.SlotsByEngine, "failing engine must be omitted")
	assert.Equal(t, "false", propsQuery.Get("autoload"), "/props query = %v", propsQuery)
	assert.Equal(t, "m", propsQuery.Get("model"), "/props query = %v", propsQuery)
}

func TestApplySettingsOverrides(t *testing.T) {
	r := ModelsResult{
		ByEngine:       map[string][]string{"lmstudio": {"m"}, "ollama": {"q"}},
		LoadedByEngine: map[string][]string{"lmstudio": {"m"}, "ollama": {}},
	}
	r.applySettingsOverrides(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"engine_slots.lmstudio.m": json.RawMessage(`3`), "engine_slots.ollama.q": json.RawMessage(`2`)}
	}, nil)
	assert.Equal(t, 3, r.SlotsByEngine["lmstudio"]["m"].Slots, "override not applied: %v", r.SlotsByEngine)
	assert.NotContains(t, r.SlotsByEngine, "ollama", "override for a non-loaded model must not apply")
	assert.False(t, r.JITLoadByEngine["lmstudio"], "jit defaults = %v", r.JITLoadByEngine)
	assert.True(t, r.JITLoadByEngine["ollama"], "jit defaults = %v", r.JITLoadByEngine)
}

// TestLocalModelsResultJSONKeys (step-3 characterization, E11): the fork's
// capacity fields keep their wire keys and drop out when empty.
func TestLocalModelsResultJSONKeys(t *testing.T) {
	r := ModelsResult{Models: []string{"a"}}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "slotsByEngine", "empty slots must be omitted")
	assert.NotContains(t, string(b), "jitLoadByEngine", "empty jit must be omitted")
	r.SlotsByEngine = map[string]map[string]SlotInfo{"lmstudio": {"a": {Slots: 2}}}
	r.JITLoadByEngine = map[string]bool{"lmstudio": true}
	b, err = json.Marshal(r)
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keys))
	assert.Contains(t, keys, "slotsByEngine")
	assert.Contains(t, keys, "jitLoadByEngine")
}
