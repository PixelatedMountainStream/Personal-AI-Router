// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// Fork glue (catch-up step 5): residency eligibility, JIT claims and
// not-loaded suppression on localState. Capacity sources live in
// local/capacity; this file only reads them.

import (
	"time"

	"nvpair-proxy/local/capacity"
)

// nodeSlots is the slot count routing uses: the provider's answer, else 1.
func (s *localState) nodeSlots(nodeID, engine, model string) int {
	if s.capacity != nil {
		if n, ok := s.capacity.Slots(nodeID, engine, model); ok && n > 0 {
			return n
		}
	}
	return 1
}

// eligible reports whether a node may be sent a request for the model: it has
// it loaded, nothing is known (legacy node), or it is known empty but loads on
// demand. A node that just refused the model as not loaded is skipped for
// suppressTTL unless a newer directory sweep shows it loaded.
func (s *localState) eligible(nodeID, engine, model string) bool {
	r, seen := ResidencyUnknown, time.Time{}
	if s.capacity != nil {
		r, seen = s.capacity.Residency(nodeID, engine, model)
	}
	if at, ok := s.suppressedAt(inflightKey{engine, model, nodeID}); ok {
		if r != ResidencyLoaded || !seen.After(at) {
			return false
		}
	}
	if s.capacity == nil {
		return true
	}
	if r != ResidencyKnownEmpty {
		return true
	}
	// A JIT load would put a second large model on a node whose other engine
	// already holds one; the proxy has no sizes, so any loaded model counts.
	if o, ok := s.capacity.(capacity.OtherEngineLoader); ok && o.OtherEngineLoaded(nodeID, engine) {
		return false
	}
	if !s.capacity.JITLoad(nodeID, engine) {
		return false
	}
	// A JIT load already dispatched here for another engine or model would
	// make this a second one. The claim is in-process only: concurrent cold
	// requests through other proxies are out of scope (runbook covers them).
	return !s.jitClaimedByOther(nodeID, engine, model)
}

type jitClaim struct{ engine, model string }

// jitReleaseCap bounds how long a released claim stays effective, so a dead
// scanner cannot wedge a node.
const jitReleaseCap = 2 * time.Minute

// jitClaimedByOther reports whether a live JIT claim on the node is for a
// different engine or model. A claim whose request ended stays live until the
// directory reports a sweep newer than the release (so the load shows as
// Loaded) or jitReleaseCap passes.
func (s *localState) jitClaimedByOther(nodeID, engine, model string) bool {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	other := func(c jitClaim) bool {
		return c.engine != engine || capacity.ModelKey(c.engine, c.model) != capacity.ModelKey(engine, model)
	}
	for c, n := range s.jitClaims[nodeID] {
		if n > 0 && other(c) {
			return true
		}
	}
	now := s.clock()
	for c, t := range s.jitReleased[nodeID] {
		expired := now.Sub(t) >= jitReleaseCap
		if !expired && s.capacity != nil {
			if _, seen := s.capacity.Residency(nodeID, c.engine, c.model); seen.After(t) {
				expired = true
			}
		}
		if expired {
			delete(s.jitReleased[nodeID], c) // lazy cleanup
			continue
		}
		if other(c) {
			return true
		}
	}
	if len(s.jitReleased[nodeID]) == 0 {
		delete(s.jitReleased, nodeID)
	}
	return false
}

// LIMITATIONS (documented, not fixed): the JIT claim is in-process, so an
// engine-manager pinned load and a proxy JIT load on the same node are not
// coordinated across processes. Clearing an uncertain load elsewhere is a
// bounded heuristic, not a guarantee.

// claimJIT records a dispatch to a KnownEmpty+JIT node and reports whether it
// did, so the matching release is exact. Released when the request ends.
func (s *localState) claimJIT(nodeID, engine, model string) bool {
	if s.capacity == nil {
		return false
	}
	if r, _ := s.capacity.Residency(nodeID, engine, model); r != ResidencyKnownEmpty || !s.capacity.JITLoad(nodeID, engine) {
		return false
	}
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	if s.jitClaims == nil {
		s.jitClaims = map[string]map[jitClaim]int{}
	}
	if s.jitClaims[nodeID] == nil {
		s.jitClaims[nodeID] = map[jitClaim]int{}
	}
	s.jitClaims[nodeID][jitClaim{engine, model}]++
	return true
}

func (s *localState) releaseJIT(nodeID, engine, model string) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	c := jitClaim{engine, model}
	if s.jitClaims[nodeID][c] <= 1 {
		delete(s.jitClaims[nodeID], c)
		if s.jitReleased == nil {
			s.jitReleased = map[string]map[jitClaim]time.Time{}
		}
		if s.jitReleased[nodeID] == nil {
			s.jitReleased[nodeID] = map[jitClaim]time.Time{}
		}
		s.jitReleased[nodeID][c] = s.clock()
		if len(s.jitClaims[nodeID]) == 0 {
			delete(s.jitClaims, nodeID)
		}
	} else {
		s.jitClaims[nodeID][c]--
	}
}

// suppressTTL is how long a not-loaded refusal keeps a node out of routing.
const suppressTTL = 60 * time.Second

func (s *localState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// suppress records that node refused model as not loaded.
func (s *localState) suppress(k inflightKey) {
	s.suppressMu.Lock()
	defer s.suppressMu.Unlock()
	if s.suppressed == nil {
		s.suppressed = make(map[inflightKey]time.Time)
	}
	s.suppressed[k] = s.clock()
}

// suppressedAt returns when a live suppression began.
func (s *localState) suppressedAt(k inflightKey) (time.Time, bool) {
	s.suppressMu.Lock()
	defer s.suppressMu.Unlock()
	at, ok := s.suppressed[k]
	if ok && s.clock().Sub(at) >= suppressTTL {
		delete(s.suppressed, k)
		return time.Time{}, false
	}
	return at, ok
}

// minContextLength is the smallest per-slot context over every node that
// listed the model and is eligible for it (Loaded, Unknown or JIT). It is
// false when no node qualifies or when any eligible node has no value: a
// client that needs a safe window must not be handed an optimistic one.
func (s *localState) minContextLength(engine, model string, nodeIDs []string) (int, bool) {
	if s.capacity == nil {
		return 0, false
	}
	min, any := 0, false
	for _, id := range nodeIDs {
		if !s.eligible(id, engine, model) {
			continue
		}
		n, ok := s.capacity.CtxPerSlot(id, engine, model)
		if !ok || n <= 0 {
			return 0, false
		}
		if !any || n < min {
			min, any = n, true
		}
	}
	return min, any
}
