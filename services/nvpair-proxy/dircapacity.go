package main

import (
	"strings"
	"sync"
	"time"

	"nvpair-shared/engines"
	"nvpair-shared/noderec"
)

// dirEntry is what the proxy keeps of one directory node for one engine. The
// observation time is the node's own modelsObservedAt, which the scanner
// advances (with a 30 s heartbeat) on every successful sweep.
type dirEntry struct {
	node noderec.DirectoryNode
}

// directoryCapacity serves CapacityProvider from the latest directory
// snapshot each facade receives. Safe for concurrent use.
type directoryCapacity struct {
	now func() time.Time
	mu  sync.RWMutex
	by  map[string]map[string]dirEntry // engine -> nodeID -> entry
}

func newDirectoryCapacity(now func() time.Time) *directoryCapacity {
	if now == nil {
		now = time.Now
	}
	return &directoryCapacity{now: now, by: map[string]map[string]dirEntry{}}
}

// update replaces the engine's view with the snapshot (a departed node simply
// vanishes).
func (d *directoryCapacity) update(engine string, nodes []noderec.DirectoryNode) {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := make(map[string]dirEntry, len(nodes))
	for _, n := range nodes {
		next[n.HostUUID] = dirEntry{node: n}
	}
	d.by[engine] = next
}

// modelKey is the one place model names are compared for residency, slots and
// context: Ollama's `m` and `m:latest` are the same model.
func modelKey(engine, model string) string {
	model = strings.TrimSpace(model)
	if engine != engines.NameOllama || model == "" {
		return model
	}
	if name := model[strings.LastIndex(model, "/")+1:]; !strings.ContainsAny(name, ":@") {
		return model + ":latest"
	}
	return model
}

func slotInfo(n noderec.DirectoryNode, engine, model string) (noderec.SlotInfo, bool) {
	want := modelKey(engine, model)
	for m, s := range n.SlotsByEngine[engine] {
		if modelKey(engine, m) == want {
			return s, true
		}
	}
	return noderec.SlotInfo{}, false
}

func (d *directoryCapacity) entry(nodeID, engine string) (dirEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.by[engine][nodeID]
	return e, ok
}

func (d *directoryCapacity) Slots(nodeID, engine, model string) (int, bool) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return 0, false
	}
	s, ok := slotInfo(e.node, engine, model)
	return s.Slots, ok && s.Slots > 0
}

func (d *directoryCapacity) CtxPerSlot(nodeID, engine, model string) (int, bool) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return 0, false
	}
	s, ok := slotInfo(e.node, engine, model)
	return s.CtxPerSlot, ok && s.CtxPerSlot > 0
}

// Residency: engine key absent is Unknown; present without the model is
// KnownEmpty; present with it is Loaded.
func (d *directoryCapacity) Residency(nodeID, engine, model string) (Residency, time.Time) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return ResidencyUnknown, time.Time{}
	}
	loaded, present := e.node.LoadedByEngine[engine]
	if !present {
		return ResidencyUnknown, time.Time{}
	}
	want := modelKey(engine, model)
	var observed time.Time
	if ms := e.node.ModelsObservedAt; ms > 0 {
		observed = time.UnixMilli(ms)
	}
	for _, m := range loaded {
		if modelKey(engine, m) == want {
			return ResidencyLoaded, observed
		}
	}
	return ResidencyKnownEmpty, observed
}

func (d *directoryCapacity) JITLoad(nodeID, engine string) bool {
	if e, ok := d.entry(nodeID, engine); ok {
		if v, ok := e.node.JITLoadByEngine[engine]; ok {
			return v
		}
	}
	return engine != engines.NameLMStudio
}

// overrideCapacity layers explicit node-settings overrides (env) over a base
// provider: an override wins where it has a value, residency is always the base's.
type overrideCapacity struct {
	over settingsCapacity
	base CapacityProvider
}

func (o overrideCapacity) Slots(n, e, m string) (int, bool) {
	if v, ok := o.over.Slots(n, e, m); ok {
		return v, true
	}
	return o.base.Slots(n, e, m)
}

func (o overrideCapacity) CtxPerSlot(n, e, m string) (int, bool) {
	if v, ok := o.over.CtxPerSlot(n, e, m); ok {
		return v, true
	}
	return o.base.CtxPerSlot(n, e, m)
}

func (o overrideCapacity) Residency(n, e, m string) (Residency, time.Time) {
	return o.base.Residency(n, e, m)
}

func (o overrideCapacity) JITLoad(n, e string) bool {
	if v, ok := o.over.jit[e]; ok {
		return v
	}
	return o.base.JITLoad(n, e)
}

// layerCapacity makes the directory the default and lets the env overrides
// (nil when unset) win where they carry a value.
func layerCapacity(env CapacityProvider, dir *directoryCapacity) CapacityProvider {
	if s, ok := env.(settingsCapacity); ok {
		return overrideCapacity{over: s, base: dir}
	}
	return dir
}

// OtherEngineLoaded reports whether any engine on the node other than engine
// has a loaded model, per the directory's loadedByEngine.
func (d *directoryCapacity) OtherEngineLoaded(nodeID, engine string) bool {
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

func (o overrideCapacity) OtherEngineLoaded(n, e string) bool {
	x, ok := o.base.(otherEngineLoader)
	return ok && x.OtherEngineLoaded(n, e)
}
