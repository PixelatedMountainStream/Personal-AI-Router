// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package routing is the fork's slot-aware candidate ranking for nvpair-proxy.
// It knows nothing about the proxy's own types: package main glue
// (local_proxy.go) converts its candidates to Candidate values and back.
package routing

// Candidate is one scheduler-listed node as the slot-aware ranking sees it.
type Candidate struct {
	ID    string
	Order int // position in the scheduler's priority list
	Slots int // concurrent requests the engine serves for the model; >= 1
	Occ   int // requests occupying those slots now
	// GPU is the scheduler's GPU pressure plus this proxy's reservations
	// through any engine's facade (they share the GPU, not the slots).
	GPU   int
	Local bool // this host's own node
	// Cooling is NVIDIA's #144 first-byte cooldown (coolingSet).
	Cooling bool
}

// Full reports whether every slot is occupied.
func (c Candidate) Full() bool { return c.Occ >= c.Slots }

// queue is occupancy per slot, or, when full, the queue a new request would
// join: (occ-slots+1)/slots.
func (c Candidate) queue() float64 {
	slots, occ := float64(c.Slots), float64(c.Occ)
	if occ >= slots {
		return (occ - slots + 1) / slots
	}
	return occ / slots
}

// Better reports whether a ranks ahead of b. Keys, outermost first:
//
//  1. not cooling: NVIDIA's cooldown demotion is the outer stable partition,
//     so a cooling node never outranks a non-cooling one, whatever its slots
//     (the same rule NVIDIA's reserveCandidate applies to load);
//  2. a free slot;
//  3. lower queue per slot;
//  4. lower GPU pressure;
//  5. the local node;
//  6. scheduler order.
func Better(a, b Candidate) bool {
	if a.Cooling != b.Cooling {
		return !a.Cooling
	}
	if af, bf := a.Full(), b.Full(); af != bf {
		return !af
	}
	if aq, bq := a.queue(), b.queue(); aq != bq {
		return aq < bq
	}
	if a.GPU != b.GPU {
		return a.GPU < b.GPU
	}
	if a.Local != b.Local {
		return a.Local
	}
	return a.Order < b.Order
}

// Best returns the index of the best candidate, or -1 when cs is empty.
func Best(cs []Candidate) int {
	best := -1
	for i, c := range cs {
		if best < 0 || Better(c, cs[best]) {
			best = i
		}
	}
	return best
}
