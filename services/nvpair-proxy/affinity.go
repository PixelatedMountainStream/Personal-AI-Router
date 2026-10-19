// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Session affinity keeps one conversation's turns on the node that already
// holds its KV/prompt cache, while distinct conversations still spread by load.
const (
	affinityTTL     = 30 * time.Minute
	affinityMaxKeys = 4096
)

// Affinity decisions reported in the per-request log.
const (
	affinityHit    = "hit"    // bound node was eligible with a free slot (or not worse than all-full peers)
	affinityMiss   = "miss"   // key not seen before; bound to the load-picked node
	affinityRebind = "rebind" // bound node unusable; fell back and rebound
	affinityYield  = "yield"  // bound node full while another was free; moved
)

type affinityEntry struct {
	nodeID  string
	expires time.Time
}

// affinityTable maps an affinity key to a node. The zero value is ready to use.
type affinityTable struct {
	mu  sync.Mutex
	m   map[string]affinityEntry
	now func() time.Time // test seam; nil means time.Now
}

func (t *affinityTable) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// lookup returns the live binding for key, refreshing its TTL.
func (t *affinityTable) lookup(key string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[key]
	if !ok {
		return "", false
	}
	now := t.clock()
	if !now.Before(e.expires) {
		delete(t.m, key)
		return "", false
	}
	e.expires = now.Add(affinityTTL)
	t.m[key] = e
	return e.nodeID, true
}

// bind records key -> nodeID, evicting expired entries and then arbitrary
// ones when the table is full.
func (t *affinityTable) bind(key, nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]affinityEntry)
	}
	now := t.clock()
	if _, exists := t.m[key]; !exists && len(t.m) >= affinityMaxKeys {
		for k, e := range t.m {
			if !now.Before(e.expires) {
				delete(t.m, k)
			}
		}
		for k := range t.m {
			if len(t.m) < affinityMaxKeys {
				break
			}
			delete(t.m, k)
		}
	}
	t.m[key] = affinityEntry{nodeID: nodeID, expires: now.Add(affinityTTL)}
}

// affinityKey extracts the per-conversation key for an inference request.
// Codex sends a "thread-id" header that is distinct per thread, including each
// spawned sub-agent, so it is preferred. The body's prompt_cache_key is a
// fallback; for codex it is the root session id shared with sub-agents, so on
// its own it would pin a whole agent tree to one node. Empty means no affinity.
func affinityKey(r *http.Request, body []byte) string {
	if v := strings.TrimSpace(r.Header.Get("thread-id")); v != "" {
		return v
	}
	var probe struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.PromptCacheKey != "" {
		return probe.PromptCacheKey
	}
	return strings.TrimSpace(r.Header.Get("session-id"))
}
