// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// Fork glue for proxy.go (catch-up step 5, hooks P1-P9). proxy.go is NVIDIA's;
// every fork behaviour it has is a marked one-line-ish call into this file.
// A nil *localState (NewProxy's default) runs NVIDIA's code path unchanged;
// main.go sets it (P1).
//
// Composition with NVIDIA #144 (first-byte cooldown): resolveCandidates
// demotes cooling nodes, then P5 filters by residency (order kept), then the
// slot-aware reservation ranks with routing.Better, whose outermost key is
// "not cooling". So the cooldown is the outer stable partition over the
// fork's slot-aware order: a cooling node never outranks a non-cooling one,
// even when it has a free slot and the other is full; among nodes in the same
// partition the fork's slot-aware order decides. An affinity-bound node that
// is cooling is not kept while a non-cooling candidate exists (rebind).

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"nvpair-proxy/local/affinity"
	"nvpair-proxy/local/capacity"
	"nvpair-proxy/local/respnorm"
	"nvpair-proxy/local/routing"
	"nvpair-shared/noderec"
	"nvpair-shared/schedulerwire"
)

// Aliases keep the fork's names inside package main.
type (
	Residency        = capacity.Residency
	CapacityProvider = capacity.Provider
)

const (
	ResidencyUnknown    = capacity.ResidencyUnknown
	ResidencyKnownEmpty = capacity.ResidencyKnownEmpty
	ResidencyLoaded     = capacity.ResidencyLoaded
)

type inflightKey struct{ engine, model, node string }

// completion is a local finish the scheduler may not have seen. It discounts
// a snapshot's pending only when the claim began before that snapshot was
// computed and ended after it, and, for an optimistic claim released within
// its own generation (reservedBy already dropped it), only once a later
// snapshot has been applied.
type completion struct {
	taken, done int64
	gen         uint64
	deferred    bool
}

// localClaim is the fork part of a reservation (embedded, P6).
type localClaim struct {
	// takenAt is when the claim began, Unix ms.
	takenAt int64
	engine  string
	model   string
	// reason is why routing chose this node, for the attempt log.
	reason string
	// optimistic is whether this also counted in priorityReservations; a
	// pinned request is counted in inflight only.
	optimistic bool
	// jit is whether this dispatch holds a JIT claim on the node.
	jit bool
}

// localState is the fork's process-wide proxy state (P3).
type localState struct {
	now func() time.Time // test seam; nil means time.Now

	// affinity pins a conversation's affinity key to a node.
	affinity affinity.Table
	// capacity supplies per-node slot counts and residency; nil means one
	// slot everywhere and nothing known.
	capacity CapacityProvider
	// directory is fed by every facade's discovery snapshot (P2).
	directory *capacity.Directory

	// Guarded by Proxy.priorityMu. inflight is the durable count of requests
	// this proxy dispatched and not finished, never reset by a snapshot;
	// reservedBy the optimistic ones since the last snapshot; completions
	// local finishes the scheduler may not have seen yet; pendingBy and
	// computedAt come from the snapshot.
	inflight    map[inflightKey]int
	reservedBy  map[inflightKey]int
	completions map[inflightKey][]completion
	pendingBy   map[string]map[string]int
	computedAt  map[string]int64

	claimMu     sync.Mutex
	jitClaims   map[string]map[jitClaim]int       // node -> live JIT claims
	jitReleased map[string]map[jitClaim]time.Time // node -> claims released at T, held until a newer sweep

	// suppressed holds not-loaded refusals (see eligible).
	suppressMu sync.Mutex
	suppressed map[inflightKey]time.Time
}

// newLocalState builds the production fork state (P1): directory capacity fed
// by discovery, layered under the NVPAIR_PROXY_CAPACITY overrides.
func newLocalState(now func() time.Time) *localState {
	s := &localState{now: now}
	s.directory = capacity.NewDirectory(s.clock)
	s.capacity = capacity.Layer(capacity.FromEnv(), s.directory)
	return s
}

// observeNodes feeds a facade's discovery snapshot to the directory (P2).
func (s *localState) observeNodes(engine string, nodes []noderec.DirectoryNode) {
	if s == nil || s.directory == nil {
		return
	}
	s.directory.Update(engine, nodes)
}

