package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nvpair-shared/schedulerwire"
)

func affinityProxy(t *testing.T, ids ...string) *Proxy {
	t.Helper()
	p := prProxy(t)
	ranks := make([]schedulerwire.NodeRank, 0, len(ids))
	for i, id := range ids {
		ranks = append(ranks, schedulerwire.NodeRank{ID: id, Rank: i})
	}
	applySnapshot(p, schedulerwire.Priority{Nodes: ids, Ranks: ranks})
	return p
}

// pick reserves for key over ids and returns the chosen node and the decision.
func pick(p *Proxy, key string, ids ...string) (string, string) {
	out, _, d := p.reserveCandidateKey(p.soleFacade(), reservationCandidates(ids...), key)
	return out[0].id, d
}

func TestAffinity_SameKeyStaysOnNode(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	first, d1 := pick(p, "conv-1", "a", "b")
	// Load now favors the other node, but the bound one must still win.
	second, d2 := pick(p, "conv-1", "a", "b")
	if first != second || d1 != affinityMiss || d2 != affinityHit {
		t.Fatalf("got %s/%s then %s/%s", first, d1, second, d2)
	}
}

func TestAffinity_DifferentKeysSpread(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	n1, _ := pick(p, "conv-1", "a", "b")
	n2, _ := pick(p, "conv-2", "a", "b")
	if n1 == n2 {
		t.Fatalf("both conversations landed on %s", n1)
	}
}

func TestAffinity_FallsBackAndRebinds(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	bound, _ := pick(p, "conv-1", "a", "b")
	other := map[string]string{"a": "b", "b": "a"}[bound]
	// Bound node lost the model: absent from the candidate list.
	got, d := pick(p, "conv-1", other)
	if got != other || d != affinityRebind {
		t.Fatalf("got %s/%s, want %s/rebind", got, d, other)
	}
	// Rebound: the next turn sticks to the new node.
	if got, d = pick(p, "conv-1", "a", "b"); got != other || d != affinityHit {
		t.Fatalf("after rebind got %s/%s, want %s/hit", got, d, other)
	}
	// Bound node no longer reservable (dropped from the scheduler list).
	p2 := affinityProxy(t, "a", "b")
	b2, _ := pick(p2, "k", "a", "b")
	live := map[string]string{"a": "b", "b": "a"}[b2]
	applySnapshot(p2, schedulerwire.Priority{Nodes: []string{live}, Ranks: []schedulerwire.NodeRank{{ID: live}}})
	if got, d = pick(p2, "k", "a", "b"); got != live || d != affinityRebind {
		t.Fatalf("unreservable: got %s/%s, want %s/rebind", got, d, live)
	}
}

func TestAffinity_NoKeyUnchanged(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	n1, d1 := pick(p, "", "a", "b")
	n2, d2 := pick(p, "", "a", "b")
	if n1 == n2 || d1 != "" || d2 != "" {
		t.Fatalf("keyless requests should spread without a decision: %s/%q %s/%q", n1, d1, n2, d2)
	}
	if len(p.affinity.m) != 0 {
		t.Fatalf("keyless request created a binding")
	}
}

func TestAffinity_TTLExpiryAndRefresh(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	now := time.Unix(1000, 0)
	p.affinity.now = func() time.Time { return now }
	pick(p, "k", "a", "b")
	now = now.Add(affinityTTL - time.Second)
	if _, d := pick(p, "k", "a", "b"); d != affinityHit {
		t.Fatalf("within TTL: %s", d)
	}
	now = now.Add(affinityTTL - time.Second) // refreshed by the hit above
	if _, d := pick(p, "k", "a", "b"); d != affinityHit {
		t.Fatalf("TTL not refreshed on use: %s", d)
	}
	now = now.Add(affinityTTL + time.Second)
	if _, d := pick(p, "k", "a", "b"); d != affinityMiss {
		t.Fatalf("after TTL: %s", d)
	}
}

func TestAffinity_SizeCap(t *testing.T) {
	var tbl affinityTable
	for i := 0; i < affinityMaxKeys+10; i++ {
		tbl.bind(string(rune('a'+i%26))+time.Duration(i).String(), "n")
	}
	if len(tbl.m) > affinityMaxKeys {
		t.Fatalf("table grew to %d", len(tbl.m))
	}
}

func TestAffinityKey(t *testing.T) {
	req := func(h map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/responses", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	body := []byte(`{"model":"m","prompt_cache_key":"root"}`)
	if got := affinityKey(req(map[string]string{"thread-id": "t1", "session-id": "root"}), body); got != "t1" {
		t.Fatalf("thread-id should win, got %q", got)
	}
	if got := affinityKey(req(nil), body); got != "root" {
		t.Fatalf("prompt_cache_key fallback, got %q", got)
	}
	if got := affinityKey(req(map[string]string{"session-id": "s"}), []byte(`{"model":"m"}`)); got != "s" {
		t.Fatalf("session-id fallback, got %q", got)
	}
	if got := affinityKey(req(nil), []byte(`{"model":"m"}`)); got != "" {
		t.Fatalf("expected no key, got %q", got)
	}
}
