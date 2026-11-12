// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"time"

	"nvpair-shared/schedulerwire"
)

// localAnnotateRanks fills the fork's rank extension (seam S3): each node's
// pending work split by engine/model (PendingBy, from the same pending, placed
// workloads upstream counts) and the compute time. Workloads without a model
// add no PendingBy entry, so a catalog of upstream-shaped workloads leaves
// PendingBy nil. The caller holds m.mu.
func (m *Manager) localAnnotateRanks(ranks []NodeRank, now time.Time) {
	pendingBy := make(map[string]map[string]int)
	for _, w := range m.catalog {
		if !isPending(w) || w.ScheduledOn == "" || w.Model == "" {
			continue
		}
		if _, ok := m.nodes[w.ScheduledOn]; !ok {
			continue
		}
		if pendingBy[w.ScheduledOn] == nil {
			pendingBy[w.ScheduledOn] = make(map[string]int)
		}
		pendingBy[w.ScheduledOn][schedulerwire.PendingKey(w.Engine, w.Model)]++
	}
	for i := range ranks {
		ranks[i].PendingBy = pendingBy[ranks[i].ID]
		ranks[i].ComputedAt = now.UnixMilli()
	}
}

// localRankUnchanged replaces upstream's slices.Equal over comparable ranks:
// NodeRank.Equal ignores ComputedAt (new on every compute) and compares
// PendingBy by content.
func localRankUnchanged(prev, next []NodeRank) bool {
	return slices.EqualFunc(prev, next, NodeRank.Equal)
}
