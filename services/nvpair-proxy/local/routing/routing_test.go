// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package routing

import "testing"

// NVIDIA's cooldown is the outer partition: a cooling node with a free slot
// still ranks behind a full, non-cooling one; within a partition the
// slot-aware keys decide.
func TestBetterCoolingIsOuterPartition(t *testing.T) {
	coolFree := Candidate{ID: "cool", Order: 0, Slots: 4, Cooling: true}
	warmFull := Candidate{ID: "warm", Order: 1, Slots: 1, Occ: 3}
	if !Better(warmFull, coolFree) || Better(coolFree, warmFull) {
		t.Fatal("cooling node outranked a non-cooling one")
	}
	warmFree := Candidate{ID: "free", Order: 2, Slots: 1}
	if got := Best([]Candidate{coolFree, warmFull, warmFree}); got != 2 {
		t.Fatalf("best = %d, want the free non-cooling node", got)
	}
	if got := Best([]Candidate{coolFree, {ID: "cool2", Order: 1, Slots: 1, Occ: 1, Cooling: true}}); got != 0 {
		t.Fatalf("all cooling: best = %d, want the free one", got)
	}
	if Best(nil) != -1 {
		t.Fatal("empty")
	}
}

func TestBetterTieBreaks(t *testing.T) {
	a := Candidate{ID: "a", Order: 0, Slots: 1}
	local := Candidate{ID: "l", Order: 1, Slots: 1, Local: true}
	if !Better(local, a) {
		t.Fatal("local should win an idle tie ahead of scheduler order")
	}
	if !Better(Candidate{Order: 1, Slots: 1, GPU: 0}, Candidate{Order: 0, Slots: 1, GPU: 1, Local: true}) {
		t.Fatal("lower GPU pressure should beat locality")
	}
}
