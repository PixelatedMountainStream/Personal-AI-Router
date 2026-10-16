package main

import (
	"sync"
	"time"

	"nvpair-shared/engines"
	"nvpair-shared/noderec"
)

// dirEntry is what the proxy keeps of one directory node for one engine.
type dirEntry struct {
	node noderec.DirectoryNode
	// observedAt is when this proxy first saw the node's current inventory
	// (see update); it is what suppression lifting compares against.
	observedAt time.Time
	srcStamp   int64 // node.ModelsObservedAt the stamp belongs to
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
// vanishes). The observed-at stamp is the time this proxy received the update,
// not the node's modelsObservedAt: that is a sender clock, and suppression is
// stamped on the proxy's own clock, so the two cannot be compared. The stamp is
// refreshed only when modelsObservedAt differs from the previous snapshot's (or
// the node is new). The node advances it on every successful sweep (with a
// heartbeat), so a live node keeps a fresh stamp while a re-sent snapshot for
// an unrelated change keeps the old one, and stale "Loaded" data cannot lift a
// fresh not-loaded suppression.
func (d *directoryCapacity) update(engine string, nodes []noderec.DirectoryNode) {
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	prev := d.by[engine]
	next := make(map[string]dirEntry, len(nodes))
	for _, n := range nodes {
		e := dirEntry{node: n, observedAt: now, srcStamp: n.ModelsObservedAt}
		if old, ok := prev[n.HostUUID]; ok && old.srcStamp == n.ModelsObservedAt {
			e.observedAt = old.observedAt
		}
		next[n.HostUUID] = e
	}
	d.by[engine] = next
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
	s, ok := e.node.SlotsByEngine[engine][model]
	return s.Slots, ok && s.Slots > 0
}

func (d *directoryCapacity) CtxPerSlot(nodeID, engine, model string) (int, bool) {
	e, ok := d.entry(nodeID, engine)
	if !ok {
		return 0, false
	}
	s, ok := e.node.SlotsByEngine[engine][model]
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
	for _, m := range loaded {
		if m == model {
			return ResidencyLoaded, e.observedAt
		}
	}
	return ResidencyKnownEmpty, e.observedAt
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
