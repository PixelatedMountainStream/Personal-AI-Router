// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// Nil-state tests for hooks P1-P9: with no fork state proxy.go runs NVIDIA's
// code path unchanged. Plus the step-5 cooling-vs-slot composition.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nvpair-shared/schedulerwire"
)

func TestP1_Nil(t *testing.T) {
	assert.Nil(t, NewProxy(NewCodec(rwNop{})).local, "NewProxy must leave fork state off")
	s := newLocalState(nil)
	require.NotNil(t, s.directory)
	assert.NotNil(t, s.capacity)
}

func TestP2_Nil(t *testing.T) {
	var s *localState
	s.observeNodes("ollama", nil) // must not panic
}

func TestP3_Nil(t *testing.T) {
	p := prProxy(t, "a")
	assert.Nil(t, p.local)
	assert.Equal(t, 1, p.nodeSlots("a", "ollama", "m"))
	assert.True(t, p.eligible("a", "ollama", "m"))
}

func TestP4_Nil(t *testing.T) {
	var s *localState
	body := []byte(`{"input":[{"type":"message","role":"user","content":"hi"},{"type":"message","role":"developer","content":"x"}]}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	assert.Equal(t, string(body), string(s.rewriteBody(r, body)))
}

func TestP5_Nil(t *testing.T) {
	var s *localState
	in := []candidate{{id: "a"}, {id: "b"}}
	out, n := s.filterCandidates(nil, "m", in, true)
	assert.Equal(t, in, out)
	assert.Zero(t, n)
	b, e, st := localNotLoadedRejection(0, false, "m", "body", "err", http.StatusBadGateway)
	assert.Equal(t, []any{"body", "err", http.StatusBadGateway}, []any{b, e, st})
}

func TestP6_Nil(t *testing.T) {
	p := prProxy(t, "a", "b")
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a", "b"}, Ranks: []schedulerwire.NodeRank{{ID: "a"}, {ID: "b", Rank: 1}}})
	lr := p.local.request(p, p.soleFacade(), httptest.NewRequest(http.MethodPost, "/", nil), nil, "m", true)
	require.Nil(t, lr)
	out, held := p.localReserve(lr, p.soleFacade(), reservationCandidates("a", "b"))
	assert.Equal(t, "a", out[0].id)
	assert.Equal(t, localClaim{}, held.localClaim, "upstream reservation carries no fork claim")
	assert.Empty(t, lr.affinityDecision())
	var mu sync.Mutex
	lr.releaseEarly(&mu, &held)
	assert.True(t, held.held, "releaseEarly is a no-op without fork state")
	p.priorityMu.Lock()
	assert.False(t, p.localDropLocked(held))
	_, moved := p.localMoveLocked(held, "b")
	p.priorityMu.Unlock()
	assert.False(t, moved)
	p.local.applySnapshot(schedulerwire.Priority{})
	p.releaseReservation(held)
}

func TestP7_Nil(t *testing.T) {
	var lr *localReq
	var mu sync.Mutex
	var held reservation
	c, skip := lr.pick([]candidate{{id: "a"}, {id: "b"}}, 1, candidate{id: "b"}, &mu, &held)
	assert.Equal(t, "b", c.id)
	assert.False(t, skip)
	lr.onMove("a", held)
	lr.resetTried()
	lr.attemptStart("1", 1, "a", 2, &mu, &held)
	lr.attemptEnd("1", 1, "a", time.Now(), false)
}

func TestP8_Nil(t *testing.T) {
	var s *localState
	raw := json.RawMessage(`{"id":"m","context_length":4096}`)
	assert.Equal(t, string(raw), string(s.annotateModel(nil, true, nil, "m", raw)))
}

func TestP9_Nil(t *testing.T) {
	var lr *localReq
	resp := &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}
	assert.False(t, lr.notLoaded(t.Context(), resp, "a", false, time.Now()))
}

// The fork's slot-aware reservation keeps NVIDIA's #144 cooldown as the outer
// partition: a cooling node with free slots loses to a full non-cooling one.
func TestLocalCoolingIsOuterPartitionOverSlots(t *testing.T) {
	tc := lmstudioCase(t)
	p, f := charRanked(t, tc, "a", "b")
	charLocal(p, &fakeCap{slots: map[string]int{"a": 4, "b": 1}})
	cands := []candidate{{id: "a"}, {id: "b"}}
	require.Equal(t, "a", charReserve(p, f, cands, tc.requestedModel), "warm: a has the free slots")
	p.noteNoFirstByte("a", time.Now())
	assert.Equal(t, "b", charReserve(p, f, cands, tc.requestedModel), "a cooling: b first while free")
	assert.Equal(t, "b", charReserve(p, f, cands, tc.requestedModel), "a cooling: b queued ahead of a's free slots")
	p.noteFirstByte("a")
	assert.Equal(t, "a", charReserve(p, f, cands, tc.requestedModel), "cooldown cleared: slots decide again")
}

// When every candidate is cooling, demotion is not exclusion: slots decide.
func TestLocalAllCoolingFallsBackToSlots(t *testing.T) {
	tc := lmstudioCase(t)
	p, f := charRanked(t, tc, "a", "b")
	charLocal(p, &fakeCap{slots: map[string]int{"a": 1, "b": 2}})
	p.noteNoFirstByte("a", time.Now())
	p.noteNoFirstByte("b", time.Now())
	cands := []candidate{{id: "a"}, {id: "b"}}
	got := []string{charReserve(p, f, cands, tc.requestedModel), charReserve(p, f, cands, tc.requestedModel)}
	assert.Equal(t, []string{"a", "b"}, got)
}

// An affinity-bound node that starts cooling is left for a warm one (rebind).
func TestLocalAffinityLeavesCoolingBoundNode(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	first, _ := turn(p, "conv", "a", "b")
	p.noteNoFirstByte(first, time.Now())
	got, d := turn(p, "conv", "a", "b")
	assert.NotEqual(t, first, got)
	assert.Equal(t, "rebind", d)
}

// Inference through the handler: the residency filter runs after NVIDIA's
// cooling demotion and keeps its order.
func TestLocalFilterKeepsCoolingOrder(t *testing.T) {
	tc := lmstudioCase(t)
	disc := NewDiscovery()
	for _, id := range []string{"a", "b", "c"} {
		srv, _ := newCountingServer(t, 200, "{}")
		disc.AddManual(nodeForModel(t, id, srv, tc.advertisedModel))
	}
	p := charLocal(testProxy(tc.profile, disc, tc.profile.FacadePort), &fakeCap{res: map[string]Residency{"b": ResidencyKnownEmpty}})
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a", "b", "c"}, Ranks: []schedulerwire.NodeRank{{ID: "a"}, {ID: "b", Rank: 1}, {ID: "c", Rank: 2}}})
	p.noteNoFirstByte("a", time.Now())
	cands, n := p.soleFacade().resolveCandidatesN(tc.advertisedModel)
	ids := []string{}
	for _, c := range cands {
		ids = append(ids, c.id)
	}
	assert.Equal(t, []string{"c", "a"}, ids)
	assert.Equal(t, 1, n)
	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, httptest.NewRequest(http.MethodPost, tc.inferencePath, strings.NewReader(tc.inferenceBody())))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// Codex P1: two IDs on one host advertise the model; the first is known empty
// with JIT off, the second has it loaded. The loaded one must be served.
func TestLocalSameHostLoadedIDSurvivesDedup(t *testing.T) {
	tc := lmstudioCase(t)
	srv, hits := newCountingServer(t, 200, "{}")
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "a", srv, tc.advertisedModel))
	disc.AddManual(nodeForModel(t, "b", srv, tc.advertisedModel))
	p := charLocal(testProxy(tc.profile, disc, tc.profile.FacadePort),
		&fakeCap{res: map[string]Residency{"a": ResidencyKnownEmpty, "b": ResidencyLoaded}})
	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, tc.inferenceRequest())
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, hits())
	// A lookup still sends one candidate per host.
	cands := p.soleFacade().resolveCandidates(tc.advertisedModel)
	out, _ := p.local.filterCandidates(p.soleFacade(), tc.advertisedModel, cands, false)
	assert.Len(t, out, 1)
}

// Codex P2: bound warm node full, other node idle but cooling: the request
// stays (queues) on the bound node rather than yielding to the cooling one.
func TestLocalAffinityDoesNotYieldToCoolingNode(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	f := p.soleFacade()
	_, held, _ := p.reserveCandidateKey(f, reservationCandidates("a", "b"), "conv")
	require.Equal(t, "a", held.nodeID)
	p.noteNoFirstByte("b", time.Now())
	out, _, d := p.reserveCandidateKey(f, reservationCandidates("a", "b"), "conv")
	assert.Equal(t, "a", out[0].id)
	assert.Equal(t, "hit", d)
}
