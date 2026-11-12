// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalRefreshCarriesSlotsJITAndObservedAt (step-4 characterization): a
// refresh publishes the fetch's slots, jit_load and a fresh ModelsObservedAt on
// the directory node, and the change event carries them.
func TestLocalRefreshCarriesSlotsJITAndObservedAt(t *testing.T) {
	d := newLocalTestDaemon()
	_, port := startModelsStub(t, http.StatusOK, slotBody(3, true))
	seedEMNode(d, "p", "127.0.0.1", port)
	before := time.Now().UnixMilli()
	require.True(t, d.refreshNodeModels("p", "127.0.0.1", port, ""))
	got, _ := d.dir.get("p")
	assert.Equal(t, 3, got.SlotsByEngine["lmstudio"]["a"].Slots)
	assert.True(t, got.JITLoadByEngine["lmstudio"])
	assert.True(t, got.ModelsObservedAt >= before && got.ModelsObservedAt <= time.Now().UnixMilli(),
		"observedAt %d", got.ModelsObservedAt)
}
