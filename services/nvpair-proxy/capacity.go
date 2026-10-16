package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"nvpair-shared/engines"
)

// Residency is what the directory says about a model on a node's engine.
type Residency int

const (
	// ResidencyUnknown: the engine reported nothing (legacy node, no sweep
	// yet). Missing data is Unknown, never KnownEmpty.
	ResidencyUnknown Residency = iota
	// ResidencyKnownEmpty: the engine reported its loaded set and the model is
	// not in it.
	ResidencyKnownEmpty
	// ResidencyLoaded: the engine reported the model loaded.
	ResidencyLoaded
)

// CapacityProvider is where routing learns a node's capacity and residency.
// Today the node-settings overrides below implement it; the directory-backed
// implementation replaces them without touching routing.
type CapacityProvider interface {
	// Slots is how many requests the engine runs at once for the model;
	// ok is false when unknown (routing then assumes 1).
	Slots(nodeID, engine, model string) (slots int, ok bool)
	// CtxPerSlot is the per-slot context window; ok is false when unknown.
	CtxPerSlot(nodeID, engine, model string) (ctx int, ok bool)
	// Residency reports whether the model is loaded, and when the directory
	// observed that (zero time when not known).
	Residency(nodeID, engine, model string) (r Residency, observedAt time.Time)
	// JITLoad reports whether the engine loads a missing model on demand.
	JITLoad(nodeID, engine string) bool
}

// capacityEnv names the environment variable carrying node-settings overrides
// as one flat JSON object, e.g. {"engine_slots.lmstudio.qwen3.8-27b-mlx": 4}.
const capacityEnv = "NVPAIR_PROXY_CAPACITY"

// settingsCapacity serves slot counts from the flat node-settings keys
// `engine_slots.<engine>.<model>`. The same value applies to every node.
type settingsCapacity struct {
	slots map[string]int  // "engine/model" -> slots
	ctx   map[string]int  // "engine/model" -> context per slot
	jit   map[string]bool // engine -> jit_load override
}

func (c settingsCapacity) Slots(_, engine, model string) (int, bool) {
	n, ok := c.slots[engine+"/"+model]
	return n, ok && n > 0
}

func (c settingsCapacity) CtxPerSlot(_, engine, model string) (int, bool) {
	n, ok := c.ctx[engine+"/"+model]
	return n, ok && n > 0
}

// Residency is always Unknown here: settings carry no loaded set.
func (settingsCapacity) Residency(_, _, _ string) (Residency, time.Time) {
	return ResidencyUnknown, time.Time{}
}

// JITLoad defaults to true for Ollama and llama.cpp and false for LM Studio.
func (c settingsCapacity) JITLoad(_, engine string) bool {
	if v, ok := c.jit[engine]; ok {
		return v
	}
	return engine != engines.NameLMStudio
}

// parseCapacitySettings reads the flat settings object. The model is
// everything after the engine, so it may itself contain dots.
func parseCapacitySettings(raw map[string]json.RawMessage) settingsCapacity {
	c := settingsCapacity{slots: map[string]int{}, ctx: map[string]int{}, jit: map[string]bool{}}
	for k, v := range raw {
		val := strings.Trim(string(v), `"`)
		if engine, ok := strings.CutPrefix(k, "jit_load."); ok {
			if b, err := strconv.ParseBool(val); err == nil && engine != "" {
				c.jit[engine] = b
			}
			continue
		}
		dst := c.slots
		rest, ok := strings.CutPrefix(k, "engine_slots.")
		if !ok {
			if rest, ok = strings.CutPrefix(k, "engine_ctx_per_slot."); !ok {
				continue
			}
			dst = c.ctx
		}
		engine, model, ok := strings.Cut(rest, ".")
		if !ok || engine == "" || model == "" {
			continue
		}
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			dst[engine+"/"+model] = n
		}
	}
	return c
}

func capacityFromEnv() CapacityProvider {
	s := os.Getenv(capacityEnv)
	if s == "" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		slog.Warn("ignoring malformed capacity overrides", "env", capacityEnv, "err", err)
		return nil
	}
	return parseCapacitySettings(raw)
}

// nodeSlots is the slot count routing uses: the provider's answer, else 1.
func (p *Proxy) nodeSlots(nodeID, engine, model string) int {
	if p.capacity != nil {
		if n, ok := p.capacity.Slots(nodeID, engine, model); ok && n > 0 {
			return n
		}
	}
	return 1
}

