// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Node-settings key prefixes for per-node engine capacity overrides. Keys are
// flat: "engine_slots.<engine>.<model>", "engine_ctx_per_slot.<engine>.<model>"
// and "jit_load.<engine>". The model is everything after the engine, so it may
// itself contain dots.
// SettingsFileKey is the object in node-settings' settings.json holding the flat
// override keys below.
const SettingsFileKey = "engine_overrides"

const (
	SettingEngineSlots      = "engine_slots."
	SettingEngineCtxPerSlot = "engine_ctx_per_slot."
	SettingJITLoad          = "jit_load."
)

// DefaultJITLoad is whether an engine loads a missing model on demand when no
// jit_load.<engine> setting exists: true for Ollama and llama.cpp (router mode),
// false for LM Studio and any unknown engine.
func DefaultJITLoad(engine string) bool {
	switch engine {
	case "ollama", "llamacpp":
		return true
	}
	return false
}

// ApplyCapacityOverrides merges the flat node-settings keys in raw onto the
// slot capacity an engine reported and returns the merged map plus the resolved
// per-engine jit_load for the given engines. An override wins over the reported
// value but only applies to a model the engine reports loaded (loaded maps
// engine -> loaded models), so an override never fabricates residency. Values
// that are not positive integers (slots, ctx) or booleans (jit) are ignored.
// slots is not mutated.
func ApplyCapacityOverrides(raw map[string]json.RawMessage, engines []string, loaded map[string][]string, slots map[string]map[string]SlotInfo) (map[string]map[string]SlotInfo, map[string]bool) {
	out := make(map[string]map[string]SlotInfo, len(slots))
	for e, m := range slots {
		c := make(map[string]SlotInfo, len(m))
		for k, v := range m {
			c[k] = v
		}
		out[e] = c
	}
	isLoaded := func(engine, model string) bool {
		for _, m := range loaded[engine] {
			if m == model {
				return true
			}
		}
		return false
	}
	for k, v := range raw {
		for _, prefix := range []string{SettingEngineSlots, SettingEngineCtxPerSlot} {
			rest, ok := strings.CutPrefix(k, prefix)
			if !ok {
				continue
			}
			engine, model, ok := strings.Cut(rest, ".")
			n, err := strconv.Atoi(strings.Trim(string(v), `"`))
			if !ok || engine == "" || model == "" || err != nil || n <= 0 || !isLoaded(engine, model) {
				continue
			}
			if out[engine] == nil {
				out[engine] = map[string]SlotInfo{}
			}
			si := out[engine][model]
			if prefix == SettingEngineSlots {
				si.Slots = n
			} else {
				si.CtxPerSlot = n
			}
			out[engine][model] = si
		}
	}
	// A ctx-only override without any slot count is not capacity.
	for e, m := range out {
		for k, si := range m {
			if si.Slots < 1 {
				delete(m, k)
			}
		}
		if len(m) == 0 {
			delete(out, e)
		}
	}
	if len(out) == 0 {
		out = nil
	}
	jit := make(map[string]bool, len(engines))
	for _, e := range engines {
		jit[e] = DefaultJITLoad(e)
		if v, ok := raw[SettingJITLoad+e]; ok {
			if b, err := strconv.ParseBool(strings.Trim(string(v), `"`)); err == nil {
				jit[e] = b
			}
		}
	}
	if len(jit) == 0 {
		jit = nil
	}
	return out, jit
}