// rewriteBody normalises /v1/responses mid-history developer items and fixes
// Content-Length (P4). Runs after bufferBodyAndModel.
func (s *localState) rewriteBody(r *http.Request, body []byte) []byte {
	if s == nil || r.URL.Path != "/v1/responses" || len(body) == 0 {
		return body
	}
	nb := respnorm.Normalize(body)
	if string(nb) == string(body) {
		return body
	}
	r.ContentLength = int64(len(nb))
	r.Header.Set("Content-Length", strconv.Itoa(len(nb)))
	return nb
}

// filterCandidates drops owners of model that no live node has loaded (unless
// they can JIT-load it), keeping resolveCandidates' order, and marks the
// candidate served from this host's loopback engine (P5). It returns how many
// were dropped. requireEligible false (model lookups, /api/show) keeps every
// advertised owner: a lookup reads the model from disk and loads nothing.
func (s *localState) filterCandidates(f *facade, model string, cands []candidate, requireEligible bool) ([]candidate, int) {
	if s == nil {
		return cands, 0
	}
	if lb, ok := f.localBackendTarget(); ok {
		for i := range cands {
			if cands[i].url != nil && cands[i].url.Host == lb.Host && cands[i].url.Scheme == lb.Scheme {
				cands[i].local = true
			}
		}
	}
	if model == "" || !requireEligible {
		return cands, 0
	}
	out, dropped := cands[:0:0], 0
	for _, c := range cands {
		if s.eligible(c.id, f.profile.Name, model) {
			out = append(out, c)
		} else {
			dropped++
		}
	}
	return out, dropped
}

// localNotLoadedRejection turns an inference rejection into a 503 when owners
// exist but none has the model loaded (P5). notLoaded is 0 with a nil state.
func localNotLoadedRejection(notLoaded int, lookup bool, model, body, errText string, status int) (string, string, int) {
	if notLoaded == 0 || lookup || model == "" {
		return body, errText, status
	}
	msg, _ := json.Marshal("no live node has model " + model + " loaded")
	return `{"error":` + string(msg) + `}`, "no live node has model loaded", http.StatusServiceUnavailable
}

// annotateModel sets an OpenAI /v1/models entry's context_length to the
// smallest per-slot context among the routable owners that can serve it, or
// removes it when that is unknown (P8). Owners are every advertised routable
// candidate, not only those whose list call succeeded.
func (s *localState) annotateModel(f *facade, openAI bool, cands []candidate, key string, raw json.RawMessage) json.RawMessage {
	if s == nil || !openAI || s.capacity == nil {
		return raw
	}
	nodeByID := map[string]Node{}
	for _, n := range f.discovery.Nodes() {
		nodeByID[n.ID] = n
	}
	var owners []string
	for _, c := range cands {
		if n, ok := nodeByID[c.id]; ok && nodeAdvertisesModel(f.profile, n, key) {
			owners = append(owners, c.id)
		}
	}
	n, _ := s.minContextLength(f.profile.Name, key, owners)
	return capacity.WithContextLength(raw, n) // n==0 removes
}

