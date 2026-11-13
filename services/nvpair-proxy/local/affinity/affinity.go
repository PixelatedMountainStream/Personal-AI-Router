// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package affinity

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
	TTL     = 30 * time.Minute
	MaxKeys = 4096
)

// Affinity decisions reported in the per-request log.
const (
	Hit    = "hit"    // bound node was eligible with a free slot (or not worse than all-full peers)
	Miss   = "miss"   // key not seen before; bound to the load-picked node
	Rebind = "rebind" // bound node unusable; fell back and rebound
	Yield  = "yield"  // bound node full while another was free; moved
)

type entry struct {
	nodeID  string
	expires time.Time
}

// Table maps an affinity key to a node. The zero value is ready to use.
type Table struct {
	mu  sync.Mutex
	m   map[string]entry
	Now func() time.Time // test seam; nil means time.Now
}

func (t *Table) clock() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// lookup returns the live binding for key, refreshing its TTL.
func (t *Table) Lookup(key string) (string, bool) {
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
	e.expires = now.Add(TTL)
	t.m[key] = e
	return e.nodeID, true
}

// bind records key -> nodeID, evicting expired entries and then arbitrary
// ones when the table is full.
func (t *Table) Bind(key, nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]entry)
	}
	now := t.clock()
	if _, exists := t.m[key]; !exists && len(t.m) >= MaxKeys {
		for k, e := range t.m {
			if !now.Before(e.expires) {
				delete(t.m, k)
			}
		}
		for k := range t.m {
			if len(t.m) < MaxKeys {
				break
			}
			delete(t.m, k)
		}
	}
	t.m[key] = entry{nodeID: nodeID, expires: now.Add(TTL)}
}

// Len is how many keys are bound (expired ones included until swept).
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

// Key extracts the per-conversation key for an inference request.
// Codex sends a "thread-id" header that is distinct per thread, including each
// spawned sub-agent, so it is preferred. The body's prompt_cache_key is a
// fallback; for codex it is the root session id shared with sub-agents, so on
// its own it would pin a whole agent tree to one node. Empty means no affinity.
func Key(r *http.Request, body []byte) string {
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
