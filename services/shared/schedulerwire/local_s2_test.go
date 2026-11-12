// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package schedulerwire

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestS2_Nil: a zero LocalRankExt adds no keys, and ranks without it compare
// and clone exactly as upstream's comparable NodeRank did.
func TestS2_Nil(t *testing.T) {
	r := NodeRank{ID: "a", Pending: 1, GPUPressure: 2, Rank: 0}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"pendingBy", "computedAt"} {
		if strings.Contains(string(b), k) {
			t.Errorf("zero ext leaked %s: %s", k, b)
		}
	}
	p := Priority{Nodes: []string{"a"}, Ranks: []NodeRank{r}}
	if !p.SameRanking(p.Clone()) || CloneRanks(nil) != nil {
		t.Fatal("zero-ext clone must equal the original; nil stays nil")
	}
	q := p.Clone()
	q.Ranks[0].Pending = 2
	if p.SameRanking(q) {
		t.Fatal("an upstream field change must differ")
	}
}
