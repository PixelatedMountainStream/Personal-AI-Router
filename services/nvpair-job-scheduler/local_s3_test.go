// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestS3_Nil: upstream-shaped workloads (no model) leave PendingBy nil, and
// localRankUnchanged decides on the upstream fields exactly as upstream's
// slices.Equal did.
func TestS3_Nil(t *testing.T) {
	m := mgrWith(nopRW{}, []string{"a", "b"},
		workload{ID: "w", Engine: "ollama", RunID: "run", State: "running", OriginatedFrom: "local", ScheduledOn: "a"},
	)
	_, ranks := m.rankAt(time.UnixMilli(1000))
	for _, r := range ranks {
		assert.Nil(t, r.PendingBy, "PendingBy without models for %s", r.ID)
	}
	plain := []NodeRank{{ID: "a", Pending: 1}, {ID: "b"}}
	other := slices.Clone(plain)
	assert.True(t, localRankUnchanged(plain, other), "equal upstream fields are unchanged")
	other[1].GPUPressure = 3
	assert.False(t, localRankUnchanged(plain, other), "an upstream field change is a change")
	assert.False(t, localRankUnchanged(plain, plain[:1]), "a length change is a change")
}
