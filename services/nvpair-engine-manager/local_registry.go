// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// The fork's manifest extensions (seam E10). Each is embedded in the upstream
// struct it extends, so encoding/json flattens it and manifest keys are
// unchanged; the zero value is an upstream manifest.

// LocalActionExt extends Action.
type LocalActionExt struct {
	// OptionalArgs maps a caller param name to an argv fragment appended to Cmd
	// only when that param is present, e.g. {"context_length": ["--context-length",
	// "{context_length}"]}. The param value must be a non-negative integer and
	// replaces {<param>} in the fragment. Cmd actions only.
	OptionalArgs map[string][]string `json:"optional_args,omitempty"`
}

// LocalResultExt extends ActionResult.
type LocalResultExt struct {
	// Record, when set, additionally extracts typed per-model capacity (see
	// ResultRecord). With Array/Field set, every array row yields one record
	// keyed by its Field. With both empty, the whole response is ONE record
	// (a per-model query such as llama.cpp /props?model=).
	Record *ResultRecord `json:"record,omitempty"`
	// Each, when set, names a per-row array of instances (LM Studio's
	// "loaded_instances"). Each row is expanded to one row per instance, named
	// by that instance's EachField (default "id"), so residency, sizes and
	// slots are per loaded instance rather than per model key. Rows with an
	// empty array stay as they are.
	Each      string `json:"each,omitempty"`
	EachField string `json:"each_field,omitempty"`
}

// LocalMatchExt extends ResultMatch.
type LocalMatchExt struct {
	// LoadingIn lists string values that are not matched (not "loaded") but still
	// occupy memory; strict residency counts them as occupied.
	LoadingIn []string `json:"loading_in,omitempty"`
}

// ResultRecord declares how to build a SlotInfo. Fields maps "slots", "ctx" and
// "bytes" (resident size, used by model_sizes)
// to dot-separated paths, where a numeric part indexes an array (LM Studio's
// "loaded_instances.0.config.parallel"). Static supplies "slots" and "ctx"
// constants for engines that expose no endpoint (Ollama serves 1 slot).
type ResultRecord struct {
	Fields map[string]string `json:"fields,omitempty"`
	Static map[string]int    `json:"static,omitempty"`
}

// declares reports whether the record maps key by path or by static value.
func (r *ResultRecord) declares(key string) bool {
	if _, ok := r.Fields[key]; ok {
		return true
	}
	_, ok := r.Static[key]
	return ok
}

var optionalArgKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var recordPathRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*$`)

// localValidate checks the fork's action extensions (seam E10). It runs in
// Action.validate just before upstream's result.array/result.field check,
// which a result with a Record skips: a record result may leave both empty.
// A zero extension is valid, so upstream manifests validate as before.
func (a *Action) localValidate(name string) error {
	if a == nil {
		return nil
	}
	if len(a.OptionalArgs) > 0 {
		if len(a.Cmd) == 0 {
			return fmt.Errorf("action %q: optional_args requires a cmd action", name)
		}
		for k, frag := range a.OptionalArgs {
			if !optionalArgKeyRe.MatchString(k) || allowedPlaceholders[k] || k == "model" {
				return fmt.Errorf("action %q: optional_args key %q is invalid or reserved", name, k)
			}
			if len(frag) == 0 {
				return fmt.Errorf("action %q: optional_args %q has an empty fragment", name, k)
			}
		}
	}
	r := a.Result
	if r == nil || r.Record == nil {
		return nil
	}
	if (strings.TrimSpace(r.Array) == "") != (strings.TrimSpace(r.Field) == "") {
		return fmt.Errorf("action %q: result.array and result.field must both be set or both empty with result.record", name)
	}
	for k, path := range r.Record.Fields {
		if (k != "slots" && k != "ctx" && k != "bytes") || !recordPathRe.MatchString(path) {
			return fmt.Errorf("action %q: result.record.fields %q=%q invalid (keys slots|ctx|bytes, dot path)", name, k, path)
		}
	}
	for k := range r.Record.Static {
		if k != "slots" && k != "ctx" && k != "bytes" {
			return fmt.Errorf("action %q: result.record.static key %q invalid (want slots, ctx or bytes)", name, k)
		}
	}
	if !r.Record.declares("slots") && !r.Record.declares("bytes") {
		return fmt.Errorf("action %q: result.record requires a slots or bytes field or static value", name)
	}
	return nil
}

// localBundledActions are fork-owned actions overlaid onto the bundled upstream
// manifests so the manifests stay upstream-shaped. catalog_sizes lists every
// downloaded model's size in bytes (no loaded filter), letting a pinned load size
// a cold target before choosing victims.
var localBundledActions = map[string]map[string]json.RawMessage{
	"lmstudio": {"catalog_sizes": json.RawMessage(`{"description":"Size in bytes of every downloaded model (/api/v1/models models[].size_bytes).","http":{"method":"GET","path":"/api/v1/models"},"result":{"array":"models","field":"key","record":{"fields":{"bytes":"size_bytes"}}}}`)},
	"ollama":   {"catalog_sizes": json.RawMessage(`{"description":"Size in bytes of every downloaded model (/api/tags models[].size).","http":{"method":"GET","path":"/api/tags"},"result":{"array":"models","field":"name","record":{"fields":{"bytes":"size"}}}}`)},
}

// overlayLocal merges localBundledActions into one bundled engine's raw JSON (so
// a per-user override still deep-merges onto them) and reloads that manifest. A
// failure is logged and leaves the upstream manifest as loaded.
func (r *Registry) overlayLocal(eng string) {
	acts, ok := localBundledActions[eng]
	if !ok {
		return
	}
	var doc map[string]json.RawMessage
	cur := map[string]json.RawMessage{}
	if json.Unmarshal(r.bundledRaw[eng], &doc) != nil || (doc["actions"] != nil && json.Unmarshal(doc["actions"], &cur) != nil) {
		slog.Warn("local manifest overlay skipped", "engine", eng)
		return
	}
	for k, v := range acts {
		if _, exists := cur[k]; !exists {
			cur[k] = v
		}
	}
	doc["actions"], _ = json.Marshal(cur)
	out, _ := json.Marshal(doc)
	if _, err := r.addManifest("local overlay "+eng, out); err != nil {
		slog.Warn("local manifest overlay rejected", "engine", eng, "err", err)
		return
	}
	r.bundledRaw[eng] = out
}
