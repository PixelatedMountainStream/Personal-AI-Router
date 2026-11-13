// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package capacity

// Package capacity is the fork's slot-capacity and residency source for the
// proxy: node-settings overrides (env), the discovery directory, and their
// layering. Moved from nvpair-proxy capacity.go/dircapacity.go in catch-up step 5.

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"nvpair-shared/engines"
	"nvpair-shared/noderec"
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

// Provider is where routing learns a node's capacity and residency.
// Today the node-settings overrides below implement it; the directory-backed
// implementation replaces them without touching routing.
type Provider interface {
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

// Env names the environment variable carrying node-settings overrides
// as one flat JSON object, e.g. {"engine_slots.lmstudio.qwen3.8-27b-mlx": 4}.
const Env = "NVPAIR_PROXY_CAPACITY"

// Settings serves slot counts from the flat node-settings keys
// `engine_slots.<engine>.<model>`. The same value applies to every node.
type Settings struct {
	SlotsBy map[string]int  // "engine/model" -> slots
	CtxBy   map[string]int  // "engine/model" -> context per slot
	JITBy   map[string]bool // engine -> jit_load override
}

func (c Settings) Slots(_, engine, model string) (int, bool) {
	n, ok := c.SlotsBy[engine+"/"+model]
	return n, ok && n > 0
}

func (c Settings) CtxPerSlot(_, engine, model string) (int, bool) {
	n, ok := c.CtxBy[engine+"/"+model]
	return n, ok && n > 0
}

// Residency is always Unknown here: settings carry no loaded set.
func (Settings) Residency(_, _, _ string) (Residency, time.Time) {
	return ResidencyUnknown, time.Time{}
}

// JITLoad defaults to noderec.DefaultJITLoad (vLLM and LM Studio false).
func (c Settings) JITLoad(_, engine string) bool {
	if v, ok := c.JITBy[engine]; ok {
		return v
	}
	return noderec.DefaultJITLoad(engine)
}

// ParseSettings reads the flat settings object. The model is
// everything after the engine, so it may itself contain dots.
func ParseSettings(raw map[string]json.RawMessage) Settings {
	c := Settings{SlotsBy: map[string]int{}, CtxBy: map[string]int{}, JITBy: map[string]bool{}}
	for k, v := range raw {
		val := strings.Trim(string(v), `"`)
		if engine, ok := strings.CutPrefix(k, "jit_load."); ok {
			if b, err := strconv.ParseBool(val); err == nil && engine != "" {
				c.JITBy[engine] = b
			}
			continue
		}
		dst := c.SlotsBy
		rest, ok := strings.CutPrefix(k, "engine_slots.")
		if !ok {
			if rest, ok = strings.CutPrefix(k, "engine_ctx_per_slot."); !ok {
				continue
			}
			dst = c.CtxBy
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

func FromEnv() Provider {
	s := os.Getenv(Env)
	if s == "" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		slog.Warn("ignoring malformed capacity overrides", "env", Env, "err", err)
		return nil
	}
	return ParseSettings(raw)
}

// OtherEngineLoader is the optional capability of a provider that knows what
// the node's other engines have loaded.
type OtherEngineLoader interface {
	OtherEngineLoaded(nodeID, engine string) bool
}

// WithContextLength sets context_length on a model record to the fleet value,
// replacing any upstream-supplied one; n <= 0 removes the field instead. A
// record that is not a JSON object is returned unchanged.
func WithContextLength(raw json.RawMessage, n int) json.RawMessage {
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

// entryRec is what the proxy keeps of one directory node for one engine. The
// observation time is the node's own modelsObservedAt, which the scanner
// advances (with a 30 s heartbeat) on every successful sweep.
type entryRec struct {
	node noderec.DirectoryNode
}

// Directory serves Provider from the latest directory
// snapshot each facade receives. Safe for concurrent use.
type Directory struct {
	now func() time.Time
	mu  sync.RWMutex
	by  map[string]map[string]entryRec // engine -> nodeID -> entry
}

func NewDirectory(now func() time.Time) *Directory {
	if now == nil {
		now = time.Now
	}
	return &Directory{now: now, by: map[string]map[string]entryRec{}}
}

// update replaces the engine's view with the snapshot (a departed node simply
// vanishes).
func (d *Directory) Update(engine string, nodes []noderec.DirectoryNode) {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := make(map[string]entryRec, len(nodes))
	for _, n := range nodes {
		next[n.HostUUID] = entryRec{node: n}
	}
	d.by[engine] = next
}

// modelKey is the one place model names are compared for residency, slots and
// context: Ollama's `m` and `m:latest` are the same model.
func ModelKey(engine, model string) string {
	model = strings.TrimSpace(model)
	if engine != engines.NameOllama || model == "" {
		return model
	}
	if name := model[strings.LastIndex(model, "/")+1:]; !strings.ContainsAny(name, ":@") {
		return model + ":latest"
	}
	return model
}

func SlotInfoFor(n noderec.DirectoryNode, engine, model string) (noderec.SlotInfo, bool) {
	want := ModelKey(engine, model)
	for m, s := range n.SlotsByEngine[engine] {
		if ModelKey(engine, m) == want {
			return s, true
		}
	}
	return noderec.SlotInfo{}, false
}

func (d *Directory) entry(nodeID, engine string) (entryRec, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.by[engine][nodeID]
	return e, ok
}

func (d *Directory) Slots(nodeID, engine, model string) (int, bool) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return 0, false
	}
	s, ok := SlotInfoFor(e.node, engine, model)
	return s.Slots, ok && s.Slots > 0
}

func (d *Directory) CtxPerSlot(nodeID, engine, model string) (int, bool) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return 0, false
	}
	s, ok := SlotInfoFor(e.node, engine, model)
	return s.CtxPerSlot, ok && s.CtxPerSlot > 0
}

