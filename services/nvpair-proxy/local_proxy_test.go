// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"

	"nvpair-shared/schedulerwire"
)

func localTieCandidates() []candidate {
	return []candidate{{id: "a-remote"}, {id: "z-local", local: true}}
}

func localTieProxy(t *testing.T) *Proxy {
	p := prProxy(t)
	ids := []string{"a-remote", "z-local"}
	applySnapshot(p, schedulerwire.Priority{Nodes: ids, Ranks: []schedulerwire.NodeRank{{ID: "a-remote", Rank: 0}, {ID: "z-local", Rank: 1}}})
	return p
}

func TestReserveCandidate_IdleTiePrefersLocal(t *testing.T) {
	p := localTieProxy(t)
	assert.Equal(t, "z-local", reservedID(p, localTieCandidates()), "want local node")
}

func TestReserveCandidate_FullLocalYieldsToFreeRemote(t *testing.T) {
	p := localTieProxy(t)
	f := p.soleFacade()
	p.priorityMu.Lock()
	for range p.nodeSlots("z-local", f.profile.Name, "") {
		p.takeLocked("z-local", f.profile.Name, "", false)
	}
	p.priorityMu.Unlock()
	require.Equal(t, "a-remote", reservedID(p, localTieCandidates()), "want remote with free slot")
}
