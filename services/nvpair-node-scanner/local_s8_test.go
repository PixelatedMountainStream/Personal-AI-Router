// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestS8_Nil: S8 is a field carry on NVIDIA's modelInventory, not a signature
// change. Upstream-shaped /v1/models JSON fetched by a daemon without fork
// state yields a zero modelInventoryExt (observedAt lives in the extension and
// is stamped only by a non-nil localScanState at fetch time), and refresh then
// reports changed exactly as upstream: first populate, not an identical sweep,
// a downloadedByEngine-only change.
func TestS8_Nil(t *testing.T) {
	d := newRefreshTestDaemon()
	body := map[string]any{"models": []string{"a"}, "modelsByEngine": map[string][]string{"vllm": {"a"}},
		"loadedByEngine": map[string][]string{"vllm": {"a"}}, "downloadedByEngine": map[string][]string{"vllm": {"a"}}}
	stub, port := startModelsStub(t, http.StatusOK, body)
	inv, ok := d.fetchModels("127.0.0.1", port, "")
	require.True(t, ok)
	assert.Zero(t, inv.local, "upstream-shaped answer must give a zero extension")
	seedEMNode(d, "p", "127.0.0.1", port)
	assert.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""), "first populate")
	assert.False(t, d.refreshNodeModels("p", "127.0.0.1", port, ""), "identical sweep")
	body["downloadedByEngine"] = map[string][]string{"vllm": {"a", "b"}}
	stub.set(http.StatusOK, body)
	assert.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""), "downloadedByEngine-only change")
	got, _ := d.dir.get("p")
	assert.Equal(t, []string{"a", "b"}, got.DownloadedByEngine["vllm"])
	assert.Zero(t, got.LocalNodeExt)
}

// TestLocalDownloadedChangeKeepsCapacity: with fork state, a
// downloadedByEngine-only change stores the fetch's slots, jit_load and a fresh
// observed-at, and a capacity-only change keeps upstream's downloaded set.
func TestLocalDownloadedChangeKeepsCapacity(t *testing.T) {
	d := newLocalTestDaemon()
	body := slotBody(2, false)
	stub, port := startModelsStub(t, http.StatusOK, body)
	seedEMNode(d, "p", "127.0.0.1", port)
	require.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""))
	body["downloadedByEngine"] = map[string][]string{"lmstudio": {"a", "z"}}
	stub.set(http.StatusOK, body)
	require.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""), "downloaded-only change")
	got, _ := d.dir.get("p")
	assert.Equal(t, []string{"a", "z"}, got.DownloadedByEngine["lmstudio"])
	assert.Equal(t, 2, got.SlotsByEngine["lmstudio"]["a"].Slots)
	assert.NotZero(t, got.ModelsObservedAt)
	stub.set(http.StatusOK, mergeBody(slotBody(5, false), "downloadedByEngine", body["downloadedByEngine"]))
	require.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""), "capacity-only change")
	got, _ = d.dir.get("p")
	assert.Equal(t, 5, got.SlotsByEngine["lmstudio"]["a"].Slots)
	assert.Equal(t, []string{"a", "z"}, got.DownloadedByEngine["lmstudio"])
}

func mergeBody(b map[string]any, k string, v any) map[string]any {
	b[k] = v
	return b
}
