// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalLoadedOnlyEngineNeverJITAndSlotsOnServedName: NVIDIA owns vLLM's
// model switch (an operator action that restarts it), so a loaded-only engine
// reports jit_load false even when a setting asks for true, and a slot override
// applies to the served name (--served-model-name, what a request names), not
// to the --model root its loadedByEngine entry uses.
func TestLocalLoadedOnlyEngineNeverJITAndSlotsOnServedName(t *testing.T) {
	m := loadedOnlyManifest(t, fakeEngineBin)
	key := runtime.GOOS + "/" + runtime.GOARCH
	plat := m.Platforms[key]
	plat.Runtime.Env = map[string]string{"OLLAMA_HOST": "127.0.0.1:{port}", "FAKE_SERVED_MODEL_NAME": "qwen-api"}
	m.Platforms[key] = plat
	ex := newTestExecutor(t, m)
	ex.settingsOverrides = func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"jit_load.fake": json.RawMessage(`true`),
			"engine_slots.fake.qwen-api": json.RawMessage(`4`), "engine_slots.fake.llama3.2:1b": json.RawMessage(`5`)}
	}
	ctx := t.Context()

	res := ex.ModelsResult(ctx) // stopped: reported (nothing loaded), never JIT
	assert.Equal(t, map[string]bool{"fake": false}, res.JITLoadByEngine, "stopped")
	assert.Nil(t, res.SlotsByEngine, "stopped")

	t.Cleanup(func() { _ = ex.Stop("fake") })
	require.NoError(t, ex.Start(ctx, "fake"))
	res = ex.ModelsResult(ctx)
	require.Equal(t, map[string][]string{"fake": {"llama3.2:1b"}}, res.LoadedByEngine, "NVIDIA loaded marker uses the root")
	assert.Equal(t, map[string]bool{"fake": false}, res.JITLoadByEngine, "running")
	assert.Equal(t, map[string]map[string]SlotInfo{"fake": {"qwen-api": {Slots: 4}}}, res.SlotsByEngine)
}
