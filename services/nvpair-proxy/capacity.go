package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// CapacityProvider answers how many requests a node's engine runs at once for
// a model. It is the seam between routing and wherever that fact comes from:
// today the node-settings overrides below, later the node directory.
//
// ok is false when nothing is known; routing then assumes a single slot.
type CapacityProvider interface {
	Slots(nodeID, engine, model string) (slots int, ok bool)
}

// capacityEnv names the environment variable carrying node-settings overrides
// as one flat JSON object, e.g. {"engine_slots.lmstudio.qwen3.8-27b-mlx": 4}.
const capacityEnv = "NVPAIR_PROXY_CAPACITY"

// settingsCapacity serves slot counts from the flat node-settings keys
// `engine_slots.<engine>.<model>`. The same value applies to every node.
type settingsCapacity struct {
	slots map[string]int // "engine/model" -> slots
}

func (c settingsCapacity) Slots(_, engine, model string) (int, bool) {
	n, ok := c.slots[engine+"/"+model]
	return n, ok && n > 0
}

// parseCapacitySettings reads the flat settings object. The model is
// everything after the engine, so it may itself contain dots.
func parseCapacitySettings(raw map[string]json.RawMessage) settingsCapacity {
	c := settingsCapacity{slots: map[string]int{}}
	for k, v := range raw {
		rest, ok := strings.CutPrefix(k, "engine_slots.")
		if !ok {
			continue
		}
		engine, model, ok := strings.Cut(rest, ".")
		if !ok || engine == "" || model == "" {
			continue
		}
		if n, err := strconv.Atoi(strings.Trim(string(v), `"`)); err == nil && n > 0 {
			c.slots[engine+"/"+model] = n
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
