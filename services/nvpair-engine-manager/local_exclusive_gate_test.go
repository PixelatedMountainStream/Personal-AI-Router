// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A load that restarts the engine (vLLM launch_model) runs under loadMu; the
// startup gate must not fail on that same lock.
func TestGuardedLoadAction_RestartKeepsGateWithoutRelock(t *testing.T) {
	e := switchModelExecutor(t, "--model")
	require.NoError(t, e.Stop("fake"))
	_, err := e.exclusiveRun(t.Context(), PinnedRequest{Engine: "fake", Model: "org/next"}, func(ctx context.Context) error {
		_, err := e.Action(ctx, "fake", "load_model", json.RawMessage(`{"model":"org/next"}`))
		return err
	})
	require.NoError(t, err)
	require.Contains(t, launchTextOf(t, e), "org/next")
	require.True(t, e.loadMu.TryLock(), "gate released afterwards")
	e.loadMu.Unlock()
}