// applySnapshot records the snapshot's per-model pending and computed-at, and
// prunes completions it now covers (P6). Caller holds priorityMu.
func (s *localState) applySnapshot(priority schedulerwire.Priority) {
	if s == nil {
		return
	}
	applied := s.clock().UnixMilli()
	pendingBy := make(map[string]map[string]int, len(priority.Ranks))
	computedAt := make(map[string]int64, len(priority.Ranks))
	for _, rank := range priority.Ranks {
		if rank.ID == "" {
			continue
		}
		if rank.PendingBy != nil {
			pendingBy[rank.ID] = rank.PendingBy
		}
		computedAt[rank.ID] = rank.ComputedAt
		if rank.ComputedAt == 0 {
			computedAt[rank.ID] = applied
		}
	}
	s.pendingBy, s.computedAt, s.reservedBy = pendingBy, computedAt, nil
	for k, ts := range s.completions {
		kept := ts[:0]
		for _, c := range ts {
			if c.done > computedAt[k.node] {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			delete(s.completions, k)
		} else {
			s.completions[k] = kept
		}
	}
}

// Proxy-level wrappers kept for the fork's tests and glue.

func (p *Proxy) nodeSlots(nodeID, engine, model string) int {
	if p.local == nil {
		return 1
	}
	return p.local.nodeSlots(nodeID, engine, model)
}

func (p *Proxy) eligible(nodeID, engine, model string) bool {
	return p.local == nil || p.local.eligible(nodeID, engine, model)
}

func (p *Proxy) suppress(k inflightKey) {
	if p.local != nil {
		p.local.suppress(k)
	}
}

func (p *Proxy) minContextLength(engine, model string, nodeIDs []string) (int, bool) {
	if p.local == nil {
		return 0, false
	}
	return p.local.minContextLength(engine, model, nodeIDs)
}

// classifyNotLoaded is routing.ClassifyNotLoaded with the proxy's first-byte
// timeout error.
func classifyNotLoaded(ctx context.Context, engine string, resp *http.Response, budget time.Duration) bool {
	return routing.ClassifyNotLoaded(ctx, engine, resp, budget, errFirstBodyTimeout)
}

// localReserveHook is reserveCandidate's fork path (P6): with fork state the
// reservation is slot-aware, with no affinity key and no model.
func (p *Proxy) localReserveHook(f *facade, candidates []candidate) ([]candidate, reservation, bool) {
	if p.local == nil {
		return candidates, reservation{}, false
	}
	out, res, _ := p.reserveCandidateKey(f, candidates, "")
	return out, res, true
}

// reserveCandidateKey is reserveCandidate with session affinity and no model,
// so slot occupancy is counted under an empty model.
func (p *Proxy) reserveCandidateKey(f *facade, candidates []candidate, key string) ([]candidate, reservation, string) {
	return p.reserveCandidateFor(f, candidates, key, "")
}

// reserveCandidateFor picks the node for a request for model and takes its
// reservation. With a non-empty key a bound node is kept while it has a free
// slot (hit), abandoned for any other free node (yield), and when every node is
// full kept only while its queue per slot is within one of the best. A bound
// node in NVIDIA's cooldown is not kept while a non-cooling candidate exists.
// The returned string is the affinity decision, empty when no key was given or
// no choice was made.
func (p *Proxy) reserveCandidateFor(f *facade, candidates []candidate, key, model string) ([]candidate, reservation, string) {
	if len(candidates) == 0 {
		return candidates, reservation{}, ""
	}
	s := p.local
	engine := f.profile.Name
	// Read before taking priorityMu: the cooldown has its own lock and nothing
	// else nests the two (NVIDIA #144).
	cooling := p.coolingSet(time.Now())
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()

	// A pinned request is not a choice, but it still occupies a slot.
	if selectedID := f.SelectedID(); selectedID != "" {
		for _, cand := range candidates {
			if cand.id == selectedID {
				// Same eligibility (incl. JIT claim) as the auto path; a
				// selected node with the model Loaded stays allowed.
				if model != "" && !s.eligible(selectedID, engine, model) {
					return nil, reservation{}, ""
				}
				res := p.takeLocked(selectedID, engine, model, false)
				res.reason = "pin"
				return candidates, res, ""
			}
		}
	}
	// Eligibility is re-checked here, under the lock the claim is taken under,
	// so two requests cannot both pass a stale check and both JIT-load.
	kept := candidates[:0:0]
	for _, c := range candidates {
		if model == "" || s.eligible(c.id, engine, model) {
			kept = append(kept, c)
		}
	}
	if candidates = kept; len(candidates) == 0 {
		return candidates, reservation{}, ""
	}
	if len(p.priority) == 0 {
		return candidates, p.takeLocked(candidates[0].id, engine, model, false), ""
	}

	candidateIndex := make(map[string]int, len(candidates))
	anyWarm := false
	for i, cand := range candidates {
		candidateIndex[cand.id] = i
		if !cooling[cand.id] {
			anyWarm = true
		}
	}
	occ := func(id string) int { return p.occLocked(inflightKey{engine, model, id}) }
	free := func(id string) bool { return occ(id) < s.nodeSlots(id, engine, model) }
	queue := func(id string) float64 { return float64(occ(id)) / float64(s.nodeSlots(id, engine, model)) }
	best := func(ok func(string) bool) int {
		return p.bestLocked(engine, model, candidates, nil, cooling, ok)
	}

	bestIndex := -1
	decision, reason := "", ""
	if key != "" {
		decision = affinity.Miss
		if bound, ok := s.affinity.Lookup(key); ok {
			decision = affinity.Rebind
			index, listed := candidateIndex[bound]
			if listed && containsID(p.priority, bound) && !(cooling[bound] && anyWarm) {
				// The hit test reads our own durable count, not scheduler
				// pending, which can be stale enough to break sequential hits.
				if s.inflight[inflightKey{engine, model, bound}] < s.nodeSlots(bound, engine, model) {
					bestIndex, decision, reason = index, affinity.Hit, "affinity-hit"
				} else if other := best(free); other >= 0 {
					bestIndex, decision, reason = other, affinity.Yield, "affinity-yield"
				} else if lowest := best(nil); lowest >= 0 {
					// Every node is full: stay unless another is clearly shorter.
					if queue(bound) <= queue(candidates[lowest].id)+1 {
						bestIndex, decision, reason = index, affinity.Hit, "affinity-hit"
					} else {
						bestIndex = lowest
					}
				}
			}
		}
	}
	if bestIndex < 0 {
		bestIndex = best(nil)
	}
	if bestIndex < 0 {
		return candidates, p.takeLocked(candidates[0].id, engine, model, false), ""
	}
	if reason == "" {
		reason = "least-queued"
		if free(candidates[bestIndex].id) {
			reason = "free-slot"
		}
	}

	chosen := candidates[bestIndex]
	if key != "" {
		s.affinity.Bind(key, chosen.id)
	}
	res := p.takeLocked(chosen.id, engine, model, true)
	res.reason = reason
	if bestIndex > 0 {
		copy(candidates[1:bestIndex+1], candidates[:bestIndex])
		candidates[0] = chosen
	}
	return candidates, res, decision
}

// takeLocked counts one request on a node: always in the durable inflight, and
// in the snapshot-scoped reservations when optimistic. Caller holds priorityMu.
func (p *Proxy) takeLocked(nodeID, engine, model string, optimistic bool) reservation {
	s := p.local
	if s.inflight == nil {
		s.inflight = make(map[inflightKey]int)
	}
	s.inflight[inflightKey{engine, model, nodeID}]++
	if optimistic {
		if p.priorityReservations == nil {
			p.priorityReservations = make(map[string]int)
		}
		if s.reservedBy == nil {
			s.reservedBy = make(map[inflightKey]int)
		}
		p.priorityReservations[nodeID]++
		s.reservedBy[inflightKey{engine, model, nodeID}]++
	}
	return reservation{
		nodeID: nodeID, generation: p.appliedPriorityGeneration, held: true,
		localClaim: localClaim{
			engine: engine, model: model, optimistic: optimistic,
			takenAt: s.clock().UnixMilli(), jit: s.claimJIT(nodeID, engine, model),
		},
	}
}

// dropLocked is takeLocked's inverse. The durable count is always decremented,
// whatever snapshot has arrived since; only the optimistic one is guarded by
// the generation, because a snapshot already folded that one in.
func (p *Proxy) dropLocked(r reservation) {
	s := p.local
	if r.jit {
		s.releaseJIT(r.nodeID, r.engine, r.model)
	}
	k := inflightKey{r.engine, r.model, r.nodeID}
	if s.inflight[k] <= 1 {
		delete(s.inflight, k)
	} else {
		s.inflight[k]--
	}
	same := r.optimistic && r.generation == p.appliedPriorityGeneration
	// Remembered either way: a snapshot computed while this ran but applied
	// after it finished would otherwise resurrect its occupancy.
	if s.completions == nil {
		s.completions = make(map[inflightKey][]completion)
	}
	c := completion{taken: r.takenAt, done: s.clock().UnixMilli(), gen: p.appliedPriorityGeneration, deferred: same}
	if ts := append(s.completions[k], c); len(ts) <= 1024 {
		s.completions[k] = ts
	} else {
		s.completions[k] = ts[len(ts)-1024:]
	}
	if !same {
		return
	}
	if s.reservedBy[k] <= 1 {
		delete(s.reservedBy, k)
	} else {
		s.reservedBy[k]--
	}
	if p.priorityReservations[r.nodeID] <= 1 {
		delete(p.priorityReservations, r.nodeID)
		return
	}
	p.priorityReservations[r.nodeID]--
}

// localDropLocked is releaseReservation's fork path (P6). Caller holds
// priorityMu and has checked r.held.
func (p *Proxy) localDropLocked(r reservation) bool {
	if p.local == nil {
		return false
	}
	p.dropLocked(r)
	return true
}

// localMoveLocked is moveReservation's fork path (P6): the optimistic part is
// retaken under the current snapshot. Caller holds priorityMu.
func (p *Proxy) localMoveLocked(r reservation, nodeID string) (reservation, bool) {
	if p.local == nil {
		return r, false
	}
	p.dropLocked(r)
	res := p.takeLocked(nodeID, r.engine, r.model, r.optimistic)
	res.reason = r.reason
	return res, true
}

// occLocked is how many requests occupy a node's slots for an engine/model:
// the larger of our own durable count and the scheduler's pending plus what we
// reserved since its snapshot, less local finishes it had not yet seen. A
// scheduler without a per-model split contributes its node-wide pending.
func (p *Proxy) occLocked(k inflightKey) int {
	s := p.local
	base := p.priorityPending[k.node]
	if by, ok := s.pendingBy[k.node]; ok {
		base = by[schedulerwire.PendingKey(k.engine, k.model)]
	}
	done := 0
	at := s.computedAt[k.node]
	for _, c := range s.completions[k] {
		if c.deferred && c.gen >= p.appliedPriorityGeneration {
			continue // reservedBy already dropped it
		}
		if c.taken <= at && c.done > at {
			done++
		}
	}
	return max(s.inflight[k], base+s.reservedBy[k]-done)
}

// bestLocked returns the index in cands of the best scheduler-listed candidate
// not in tried and accepted by ok, ranked by routing.Better (cooling last,
// then free slot, queue per slot, GPU pressure plus reservations, local node,
// scheduler order). Caller holds priorityMu.
func (p *Proxy) bestLocked(engine, model string, cands []candidate, tried, cooling map[string]bool, ok func(string) bool) int {
	index := make(map[string]int, len(cands))
	for i, c := range cands {
		index[c.id] = i
	}
	rc := make([]routing.Candidate, 0, len(cands))
	at := make([]int, 0, len(cands))
	for order, id := range p.priority {
		i, listed := index[id]
		if !listed || tried[id] || (ok != nil && !ok(id)) {
			continue
		}
		rc = append(rc, routing.Candidate{
			ID: id, Order: order,
			Slots: p.local.nodeSlots(id, engine, model),
			Occ:   p.occLocked(inflightKey{engine, model, id}),
			// Reservations through any engine's facade count as pressure:
			// they share the GPU even though they do not share slots.
			GPU:     p.priorityGPUPressure[id] + p.priorityReservations[id],
			Local:   cands[i].local,
			Cooling: cooling[id],
		})
		at = append(at, i)
	}
	if b := routing.Best(rc); b >= 0 {
		return at[b]
	}
	return -1
}

// rescoreReserve picks the best untried candidate right now and moves the
// request's held claim onto it in one critical section, so failover sees the
// load as it is rather than as it was when the round was formed. Candidates
// the scheduler does not list follow in round order.
func (p *Proxy) rescoreReserve(f *facade, round []candidate, tried map[string]bool, held *reservation, model string) (candidate, bool) {
	engine := f.profile.Name
	cooling := p.coolingSet(time.Now())
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	// Eligibility is re-checked under the reservation lock: another request or
	// engine may have loaded a model, or claimed the node, since the round was
	// built.
	skip := make(map[string]bool, len(round))
	for _, c := range round {
		skip[c.id] = tried[c.id] || !p.local.eligible(c.id, engine, model)
	}
	i := p.bestLocked(engine, model, round, skip, cooling, nil)
	if i < 0 {
		for j, c := range round {
			if !skip[c.id] {
				i = j
				break
			}
		}
	}
	if i < 0 {
		return candidate{}, false
	}
	cand := round[i]
	if !held.held {
		return cand, true
	}
	// The claim is published at each step so a panic below can never leave the
	// caller holding one that was already dropped, or lose the new one.
	old := *held
	*held = reservation{}
	p.dropLocked(old)
	*held = p.takeLocked(cand.id, engine, model, old.optimistic)
	held.reason = "free-slot"
	if p.occLocked(inflightKey{engine, model, cand.id}) > p.local.nodeSlots(cand.id, engine, model) {
		held.reason = "least-queued"
	}
	return cand, true
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// localReq is one request's fork routing state; nil when the proxy has no
// fork state, which makes every method below a no-op.
type localReq struct {
	p        *Proxy
	f        *facade
	model    string // set only for inference with a model
	post     bool
	key      string
	decision string
	tried    map[string]bool
	// notLoadedRetry marks the next attempt as the P9 retry, for its log.
	notLoadedRetry bool
	status         int
	ttfb           int64
}

// request starts a request's fork state (P6).
func (s *localState) request(p *Proxy, f *facade, r *http.Request, body []byte, model string, isInf bool) *localReq {
	if s == nil {
		return nil
	}
	lr := &localReq{p: p, f: f, post: r.Method == http.MethodPost, tried: map[string]bool{}}
	if isInf && model != "" {
		lr.model = model
		lr.key = affinity.Key(r, body)
	}
	return lr
}

// localReserve takes the request's reservation (P6): NVIDIA's reserveCandidate
// without fork state, else the slot-aware, affinity-aware one.
func (p *Proxy) localReserve(lr *localReq, f *facade, cands []candidate) ([]candidate, reservation) {
	if lr == nil {
		return p.reserveCandidate(f, cands)
	}
	out, held, d := p.reserveCandidateFor(f, cands, lr.key, lr.model)
	if d != "" {
		lr.decision = d
	}
	return out, held
}

// affinityDecision is the RequestEvent's affinity field (P6).
func (lr *localReq) affinityDecision() string {
	if lr == nil {
		return ""
	}
	return lr.decision
}

// onMove follows a failover that landed elsewhere with the affinity binding
// (P7). Caller holds the request's resMu.
func (lr *localReq) onMove(nodeID string, held reservation) {
	if lr == nil || lr.key == "" || !held.held {
		return
	}
	lr.p.local.affinity.Bind(lr.key, nodeID)
}

// resetTried starts a fresh round (P7).
func (lr *localReq) resetTried() {
	if lr != nil {
		lr.tried = map[string]bool{}
	}
}

// pick re-scores the untried candidates of the round before every attempt
// after its first, moving the held claim onto the winner (P7). skip reports
// that none is still eligible, which ends the round.
func (lr *localReq) pick(round []candidate, next int, cand candidate, resMu *sync.Mutex, held *reservation) (candidate, bool) {
	if lr == nil {
		return cand, false
	}
	if next > 0 && lr.model != "" {
		ok := true
		func() {
			resMu.Lock()
			defer resMu.Unlock()
			cand, ok = lr.p.rescoreReserve(lr.f, round, lr.tried, held, lr.model)
		}()
		if !ok {
			return cand, true
		}
	}
	lr.tried[cand.id] = true
	return cand, false
}

// attemptStart logs why this node was chosen (P7).
func (lr *localReq) attemptStart(reqID string, attempt int, nodeID string, ncands int, resMu *sync.Mutex, held *reservation) {
	if lr == nil || lr.model == "" {
		return
	}
	lr.status, lr.ttfb = 0, 0
	resMu.Lock()
	reason := held.reason
	resMu.Unlock()
	if lr.notLoadedRetry {
		reason, lr.notLoadedRetry = "notloaded-retry", false
	}
	slog.Info("attempt_start", "req", reqID, "attempt", attempt,
		"engine", lr.f.profile.Name, "model", lr.model, "node", nodeID,
		"reason", reason, "cands", ncands)
}

// notLoaded records the attempt's status and, when the engine refused the
// model as not loaded, suppresses the node for it and reports whether to
// retry on another (P9).
func (lr *localReq) notLoaded(ctx context.Context, resp *http.Response, nodeID string, last bool, attemptStart time.Time) bool {
	if lr == nil {
		return false
	}
	lr.status = resp.StatusCode
	lr.ttfb = time.Since(attemptStart).Milliseconds()
	if lr.model == "" || !lr.post ||
		(resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound) ||
		!classifyNotLoaded(ctx, lr.f.profile.Name, resp, firstBodyTimeout) {
		return false
	}
	lr.p.suppress(inflightKey{lr.f.profile.Name, lr.model, nodeID})
	if last {
		return false
	}
	lr.notLoadedRetry = true
	return true
}

// attemptEnd logs the attempt's outcome (P7).
func (lr *localReq) attemptEnd(reqID string, attempt int, nodeID string, attemptStart time.Time, retried bool) {
	if lr == nil || lr.model == "" {
		return
	}
	slog.Info("attempt_end", "req", reqID, "attempt", attempt, "node", nodeID,
		"status", lr.status, "ttfb_ms", lr.ttfb,
		"dur_ms", time.Since(attemptStart).Milliseconds(), "retried", retried)
}

// releaseEarly drops the claim as soon as the response has been copied (P6,
// fork #17): a follow-up turn of the same conversation arriving before the
// reporting defer would otherwise see the bound node as full and yield off
// it. NVIDIA's deferred release then sees a zero reservation and is a no-op.
func (lr *localReq) releaseEarly(resMu *sync.Mutex, held *reservation) {
	if lr == nil {
		return
	}
	resMu.Lock()
	defer resMu.Unlock()
	lr.p.releaseReservation(*held)
	*held = reservation{}
}

// resolveCandidatesN is NVIDIA's resolveCandidates followed by the P5
// residency filter, returning how many owners were dropped as not loaded.
func (f *facade) resolveCandidatesN(model string) ([]candidate, int) {
	return f.host.local.filterCandidates(f, model, f.resolveCandidates(model), true)
}
