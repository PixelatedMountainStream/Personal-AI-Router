// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvpair-shared/schedulerwire"
)

func localTieCandidates() []candidate {
	return []candidate{{id: "a-remote"}, {id: "z-local", local: true}}
}

func localTieProxy(t *testing.T) *Proxy {
	p := withLocal(prProxy(t))
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

// Model lookups (/api/show) only need an owner with the model installed, not a
// loaded slot; inference for the same model still requires residency.
func TestHandleHTTP_ModelLookupIgnoresResidency(t *testing.T) {
	tc := ollamaCase(t)
	a, _ := newCountingServer(t, 200, "")
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "a", a, tc.advertisedModel))
	p := withLocal(testProxy(tc.profile, disc, tc.profile.FacadePort))
	p.local.capacity = &fakeCap{res: map[string]Residency{"a": ResidencyKnownEmpty}}

	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(`{"name":"qwen3"}`)))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, tc.inferenceRequest())
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.NotEmpty(t, rec.Body.String())
}

// withLocal gives a test proxy fork state with no capacity source (one slot
// everywhere, nothing known), as NewProxy-built fork tests had before step 5.
func withLocal(p *Proxy) *Proxy {
	p.local = &localState{}
	return p
}