// Residency: engine key absent is Unknown; present without the model is
// KnownEmpty; present with it is Loaded.
func (d *Directory) Residency(nodeID, engine, model string) (Residency, time.Time) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return ResidencyUnknown, time.Time{}
	}
	loaded, present := e.node.LoadedByEngine[engine]
	if !present {
		return ResidencyUnknown, time.Time{}
	}
	loaded = ServedLoaded(e.node, engine, loaded)
	want := ModelKey(engine, model)
	var observed time.Time
	if ms := e.node.ModelsObservedAt; ms > 0 {
		observed = time.UnixMilli(ms)
	}
	for _, m := range loaded {
		if ModelKey(engine, m) == want {
			return ResidencyLoaded, observed
		}
	}
	return ResidencyKnownEmpty, observed
}

func (d *Directory) JITLoad(nodeID, engine string) bool {
	if e, ok := d.entry(nodeID, engine); ok {
		if v, ok := e.node.JITLoadByEngine[engine]; ok {
			return v
		}
	}
	return noderec.DefaultJITLoad(engine)
}

// Override layers explicit node-settings overrides (env) over a base
// provider: an override wins where it has a value, residency is always the base's.
type Override struct {
	Over Settings
	Base Provider
}

func (o Override) Slots(n, e, m string) (int, bool) {
	if v, ok := o.Over.Slots(n, e, m); ok {
		return v, true
	}
	return o.Base.Slots(n, e, m)
}

func (o Override) CtxPerSlot(n, e, m string) (int, bool) {
	if v, ok := o.Over.CtxPerSlot(n, e, m); ok {
		return v, true
	}
	return o.Base.CtxPerSlot(n, e, m)
}

func (o Override) Residency(n, e, m string) (Residency, time.Time) {
	return o.Base.Residency(n, e, m)
}

func (o Override) JITLoad(n, e string) bool {
	if v, ok := o.Over.JITBy[e]; ok {
		return v
	}
	return o.Base.JITLoad(n, e)
}

// Layer makes the directory the default and lets the env overrides
// (nil when unset) win where they carry a value.
func Layer(env Provider, dir *Directory) Provider {
	if s, ok := env.(Settings); ok {
		return Override{Over: s, Base: dir}
	}
	return dir
}

// OtherEngineLoaded reports whether any engine on the node other than engine
// has a loaded model, per the directory's loadedByEngine.
func (d *Directory) OtherEngineLoaded(nodeID, engine string) bool {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return false
	}
	for eng, models := range e.node.LoadedByEngine {
		if eng != engine && len(models) > 0 {
			return true
		}
	}
	return false
}

func (o Override) OtherEngineLoaded(n, e string) bool {
	x, ok := o.Base.(OtherEngineLoader)
	return ok && x.OtherEngineLoaded(n, e)
}

// ServedLoaded is the loaded set directory residency compares a request's
// model with. A loaded-only engine (vLLM by name, or any engine the node gives a
// downloadedByEngine entry; the name alone covers a failed disk-inventory read,
// which omits that entry) names its loadedByEngine models by --model root (NVIDIA #181),
// but a request names the served model, which is what its modelsByEngine
// advertises; a stopped one advertises nothing, so nothing is loaded. Any
// other engine keeps loadedByEngine.
func ServedLoaded(n noderec.DirectoryNode, engine string, loaded []string) []string {
	if _, loadedOnly := n.DownloadedByEngine[engine]; loadedOnly || engine == engines.NameVLLM {
		return n.ModelsByEngine[engine]
	}
	return loaded
}
