// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package routing

import "testing"

// Scheduler GPU pressure occupies slots: an idle but GPU-loaded node must not
// outrank a lightly loaded one (NVIDIA pending+pressure balance).
func TestBetterCountsSchedulerPressure(t *testing.T) {
	a := Candidate{ID: "a", Order: 0, Slots: 1, Pressure: 3, GPU: 3}
	b := Candidate{ID: "b", Order: 1, Slots: 1, Occ: 1}
	c := Candidate{ID: "c", Order: 2, Slots: 1, Pressure: 2, GPU: 2}
	if got := Best([]Candidate{a, b, c}); got != 1 {
		t.Fatalf("Best = %d, want 1 (b: total load 1 < 2 < 3)", got)
	}
}
