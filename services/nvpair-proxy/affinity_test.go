package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
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

// turn is a finished request: reserve, then release, so the slot is free again.
func turn(p *Proxy, key string, ids ...string) (string, string) {
	out, res, d := p.reserveCandidateKey(p.soleFacade(), reservationCandidates(ids...), key)
	p.releaseReservation(res)
	return out[0].id, d
}

func TestAffinity_SequentialTurnsAllHit(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	first, _ := turn(p, "conv", "a", "b")
	for i := 0; i < 5; i++ {
		if got, d := turn(p, "conv", "a", "b"); got != first || d != affinityHit {
			t.Fatalf("turn %d: %s/%s, want %s/hit", i, got, d, first)
		}
	}
}

func TestAffinity_YieldsToFreeNode(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	first, _ := pick(p, "conv", "a", "b") // slot stays held
	other := map[string]string{"a": "b", "b": "a"}[first]
	if got, d := pick(p, "conv", "a", "b"); got != other || d != affinityYield {
		t.Fatalf("got %s/%s, want %s/yield", got, d, other)
	}
	if got, d := turn(p, "conv", "a", "b"); got != other || d != affinityHit {
		t.Fatalf("not rebound: %s/%s", got, d)
	}
}

func TestAffinity_AllFullTolerance(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	p.capacity = &fakeCap{slots: map[string]int{"a": 1, "b": 1}}
	bound, _ := pick(p, "conv", "a", "b")
	other := map[string]string{"a": "b", "b": "a"}[bound]
	pick(p, "x", "a", "b") // fills the other node
	// 1 vs 1: within tolerance, stay on the bound node.
	if got, d := pick(p, "conv", "a", "b"); got != bound || d != affinityHit {
		t.Fatalf("within tolerance: %s/%s", got, d)
	}
	// Bound now holds 2 against 1 on the other: 2 <= 1+1 still stays.
	pick(p, "conv", "a", "b")
	// 3 against 1 is outside the tolerance: go to the shorter queue.
	if got, d := pick(p, "conv", "a", "b"); got != other || d != affinityRebind {
		t.Fatalf("outside tolerance: %s/%s, want %s/rebind", got, d, other)
	}
}

func TestAffinity_SameKeyStaysOnNode(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	first, d1 := turn(p, "conv-1", "a", "b")
	second, d2 := turn(p, "conv-1", "a", "b")
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
	turn(p, "k", "a", "b")
	now = now.Add(affinityTTL - time.Second)
	if _, d := turn(p, "k", "a", "b"); d != affinityHit {
		t.Fatalf("within TTL: %s", d)
	}
	now = now.Add(affinityTTL - time.Second) // refreshed by the hit above
	if _, d := turn(p, "k", "a", "b"); d != affinityHit {
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

// reportGate is a slog handler that parks the first "proxy request complete"
// record (the first statement of the handler's reporting defer, before any
// codec write) so a test can act while that request is still in reporting. It
// also records the node and affinity decision of every completion.
type reportGate struct {
	blocked chan struct{}
	release chan struct{}
	mu      sync.Mutex
	done    [][2]string // node_id, affinity
}

func (g *reportGate) Enabled(context.Context, slog.Level) bool { return true }
func (g *reportGate) WithAttrs([]slog.Attr) slog.Handler       { return g }
func (g *reportGate) WithGroup(string) slog.Handler            { return g }
func (g *reportGate) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "proxy request complete" {
		return nil
	}
	var rec [2]string
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "node_id":
			rec[0] = a.Value.String()
		case "affinity":
			rec[1] = a.Value.String()
		}
		return true
	})
	g.mu.Lock()
	g.done = append(g.done, rec)
	first := len(g.done) == 1
	g.mu.Unlock()
	if first {
		close(g.blocked)
		<-g.release
	}
	return nil
}

// #17: the slot claim must be released once the response is copied, not after
// reporting, or the next turn of the conversation yields off its bound node.
func TestAffinity_SlotReleasedBeforeReporting(t *testing.T) {
	tc := anyCase(t)
	g := &reportGate{blocked: make(chan struct{}), release: make(chan struct{})}
	prev := slog.Default()
	slog.SetDefault(slog.New(g))
	t.Cleanup(func() { slog.SetDefault(prev) })
	disc := NewDiscovery()
	for _, id := range []string{"a", "b"} {
		srv, _ := newCountingServer(t, 200, "ok")
		disc.AddManual(nodeForModel(t, id, srv, tc.advertisedModel))
	}
	p := testProxy(tc.profile, disc, tc.profile.FacadePort)
	p.capacity = &fakeCap{slots: map[string]int{"a": 1, "b": 1}}
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a", "b"}, Ranks: []schedulerwire.NodeRank{{ID: "a"}, {ID: "b", Rank: 1}}})
	f := p.soleFacade()
	send := func() {
		req := tc.inferenceRequest()
		req.Header.Set("thread-id", "conv")
		f.handleHTTP(httptest.NewRecorder(), req)
	}
	var unblock sync.Once
	open := func() { unblock.Do(func() { close(g.release) }) }
	t.Cleanup(open) // a regression must fail fast, not leave a handler parked
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	run := func() <-chan struct{} {
		c := make(chan struct{})
		go func() { defer close(c); send() }()
		return c
	}
	first := run()
	wait(g.blocked, "first request to reach reporting") // response copied; parked in reporting
	wait(run(), "second request to complete")
	open()
	wait(first, "first request to complete")
	if len(g.done) != 2 || g.done[1][1] != affinityHit || g.done[1][0] != g.done[0][0] {
		t.Fatalf("second turn was not an affinity hit on the same node: %v", g.done)
	}
}
