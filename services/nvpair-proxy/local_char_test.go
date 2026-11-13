// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// Step-5 characterization tests for the fork's proxy.go behaviour, written
// against pre-step develop. Only the char* helpers below may change when the
// fork state moves behind seams; the test bodies and assertions may not.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nvpair-shared/noderec"
	"nvpair-shared/schedulerwire"
)

// charLocal turns on the fork's routing state with capacity c (nil: none).
func charLocal(p *Proxy, c CapacityProvider) *Proxy {
	p.capacity = c
	return p
}

func charDirectory(engine string, nodes ...noderec.DirectoryNode) CapacityProvider {
	d := newDirectoryCapacity(nil)
	d.update(engine, nodes)
	return d
}

func charEligible(p *Proxy, id, engine, model string) bool { return p.eligible(id, engine, model) }

// charReserve reserves a node for model and returns its id (the claim is kept).
func charReserve(p *Proxy, f *facade, cands []candidate, model string) string {
	out, res, _ := p.reserveCandidateFor(f, append([]candidate(nil), cands...), "", model)
	if !res.held || len(out) == 0 {
		return ""
	}
	return out[0].id
}

func charRanked(t *testing.T, tc engineCase, ids ...string) (*Proxy, *facade) {
	t.Helper()
	disc := NewDiscovery()
	p := testProxy(tc.profile, disc, tc.profile.FacadePort)
	ranks := make([]schedulerwire.NodeRank, len(ids))
	for i, id := range ids {
		ranks[i] = schedulerwire.NodeRank{ID: id, Rank: i}
	}
	applySnapshot(p, schedulerwire.Priority{Nodes: ids, Ranks: ranks})
	return p, p.soleFacade()
}

// LM Studio slots: lowest occupancy per slot wins, scheduler order breaks
// ties, and a full node is passed over while another has a free slot.
func TestCharLMStudioSlotsBalanceThenAvoidFull(t *testing.T) {
	tc := lmstudioCase(t)
	p, f := charRanked(t, tc, "a", "b")
	charLocal(p, &fakeCap{slots: map[string]int{"a": 3, "b": 1}})
	cands := []candidate{{id: "a"}, {id: "b"}}
	got := []string{
		charReserve(p, f, cands, tc.requestedModel),
		charReserve(p, f, cands, tc.requestedModel),
		charReserve(p, f, cands, tc.requestedModel),
		charReserve(p, f, cands, tc.requestedModel),
	}
	assert.Equal(t, []string{"a", "b", "a", "a"}, got)
}

// LM Studio does not JIT-load by default: a node that reports nothing loaded
// is ineligible unless it opts in with jit_load.
func TestCharLMStudioJITOffByDefault(t *testing.T) {
	p := testProxy(lmstudioCase(t).profile, NewDiscovery(), 0)
	empty := map[string][]string{"lmstudio": {}}
	charLocal(p, charDirectory("lmstudio",
		noderec.DirectoryNode{HostUUID: "off", LoadedByEngine: empty},
		noderec.DirectoryNode{HostUUID: "on", LoadedByEngine: empty, LocalNodeExt: noderec.LocalNodeExt{JITLoadByEngine: map[string]bool{"lmstudio": true}}},
	))
	assert.False(t, charEligible(p, "off", "lmstudio", "qwen3-8b"), "jit off by default")
	assert.True(t, charEligible(p, "on", "lmstudio", "qwen3-8b"), "jit_load opt-in")
}

// An LM Studio loaded instance makes its node eligible without JIT; another
// model on the same node is not.
func TestCharLMStudioLoadedInstanceEligible(t *testing.T) {
	p := testProxy(lmstudioCase(t).profile, NewDiscovery(), 0)
	charLocal(p, charDirectory("lmstudio",
		noderec.DirectoryNode{HostUUID: "n", LoadedByEngine: map[string][]string{"lmstudio": {"qwen3-8b"}}},
	))
	assert.True(t, charEligible(p, "n", "lmstudio", "qwen3-8b"))
	assert.False(t, charEligible(p, "n", "lmstudio", "other-model"))
}

// Inference for an advertised model nobody has loaded is a 503; for a model
// nobody advertises it stays the upstream 502.
func TestCharLMStudioNotLoaded503NoOwner502(t *testing.T) {
	tc := lmstudioCase(t)
	a, hits := newCountingServer(t, 200, "{}")
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "a", a, tc.advertisedModel))
	p := charLocal(testProxy(tc.profile, disc, tc.profile.FacadePort), &fakeCap{res: map[string]Residency{"a": ResidencyKnownEmpty}})

	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, tc.inferenceRequest())
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, httptest.NewRequest(http.MethodPost, tc.inferencePath, strings.NewReader(`{"model":"nobody-has-it"}`)))
	assert.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	assert.Equal(t, 0, hits())
}

// /v1/responses mid-history developer items reach the engine rewritten, with
// a matching Content-Length.
func TestCharResponsesInputNormalisedBeforeForward(t *testing.T) {
	tc := lmstudioCase(t)
	var mu sync.Mutex
	var gotBody string
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody, gotLen = string(b), r.ContentLength
		mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "a", srv.URL, tc.advertisedModel))
	p := charLocal(testProxy(tc.profile, disc, tc.profile.FacadePort), nil)
	body := `{"model":"qwen3-8b","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"b"}]}]}`
	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, gotBody, `"[developer]\nb"`)
	assert.Equal(t, int64(len(gotBody)), gotLen)
}
