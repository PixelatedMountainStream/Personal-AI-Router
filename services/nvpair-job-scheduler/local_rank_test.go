// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmit_IncludesPendingRanksLocalExt is the fork twin of
// TestEmit_IncludesPendingRanks: it also checks the per-model PendingBy
// breakdown and that a non-idle node carries a ComputedAt stamp.
func TestEmit_IncludesPendingRanksLocalExt(t *testing.T) {
	rec := &capRW{}
	m := mgrWith(rec, []string{"a", "b"},
		workload{ID: "b1", Engine: "ollama", Model: "m", RunID: "run", State: "running", OriginatedFrom: "local", ScheduledOn: "b"},
	)

	m.recomputeAll(false)
	got := rec.priorities(t, "ollama")
	require.Len(t, got, 1, "priority snapshots")
	want := []NodeRank{
		{ID: "a", Pending: 0, GPUPressure: unknownGPUPressure, Rank: 0},
		{ID: "b", Pending: 1, GPUPressure: unknownGPUPressure, Rank: 1, PendingBy: map[string]int{"ollama/m": 1}},
	}
	require.Len(t, got[0].Ranks, len(want), "ranks")
	for i := range want {
		assert.True(t, got[0].Ranks[i].Equal(want[i]), "rank[%d] = %+v, want %+v", i, got[0].Ranks[i], want[i])
		if i == 1 {
			assert.NotZero(t, got[0].Ranks[i].ComputedAt, "rank[1] ComputedAt")
		}
	}
}
