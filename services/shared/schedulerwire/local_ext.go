// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package schedulerwire

import (
	"maps"
	"slices"
)

// LocalRankExt is the fork's extension of NodeRank (seam S2): the per-model
// pending breakdown and the compute time. It is embedded, so encoding/json
// flattens it and the wire keys are unchanged; a zero value adds no keys.
type LocalRankExt struct {
	// PendingBy splits Pending by PendingKey(engine, model). Nil from a
	// scheduler that predates it; consumers then fall back to Pending.
	PendingBy map[string]int `json:"pendingBy,omitempty"`
	// ComputedAt is when the scheduler computed this rank, in Unix ms. A
	// consumer discounts its own completions after it, since the scheduler had
	// not yet seen them.
	ComputedAt int64 `json:"computedAt,omitempty"`
}

// PendingKey is the PendingBy key for one engine's model.
func PendingKey(engine, model string) string { return engine + "/" + model }

// Equal compares everything but ComputedAt, which changes on every compute.
func (r NodeRank) Equal(o NodeRank) bool {
	return r.ID == o.ID && r.Pending == o.Pending && r.GPUPressure == o.GPUPressure &&
		r.Rank == o.Rank && maps.Equal(r.PendingBy, o.PendingBy)
}

// CloneRanks copies ranks, including each PendingBy map. A nil input stays nil.
func CloneRanks(in []NodeRank) []NodeRank {
	if in == nil {
		return nil
	}
	out := slices.Clone(in)
	for i := range out {
		out[i].PendingBy = maps.Clone(out[i].PendingBy)
	}
	return out
}
