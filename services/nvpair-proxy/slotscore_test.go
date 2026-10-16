package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvpair-shared/schedulerwire"
)

func inflightTotal(p *Proxy) int {
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	n := 0
	for _, v := range p.inflight {
		n += v
	}
	return n
}

// reserveN reserves n requests for model m without releasing any.
func reserveN(p *Proxy, n int, ids ...string) map[string]int {
	got := map[string]int{}
	for range n {
		out, _, _ := p.reserveCandidateFor(p.soleFacade(), reservationCandidates(ids...), "", "m")
		got[out[0].id]++
	}
	return got
}

func TestSlotScore_TwoByTwoSplitsEvenly(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	p.capacity = &fakeCap{slots: map[string]int{"a": 2, "b": 2}}
	if got := reserveN(p, 4, "a", "b"); got["a"] != 2 || got["b"] != 2 {
		t.Fatalf("split = %v, want 2/2", got)
	}
}

func TestSlotScore_FullNodeNotChosenWhileAnotherFree(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	p.capacity = &fakeCap{slots: map[string]int{"a": 1, "b": 3}}
	if got := reserveN(p, 4, "a", "b"); got["a"] != 1 || got["b"] != 3 {
		t.Fatalf("split = %v, want a=1 b=3", got)
	}
}

func TestSlotScore_SnapshotKeepsInflightAndMoveCancelLeavesZero(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	f := p.soleFacade()
	_, r1, _ := p.reserveCandidateFor(f, reservationCandidates("a", "b"), "", "m")
	_, r2, _ := p.reserveCandidateFor(f, reservationCandidates("a", "b"), "", "m")
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a", "b"}, Ranks: []schedulerwire.NodeRank{{ID: "a"}, {ID: "b"}}})
	if n := inflightTotal(p); n != 2 {
		t.Fatalf("inflight after snapshot = %d, want 2", n)
	}
	moved := p.moveReservation(r1, map[string]string{"a": "b", "b": "a"}[r1.nodeID])
	p.releaseReservation(moved)
	p.releaseReservation(r2)
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	if len(p.inflight) != 0 || len(p.reservedBy) != 0 {
		t.Fatalf("leaked: inflight=%v reservedBy=%v", p.inflight, p.reservedBy)
	}
}

func TestSlotScore_OtherModelDoesNotFillSlot(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	f := p.soleFacade()
	p.reserveCandidateFor(f, reservationCandidates("a"), "", "other")
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	if got := p.occLocked(inflightKey{f.profile.Name, "m", "a"}); got != 0 {
		t.Fatalf("occupancy of m on a = %d, want 0", got)
	}
}

func TestSlotScore_OccupancyUsesPendingByAndDiscountsCompletions(t *testing.T) {
	p := affinityProxy(t, "a")
	f := p.soleFacade()
	now := time.UnixMilli(10_000)
	p.now = func() time.Time { return now }
	key := schedulerwire.PendingKey(f.profile.Name, "m")
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a"}, Ranks: []schedulerwire.NodeRank{
		{ID: "a", Pending: 9, PendingBy: map[string]int{key: 2}, ComputedAt: 9_000}}})
	k := inflightKey{f.profile.Name, "m", "a"}
	p.priorityMu.Lock()
	if got := p.occLocked(k); got != 2 {
		t.Fatalf("occ = %d, want PendingBy 2 (not node-wide 9)", got)
	}
	p.priorityMu.Unlock()
	// A pinned request finishes after the scheduler computed: it is discounted.
	p.now = func() time.Time { return time.UnixMilli(8_000) }
	p.priorityMu.Lock()
	r := p.takeLocked("a", f.profile.Name, "m", false) // began before the snapshot was computed
	p.priorityMu.Unlock()
	p.now = func() time.Time { return now }
	p.releaseReservation(r)
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	if got := p.occLocked(k); got != 1 {
		t.Fatalf("occ after local completion = %d, want 1", got)
	}
}

func TestSlotScore_FailoverPicksFreeOverFull(t *testing.T) {
	p := affinityProxy(t, "a", "b", "c")
	f := p.soleFacade()
	p.capacity = &fakeCap{slots: map[string]int{"a": 1, "b": 1, "c": 1}}
	p.reserveCandidateFor(f, reservationCandidates("b"), "", "m") // b is full
	_, held, _ := p.reserveCandidateFor(f, reservationCandidates("a"), "", "m")
	round := reservationCandidates("a", "b", "c")
	cand, _ := p.rescoreReserve(f, round, map[string]bool{"a": true}, &held, "m")
	res := held
	if cand.id != "c" || res.nodeID != "c" {
		t.Fatalf("failover chose %s, want free node c", cand.id)
	}
	if n := inflightTotal(p); n != 2 {
		t.Fatalf("inflight = %d, want 2 (claim moved, not added)", n)
	}
}

