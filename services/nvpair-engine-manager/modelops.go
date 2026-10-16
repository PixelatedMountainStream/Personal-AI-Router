// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// modelActionRequest is the shared body for ec model load/unload/delete endpoints.
type modelActionRequest struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
}

// ModelLoad warms a model into engine memory using each engine's manifest action.
func (e *Executor) ModelLoad(ctx context.Context, engine, model string) (json.RawMessage, error) {
	return e.ModelLoadWith(ctx, engine, model, LoadOpts{})
}

// LoadOpts are the optional load-time parameters of a pinned load. Zero means
// "engine default" and is never sent.
type LoadOpts struct {
	ContextLength int `json:"context_length,omitempty"`
	Parallel      int `json:"parallel,omitempty"`
}

// ModelLoadWith loads a model with explicit load-time parameters. An engine
// that cannot honor a requested parameter fails with a 400 *pinnedError rather
// than silently ignoring it.
func (e *Executor) ModelLoadWith(ctx context.Context, engine, model string, o LoadOpts) (json.RawMessage, error) {
	action, params, err := modelLoadWire(engine, model, o)
	if err != nil {
		return nil, err
	}
	return e.Action(ctx, engine, action, params)
}

// modelLoadWire builds the load action and params for the engine.
//   - ollama: context_length is sent as options.num_ctx; parallel other than 1
//     is refused (Ollama sizes parallelism server-side).
//   - lmstudio: both go to the load action's optional_args (lms load flags).
//   - llamacpp and any other engine: context and parallel are fixed at launch,
//     so requesting them is refused.
func modelLoadWire(engine, model string, o LoadOpts) (string, json.RawMessage, error) {
	if o.ContextLength < 0 || o.Parallel < 0 {
		return "", nil, badParam("context_length and parallel must be non-negative")
	}
	if o == (LoadOpts{}) {
		return modelActionWire(engine, "load", model)
	}
	if model == "" {
		return "", nil, fmt.Errorf("model is required")
	}
	switch engine {
	case "ollama":
		if o.Parallel > 1 {
			return "", nil, badParam("ollama does not support a per-load parallel setting")
		}
		body := map[string]any{"model": model, "stream": false}
		if o.ContextLength > 0 {
			body["options"] = map[string]int{"num_ctx": o.ContextLength}
		}
		return marshalModelAction("run_model", body)
	case "lmstudio":
		body := map[string]any{"model": model}
		if o.ContextLength > 0 {
			body["context_length"] = o.ContextLength
		}
		if o.Parallel > 0 {
			body["parallel"] = o.Parallel
		}
		return marshalModelAction("load_model", body)
	default:
		return "", nil, badParam(fmt.Sprintf("engine %q fixes context_length and parallel at launch; omit them", engine))
	}
}

// ModelUnload frees a model from engine memory.
func (e *Executor) ModelUnload(ctx context.Context, engine, model string) (json.RawMessage, error) {
	action, params, err := modelActionWire(engine, "unload", model)
	if err != nil {
		return nil, err
	}
	return e.Action(ctx, engine, action, params)
}

// ModelDelete removes a downloaded model from disk when the manifest exposes it.
func (e *Executor) ModelDelete(ctx context.Context, engine, model string) (json.RawMessage, error) {
	action, params, err := modelActionWire(engine, "delete", model)
	if err != nil {
		return nil, err
	}
	return e.Action(ctx, engine, action, params)
}

func modelActionWire(engine, op, model string) (string, json.RawMessage, error) {
	if model == "" {
		return "", nil, fmt.Errorf("model is required")
	}
	switch op {
	case "load":
		switch engine {
		case "ollama":
			return marshalModelAction("run_model", map[string]any{"model": model, "stream": false})
		default:
			return marshalModelAction("load_model", map[string]string{"model": model})
		}
	case "unload":
		switch engine {
		case "ollama":
			return marshalModelAction("unload_model", map[string]any{"model": model, "keep_alive": 0})
		default:
			return marshalModelAction("unload_model", map[string]string{"model": model})
		}
	case "delete":
		switch engine {
		case "ollama":
			return marshalModelAction("delete_model", map[string]string{"name": model})
		default:
			return marshalModelAction("delete_model", map[string]string{"model": model})
		}
	default:
		return "", nil, fmt.Errorf("unknown model op %q", op)
	}
}

func marshalModelAction(action string, params any) (string, json.RawMessage, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return "", nil, err
	}
	return action, b, nil
}
