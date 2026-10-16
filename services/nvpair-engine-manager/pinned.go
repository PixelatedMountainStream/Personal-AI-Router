// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// pinned.go is the pinned model load: load one named model on one named engine
// of one node, with optional load-time parameters. Entry points are the
// loopback POST /v1/models/load on the model HTTP port, the
// engine:remote-load-model RPC, and (peer side) the ec route
// POST /v1/models/pinned-load.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// controlPinnedLoadPath is deliberately a NEW ec route: an old peer 404s it,
// whereas reusing /v1/models/load would let an old peer silently ignore the
// load parameters.
const controlPinnedLoadPath = "/v1/models/pinned-load"

// PinnedRequest is the body of every pinned-load entry point. Node is consumed
// by the caller (the loopback route / RPC) and ignored on the ec peer route.
type PinnedRequest struct {
	Node   string `json:"node,omitempty"`
	Engine string `json:"engine"`
	Model  string `json:"model"`
	LoadOpts
	Evict bool `json:"evict,omitempty"`
}

// Victim is a resident model a pinned load would (or did) unload.
type Victim struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	Bytes  int64  `json:"bytes,omitempty"`
}

// PinnedResult is the response of a successful pinned load.
type PinnedResult struct {
	Node          string   `json:"node,omitempty"`
	Engine        string   `json:"engine"`
	Model         string   `json:"model"`
	Applied       LoadOpts `json:"applied"`
	Unloaded      []Victim `json:"unloaded"`
	AlreadyLoaded bool     `json:"already_loaded"`
	Warnings      []string `json:"warnings,omitempty"`
}

// pinnedError carries the HTTP status a pinned-load failure maps to: 400 bad or
// unsupported param, 404 unknown node, 409 conflict (Victims lists what would
// be unloaded), 502 engine failure, 504 unload-confirm timeout.
type pinnedError struct {
	Status  int      `json:"-"`
	Message string   `json:"error"`
	Victims []Victim `json:"victims,omitempty"`
}

func (e *pinnedError) Error() string { return e.Message }

func badParam(msg string) error { return &pinnedError{Status: http.StatusBadRequest, Message: msg} }

// pinnedStatus maps any error to its HTTP status (500 when untyped).
func pinnedStatus(err error) int {
	var pe *pinnedError
	if errors.As(err, &pe) {
		return pe.Status
	}
	return http.StatusInternalServerError
}

// writePinnedError renders err as {"error":..., "victims":[...]} with its status.
func writePinnedError(w http.ResponseWriter, err error) {
	pe := &pinnedError{Status: pinnedStatus(err), Message: err.Error()}
	var typed *pinnedError
	if errors.As(err, &typed) {
		pe = typed
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pe.Status)
	_ = json.NewEncoder(w).Encode(pe)
}

// PinnedLoad validates the request and loads the model.
func (e *Executor) PinnedLoad(ctx context.Context, req PinnedRequest) (PinnedResult, error) {
	if req.Engine == "" || req.Model == "" {
		return PinnedResult{}, badParam(`"engine" and "model" are required`)
	}
	if _, ok := e.reg.Get(req.Engine); !ok {
		return PinnedResult{}, badParam(fmt.Sprintf("unknown engine %q", req.Engine))
	}
	// Reject unsupported params before anything else happens.
	if _, _, err := modelLoadWire(req.Engine, req.Model, req.LoadOpts); err != nil {
		return PinnedResult{}, err
	}
	if _, err := e.ModelLoadWith(ctx, req.Engine, req.Model, req.LoadOpts); err != nil {
		if pinnedStatus(err) != http.StatusInternalServerError {
			return PinnedResult{}, err
		}
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	return PinnedResult{Engine: req.Engine, Model: req.Model, Applied: req.LoadOpts, Unloaded: []Victim{}}, nil
}

// handlePinnedLoad is the ec peer route.
func (s *controlServer) handlePinnedLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PinnedRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxControlBody)).Decode(&req); err != nil {
		writePinnedError(w, badParam("invalid JSON body"))
		return
	}
	res, err := s.exec.PinnedLoad(r.Context(), req)
	if err != nil {
		writePinnedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// pinnedLoader loads per a request on the named node ("self" = this node).
type pinnedLoader func(ctx context.Context, node string, req PinnedRequest) (PinnedResult, error)

// pinnedLoadHandler is the loopback route POST /v1/models/load on the model
// HTTP port; the caller wraps it in loopbackOnly.
func pinnedLoadHandler(load pinnedLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req PinnedRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, maxControlBody)).Decode(&req); err != nil {
			writePinnedError(w, badParam("invalid JSON body"))
			return
		}
		if req.Node == "" {
			writePinnedError(w, badParam(`"node" is required (a host UUID or "self")`))
			return
		}
		res, err := load(r.Context(), req.Node, req)
		if err != nil {
			writePinnedError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// pinnedLoad routes a pinned load to this node's executor or to a peer's ec
// surface.
func (m *Manager) pinnedLoad(ctx context.Context, node string, req PinnedRequest) (PinnedResult, error) {
	if node == "self" {
		res, err := m.exec.PinnedLoad(ctx, req)
		res.Node = "self"
		return res, err
	}
	peer, ok := m.peers.lookup(node)
	if !ok {
		return PinnedResult{}, &pinnedError{Status: http.StatusNotFound,
			Message: fmt.Sprintf("node %q is not a discovered ec peer; known: %s", node, m.peers.describe())}
	}
	client, err := m.remoteClient(ctx, peer)
	if err != nil {
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	res, err := client.pinnedLoad(ctx, req)
	res.Node = node
	return res, err
}

// pinnedLoad POSTs to the peer's pinned-load route. A 404 means the peer
// predates the route; its error body is otherwise a pinnedError.
func (c *remoteClient) pinnedLoad(ctx context.Context, req PinnedRequest) (PinnedResult, error) {
	req.Node = ""
	data, status, err := c.post(ctx, controlPinnedLoadPath, req.Engine, req)
	if err != nil {
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	switch {
	case status == http.StatusNotFound:
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway,
			Message: "peer too old: it has no pinned-load route; upgrade its engine-manager"}
	case status < 200 || status >= 300:
		pe := &pinnedError{Status: status}
		if json.Unmarshal(data, pe) != nil || pe.Message == "" {
			pe.Message = strings.TrimSpace(string(data))
		}
		return PinnedResult{}, pe
	}
	var res PinnedResult
	if err := json.Unmarshal(data, &res); err != nil {
		return PinnedResult{}, &pinnedError{Status: http.StatusBadGateway, Message: "peer returned an invalid response"}
	}
	return res, nil
}