func TestSlotScore_EligibilityKnownEmptyJITAndUnknown(t *testing.T) {
	tc := anyCase(t)
	a, _ := newCountingServer(t, 200, "")
	b, _ := newCountingServer(t, 200, "")
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "a", a, tc.advertisedModel))
	disc.AddManual(nodeForModel(t, "b", b, tc.advertisedModel))
	p := testProxy(tc.profile, disc, tc.profile.FacadePort)
	p.capacity = &fakeCap{res: map[string]Residency{"a": ResidencyKnownEmpty}} // b Unknown
	f := p.soleFacade()
	if cands, n := f.resolveCandidatesN(tc.advertisedModel); len(cands) != 1 || cands[0].id != "b" || n != 1 {
		t.Fatalf("cands=%v skipped=%d, want only b and 1 skipped", cands, n)
	}
	p.capacity.(*fakeCap).jit = map[string]bool{"a": true}
	if cands, _ := f.resolveCandidatesN(tc.advertisedModel); len(cands) != 2 {
		t.Fatalf("JIT node should be eligible, got %d", len(cands))
	}
	p.capacity.(*fakeCap).res["b"] = ResidencyKnownEmpty
	p.capacity.(*fakeCap).jit = nil
	rec := httptest.NewRecorder()
	f.handleHTTP(rec, tc.inferenceRequest())
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "no live node has model") {
		t.Fatalf("got %d %q, want 503 no live node has model", rec.Code, rec.Body.String())
	}
}

// notLoadedServer answers 400 with the Ollama not-loaded refusal.
func notLoadedServer(t *testing.T, status int, body string) (string, func() int) {
	return newCountingServer(t, status, body)
}

func TestSlotScore_NotLoaded400FailsOverAndSuppresses(t *testing.T) {
	tc := anyCase(t)
	bad, badHits := notLoadedServer(t, 400, `{"error":"model \"qwen3:latest\" not found"}`)
	good, goodHits := newCountingServer(t, 200, `{"done":true}`)
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "bad", bad, tc.advertisedModel))
	disc.AddManual(nodeForModel(t, "good", good, tc.advertisedModel))
	p := testProxy(tc.profile, disc, tc.profile.FacadePort)
	p.capacity = &fakeCap{}
	p.soleFacade().SetSelected("bad")
	rec := httptest.NewRecorder()
	p.soleFacade().handleHTTP(rec, tc.inferenceRequest())
	if rec.Code != 200 || badHits() != 1 || goodHits() != 1 {
		t.Fatalf("code=%d bad=%d good=%d, want 200 after one try each", rec.Code, badHits(), goodHits())
	}
	if p.eligible("bad", tc.profile.Name, tc.requestedModel) {
		t.Fatal("bad node should be suppressed")
	}
	// A directory sweep newer than the suppression showing it loaded lifts it.
	p.capacity = &fakeCap{res: map[string]Residency{"bad": ResidencyLoaded}, seen: time.Now().Add(time.Second)}
	if !p.eligible("bad", tc.profile.Name, tc.requestedModel) {
		t.Fatal("a newer Loaded sweep should lift the suppression")
	}
}

func TestSlotScore_NonMatchingAndLongBodiesPassThrough(t *testing.T) {
	tc := anyCase(t)
	long := `model "qwen3:latest" not found ` + strings.Repeat("x", 5000)
	for name, body := range map[string]string{"nonmatching": `{"error":"bad request"}`, "over4k": long} {
		srv, hits := notLoadedServer(t, 400, body)
		other, otherHits := newCountingServer(t, 200, "")
		disc := NewDiscovery()
		disc.AddManual(nodeForModel(t, "bad", srv, tc.advertisedModel))
		disc.AddManual(nodeForModel(t, "other", other, tc.advertisedModel))
		p := testProxy(tc.profile, disc, tc.profile.FacadePort)
		p.soleFacade().SetSelected("bad")
		rec := httptest.NewRecorder()
		p.soleFacade().handleHTTP(rec, tc.inferenceRequest())
		if rec.Code != 400 || rec.Body.String() != body || hits() != 1 || otherHits() != 0 {
			t.Fatalf("%s: code=%d len=%d hits=%d/%d, want the 400 untouched with no retry",
				name, rec.Code, rec.Body.Len(), hits(), otherHits())
		}
	}
}

