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
	return r != ResidencyKnownEmpty || p.capacity.JITLoad(nodeID, engine)
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
