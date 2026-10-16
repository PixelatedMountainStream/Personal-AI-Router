// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package schedulerwire defines the shared JSON-RPC payloads used to carry
// scheduler rankings through the broker to the inference proxies.
package schedulerwire

import (
	"maps"
	"slices"
)

const (
	MethodTelemetry = "scheduler:telemetry"
	MaxGPUPressure  = 3
)

// NodeRank is one node's position in a node-wide workload and GPU-pressure
// ranking.
type NodeRank struct {
	ID          string `json:"id"`
	Pending     int    `json:"pending"`
	GPUPressure int    `json:"gpuPressure"`
	Rank        int    `json:"rank"`
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

// Priority is the payload accepted by a proxy's node/set-priority method.
// Ranks is optional so a newer broker can still drive an older nodes-only
// producer or consumer during a rolling upgrade.
type Priority struct {
	// Generation makes applying a snapshot idempotent, which matters because
	// applying one also clears the proxy's optimistic reservations.
	//
	// A node/set-priority call can time out after the proxy already applied it,
	// so the broker cannot distinguish "never arrived" from "already done" and
	// may redeliver. Without a version the redelivery would clear reservations
	// taken since the first apply — discarding exactly the dispatches the
	// scheduler has not yet seen. The proxy records the last generation it
	// applied and treats an equal one as an acknowledged no-op and an older one
	// as stale.
	//
	// Required, and greater than zero. A producer must mint a monotonic
	// generation per delivery; the proxy rejects zero at the RPC boundary and
	// ignores it thereafter.
	//
	// Zero used to mean "apply unconditionally", which reintroduced the very
	// undercount this field prevents: it cleared the reservations while
	// leaving the applied epoch untouched, so a reservation taken before the
	// snapshot still matched and released one taken after it.
	Generation uint64     `json:"generation,omitempty"`
	Nodes      []string   `json:"nodes"`
	Ranks      []NodeRank `json:"ranks,omitempty"`
}

// Clone returns an independently owned snapshot safe to cache across goroutines.
func (p Priority) Clone() Priority {
	return Priority{
		Generation: p.Generation,
		Nodes:      append([]string(nil), p.Nodes...),
		Ranks:      CloneRanks(p.Ranks),
	}
}

// SameRanking reports whether two snapshots carry the same node-wide ranking,
// ignoring Generation. The broker uses it to drop the scheduler's duplicate
// per-engine emission of one recompute before it mints a generation.
//
// Not reflect.DeepEqual: Snapshot and Clone normalize an empty slice to nil, so
// the two agree on every reachable value here, but DeepEqual would separate
// nil from empty if a producer ever sent one.
func (p Priority) SameRanking(other Priority) bool {
	return slices.Equal(p.Nodes, other.Nodes) && slices.EqualFunc(p.Ranks, other.Ranks, NodeRank.Equal)
}

// EnginePriority is the schedule:priority notification emitted by the scheduler.
type EnginePriority struct {
	Engine string     `json:"engine"`
	Nodes  []string   `json:"nodes"`
	Ranks  []NodeRank `json:"ranks,omitempty"`
}

// Snapshot strips the engine routing key and returns an independently owned
// node/set-priority payload for the matching proxy.
func (p EnginePriority) Snapshot() Priority {
	return Priority{
		Nodes: append([]string(nil), p.Nodes...),
		Ranks: CloneRanks(p.Ranks),
	}
}