func TestSlotScore_TruncatedCommittedStreamNotReplayed(t *testing.T) {
	tc := anyCase(t)
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"partial\":true}\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(cut.Close)
	other, otherHits := newCountingServer(t, 200, "")
	disc := NewDiscovery()
	disc.AddManual(nodeForModel(t, "cut", cut.URL, tc.advertisedModel))
	disc.AddManual(nodeForModel(t, "other", other, tc.advertisedModel))
	p := testProxy(tc.profile, disc, tc.profile.FacadePort)
	p.soleFacade().SetSelected("cut")
	func() {
		defer func() { _ = recover() }() // the copy error aborts the handler
		p.soleFacade().handleHTTP(httptest.NewRecorder(), tc.inferenceRequest())
	}()
	if otherHits() != 0 {
		t.Fatalf("a stream cut after its first bytes was replayed on another node (%d hits)", otherHits())
	}
	if n := inflightTotal(p); n != 0 {
		t.Fatalf("inflight after stream abort = %d, want 0", n)
	}
}

func TestSlotScore_AffinityHitUsesLocalInflight(t *testing.T) {
	p := affinityProxy(t, "a", "b")
	f := p.soleFacade()
	p.capacity = &fakeCap{slots: map[string]int{"a": 1, "b": 1}}
	_, r, _ := p.reserveCandidateFor(f, reservationCandidates("a", "b"), "k", "m")
	p.releaseReservation(r)
	// Stale scheduler pending says a is full; our own inflight says it is free.
	applySnapshot(p, schedulerwire.Priority{Nodes: []string{"a", "b"}, Ranks: []schedulerwire.NodeRank{
		{ID: "a", Pending: 5, ComputedAt: time.Now().UnixMilli() + 1000}, {ID: "b"}}})
	_, _, d := p.reserveCandidateFor(f, reservationCandidates("a", "b"), "k", "m")
	if d != affinityHit {
		t.Fatalf("decision = %q, want hit", d)
	}
}

func TestSlotScore_SuppressionAppliesWithoutProvider(t *testing.T) {
	p := affinityProxy(t, "a")
	p.capacity = nil
	p.suppress(inflightKey{"e", "m", "a"})
	if p.eligible("a", "e", "m") {
		t.Fatal("suppressed node eligible with no capacity provider")
	}
	p.capacity = &fakeCap{}
	if p.eligible("a", "e", "m") {
		t.Fatal("suppressed node eligible with Unknown residency")
	}
}

func notLoadedResp(r io.Reader) *http.Response {
	return &http.Response{StatusCode: 400, Body: io.NopCloser(r)}
}

type stallReader struct{ done chan struct{} }

func (s stallReader) Read([]byte) (int, error) { <-s.done; return 0, io.EOF }

func TestNotLoaded_ReadIsCancellable(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	resp := &http.Response{Body: struct {
		io.Reader
		io.Closer
	}{stallReader{done}, io.NopCloser(nil)}}
	start := time.Now()
	if classifyNotLoaded(context.Background(), "ollama", resp, 20*time.Millisecond) {
		t.Fatal("classified a stalled body")
	}
	if time.Since(start) > time.Second {
		t.Fatal("classification did not honour its deadline")
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("stalled body should surface the failure")
	}
}

type failAfter struct {
	data string
	sent bool
}

func (f *failAfter) Read(b []byte) (int, error) {
	if f.sent {
		return 0, io.ErrUnexpectedEOF
	}
	f.sent = true
	return copy(b, f.data), nil
}

func TestNotLoaded_ReadErrorPassesThrough(t *testing.T) {
	prefix := `{"error":"model \"x\" not found"}`
	resp := notLoadedResp(&failAfter{data: prefix})
	if classifyNotLoaded(context.Background(), "ollama", resp, time.Second) {
		t.Fatal("truncated body must not classify")
	}
	got, err := io.ReadAll(resp.Body)
	if string(got) != prefix || err != io.ErrUnexpectedEOF {
		t.Fatalf("got %q, %v; want prefix then ErrUnexpectedEOF", got, err)
	}
}
