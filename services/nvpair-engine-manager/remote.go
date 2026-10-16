// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// remote.go implements the engine:remote-* methods: the local engine-manager
// acting as a client that drives a peer's ec surface on behalf of a UI. The
// broker relays engine:* verbatim, so these arrive here directly. Each resolves
// the target node in the ec peer directory, dials it over pinned mTLS, and (for
// the streaming ops) relays progress up as engine:remote-progress before
// settling the request with the terminal result.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// remoteParam is the shared input for the engine:remote-* methods. Fields not
// relevant to a given method are ignored.
type remoteParam struct {
	Node   string          `json:"node"`
	Engine string          `json:"engine,omitempty"`
	Start  bool            `json:"start,omitempty"`
	Port   int             `json:"port,omitempty"`
	Model  string          `json:"model,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	// Pinned-load parameters (engine:remote-load-model).
	LoadOpts
	Evict bool `json:"evict,omitempty"`
}

// remoteProgress is the engine:remote-progress notification payload the broker
// forwards to a subscribed UI.
type remoteProgress struct {
	OpID    string `json:"opId"`
	Node    string `json:"node"`
	Engine  string `json:"engine,omitempty"`
	Op      string `json:"op,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Percent int    `json:"percent,omitempty"`
	Message string `json:"message,omitempty"`
}

// newOpID mints a random correlation id for a remote operation.
func newOpID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// runRemote dispatches an engine:remote-* request. It runs on its own goroutine
// (like the other long ops) so the read loop stays responsive during a
// multi-minute remote install/pull.
func (m *Manager) runRemote(ctx context.Context, msg *Message) {
	var p remoteParam
	if !m.parse(msg, &p) {
		return
	}
	if p.Node == "" {
		m.codec.RespondError(msg.ID, -32602, "node is required")
		return
	}
	if msg.Method == "engine:remote-load-model" {
		m.runRemotePinnedLoad(ctx, msg, p)
		return
	}
	peer, ok := m.peers.lookup(p.Node)
	if !ok {
		m.codec.RespondError(msg.ID, -32000, "node "+p.Node+" is not a discovered ec peer")
		return
	}
	client, err := m.remoteClient(ctx, peer)
	if err != nil {
		m.codec.RespondError(msg.ID, -32000, err.Error())
		return
	}

	switch msg.Method {
	case "engine:remote-get-installed":
		res, err := client.getEngines(ctx)
		m.respondOrErr(msg, res, err)

	case "engine:remote-install":
		if p.Engine == "" {
			m.codec.RespondError(msg.ID, -32602, "engine is required")
			return
		}
		opID := newOpID()
		body := installRequest{OpID: opID, Engine: p.Engine, Start: p.Start}
		terminal, err := client.stream(ctx, controlInstallPath, body, m.remoteProgressFn(opID, peer.nodeID))
		if err != nil {
			m.codec.RespondError(msg.ID, -32000, err.Error())
			return
		}
		m.codec.Respond(msg.ID, map[string]any{"opId": opID, "status": terminal.Status})

	case "engine:remote-pull-model":
		if p.Engine == "" {
			m.codec.RespondError(msg.ID, -32602, "engine is required")
			return
		}
		if p.Model == "" && len(p.Params) == 0 {
			m.codec.RespondError(msg.ID, -32602, "model or params is required")
			return
		}
		opID := newOpID()
		body := pullRequest{OpID: opID, Engine: p.Engine, Model: p.Model, Params: p.Params}
		terminal, err := client.stream(ctx, controlPullPath, body, m.remoteProgressFn(opID, peer.nodeID))
		if err != nil {
			m.codec.RespondError(msg.ID, -32000, err.Error())
			return
		}
		m.codec.Respond(msg.ID, map[string]any{"opId": opID, "result": terminal.Result})

	case "engine:remote-unload-model", "engine:remote-delete-model":
		if p.Engine == "" {
			m.codec.RespondError(msg.ID, -32602, "engine is required")
			return
		}
		if p.Model == "" {
			m.codec.RespondError(msg.ID, -32602, "model is required")
			return
		}
		path := controlUnloadPath
		switch msg.Method {
		case "engine:remote-delete-model":
			path = controlDeletePath
		}
		res, err := client.postJSON(ctx, path, p.Engine, modelActionRequest{Engine: p.Engine, Model: p.Model})
		m.respondOrErr(msg, res, err)

	case "engine:remote-start":
		if p.Engine == "" {
			m.codec.RespondError(msg.ID, -32602, "engine is required")
			return
		}
		res, err := client.postJSON(ctx, controlStartPath, p.Engine, startRequest{Engine: p.Engine, Port: p.Port})
		m.respondOrErr(msg, res, err)

	case "engine:remote-stop":
		if p.Engine == "" {
			m.codec.RespondError(msg.ID, -32602, "engine is required")
			return
		}
		res, err := client.postJSON(ctx, controlStopPath, p.Engine, stopRequest{Engine: p.Engine})
		m.respondOrErr(msg, res, err)

	default:
		m.codec.RespondError(msg.ID, -32601, "method not found: "+msg.Method)
	}
}

// remoteProgressFn returns an onProgress callback that relays a peer's stream
// frames up as engine:remote-progress notifications, stamped with our opId and
// the target node id.
func (m *Manager) remoteProgressFn(opID, node string) func(streamFrame) {
	return func(f streamFrame) {
		p := remoteProgress{
			OpID: opID, Node: node, Engine: f.Engine, Op: f.Op,
			Stage: f.Stage, Message: f.Message,
		}
		if wirePercentIncluded(f.Percent) {
			p.Percent = f.Percent
		}
		_ = m.codec.Notify("engine:remote-progress", p)
	}
}

// runRemotePinnedLoad serves engine:remote-load-model as a pinned load on the
// target node ("self" = this node). Params: {node, engine, model,
// context_length?, parallel?, evict?}; result: PinnedResult.
func (m *Manager) runRemotePinnedLoad(ctx context.Context, msg *Message, p remoteParam) {
	res, err := m.pinnedLoad(ctx, p.Node, PinnedRequest{Engine: p.Engine, Model: p.Model, LoadOpts: p.LoadOpts, Evict: p.Evict})
	if err != nil {
		code := -32000
		if pinnedStatus(err) == 400 {
			code = -32602
		}
		m.codec.RespondError(msg.ID, code, err.Error())
		return
	}
	m.codec.Respond(msg.ID, res)
}