// eligible reports whether a node may be sent a request for the model: it has
// it loaded, nothing is known (legacy node), or it is known empty but loads on
// demand. A node that just refused the model as not loaded is skipped for
// suppressTTL unless a newer directory sweep shows it loaded.
func (p *Proxy) eligible(nodeID, engine, model string) bool {
	r, seen := ResidencyUnknown, time.Time{}
	if p.capacity != nil {
		r, seen = p.capacity.Residency(nodeID, engine, model)
	}
	if at, ok := p.suppressedAt(inflightKey{engine, model, nodeID}); ok {
		if r != ResidencyLoaded || !seen.After(at) {
			return false
		}
	}
	if p.capacity == nil {
		return true
	}
	if r != ResidencyKnownEmpty {
		return true
	}
	// A JIT load would put a second large model on a node whose other engine
	// already holds one; the proxy has no sizes, so any loaded model counts.
	if o, ok := p.capacity.(otherEngineLoader); ok && o.OtherEngineLoaded(nodeID, engine) {
		return false
	}
	if !p.capacity.JITLoad(nodeID, engine) {
		return false
	}
	// A JIT load already dispatched here for another engine or model would
	// make this a second one. The claim is in-process only: concurrent cold
	// requests through other proxies are out of scope (runbook covers them).
	return !p.jitClaimedByOther(nodeID, engine, model)
}

type jitClaim struct{ engine, model string }

// jitClaimedByOther reports whether a live JIT claim on the node is for a
// different engine or model.
func (p *Proxy) jitClaimedByOther(nodeID, engine, model string) bool {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()
	for c, n := range p.jitClaims[nodeID] {
		if n > 0 && (c.engine != engine || modelKey(c.engine, c.model) != modelKey(engine, model)) {
			return true
		}
	}
	return false
}

// claimJIT records a dispatch to a KnownEmpty+JIT node and reports whether it
// did, so the matching release is exact. Released when the request ends.
func (p *Proxy) claimJIT(nodeID, engine, model string) bool {
	if p.capacity == nil {
		return false
	}
	if r, _ := p.capacity.Residency(nodeID, engine, model); r != ResidencyKnownEmpty || !p.capacity.JITLoad(nodeID, engine) {
		return false
	}
	p.claimMu.Lock()
	defer p.claimMu.Unlock()
	if p.jitClaims == nil {
		p.jitClaims = map[string]map[jitClaim]int{}
	}
	if p.jitClaims[nodeID] == nil {
		p.jitClaims[nodeID] = map[jitClaim]int{}
	}
	p.jitClaims[nodeID][jitClaim{engine, model}]++
	return true
}

func (p *Proxy) releaseJIT(nodeID, engine, model string) {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()
	c := jitClaim{engine, model}
	if p.jitClaims[nodeID][c] <= 1 {
		delete(p.jitClaims[nodeID], c)
		if len(p.jitClaims[nodeID]) == 0 {
			delete(p.jitClaims, nodeID)
		}
	} else {
		p.jitClaims[nodeID][c]--
	}
}

// otherEngineLoader is the optional capability of a provider that knows what
// the node's other engines have loaded.
type otherEngineLoader interface {
	OtherEngineLoaded(nodeID, engine string) bool
}

// suppressTTL is how long a not-loaded refusal keeps a node out of routing.
const suppressTTL = 60 * time.Second

func (p *Proxy) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// suppress records that node refused model as not loaded.
func (p *Proxy) suppress(k inflightKey) {
	p.suppressMu.Lock()
	defer p.suppressMu.Unlock()
	if p.suppressed == nil {
		p.suppressed = make(map[inflightKey]time.Time)
	}
	p.suppressed[k] = p.clock()
}

// suppressedAt returns when a live suppression began.
func (p *Proxy) suppressedAt(k inflightKey) (time.Time, bool) {
	p.suppressMu.Lock()
	defer p.suppressMu.Unlock()
	at, ok := p.suppressed[k]
	if ok && p.clock().Sub(at) >= suppressTTL {
		delete(p.suppressed, k)
		return time.Time{}, false
	}
	return at, ok
}

// minContextLength is the smallest per-slot context over every node that
// listed the model and is eligible for it (Loaded, Unknown or JIT). It is
// false when no node qualifies or when any eligible node has no value: a
// client that needs a safe window must not be handed an optimistic one.
func (p *Proxy) minContextLength(engine, model string, nodeIDs []string) (int, bool) {
	if p.capacity == nil {
		return 0, false
	}
	min, any := 0, false
	for _, id := range nodeIDs {
		if !p.eligible(id, engine, model) {
			continue
		}
		n, ok := p.capacity.CtxPerSlot(id, engine, model)
		if !ok || n <= 0 {
			return 0, false
		}
		if !any || n < min {
			min, any = n, true
		}
	}
	return min, any
}

// withContextLength sets context_length on a model record to the fleet value,
// replacing any upstream-supplied one; n <= 0 removes the field instead. A
// record that is not a JSON object is returned unchanged.
func withContextLength(raw json.RawMessage, n int) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw
	}
	if n > 0 {
		obj["context_length"] = json.RawMessage(strconv.Itoa(n))
	} else {
		delete(obj, "context_length")
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}
