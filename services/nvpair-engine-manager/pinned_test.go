// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// bundledEngineExec builds an Executor over the real bundled manifest for name,
// with that engine "running" on the httptest handler h.
func bundledEngineExec(t *testing.T, name string, h http.Handler) *Executor {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	raw, err := os.ReadFile("manifests/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	key := runtime.GOOS + "/" + runtime.GOARCH
	p := m.Platforms[key]
	p.Runtime.Port = port
	m.Platforms[key] = p
	ex := newTestExecutor(t, &m)
	st, err := ex.state(name)
	if err != nil {
		t.Fatal(err)
	}
	st.running, st.port = true, port
	return ex
}

func TestOptionalArgv(t *testing.T) {
	act := Action{OptionalArgs: map[string][]string{
		"context_length": {"--context-length", "{context_length}"},
		"parallel":       {"--parallel", "{parallel}"},
	}}
	got, err := optionalArgv(act, json.RawMessage(`{"model":"m","context_length":1000000}`))
	if err != nil || !reflect.DeepEqual(got, []string{"--context-length", "1000000"}) {
		t.Fatalf("present/absent/exact: %v %v", got, err)
	}
	for _, bad := range []string{`{"parallel":"4"}`, `{"parallel":1.5}`, `{"parallel":-1}`} {
		if _, err := optionalArgv(act, json.RawMessage(bad)); err == nil {
			t.Fatalf("%s must be rejected", bad)
		}
	}
	for _, k := range []string{"model", "cli", "Bad"} {
		a := Action{Cmd: []string{"x"}, OptionalArgs: map[string][]string{k: {"--f", "{" + k + "}"}}}
		if a.validate("load") == nil {
			t.Fatalf("reserved/invalid key %q must be refused", k)
		}
	}
}

func TestLoadWirePerEngine(t *testing.T) {
	_, p, err := modelLoadWire("ollama", "q", LoadOpts{ContextLength: 8192, Parallel: 1})
	if err != nil || !strings.Contains(string(p), `"options":{"num_ctx":8192}`) {
		t.Fatalf("ollama: %s %v", p, err)
	}
	if _, _, err := modelLoadWire("ollama", "q", LoadOpts{Parallel: 4}); pinnedStatus(err) != 400 {
		t.Fatalf("ollama parallel 4: %v", err)
	}
	if _, _, err := modelLoadWire("llamacpp", "q", LoadOpts{ContextLength: 4096}); pinnedStatus(err) != 400 {
		t.Fatalf("llamacpp ctx: %v", err)
	}
	if a, p, err := modelLoadWire("llamacpp", "q", LoadOpts{}); err != nil || a != "load_model" || string(p) != `{"model":"q"}` {
		t.Fatalf("llamacpp bare: %s %s %v", a, p, err)
	}
	_, p, _ = modelLoadWire("lmstudio", "q", LoadOpts{ContextLength: 16384, Parallel: 2})
	raw, _ := os.ReadFile("manifests/lmstudio.json")
	var m Manifest
	_ = json.Unmarshal(raw, &m)
	argv, err := optionalArgv(m.Actions["load_model"], p)
	want := []string{"--context-length", "16384", "--parallel", "2"}
	if err != nil || !reflect.DeepEqual(argv, want) || m.Actions["load_model"].Cmd[len(m.Actions["load_model"].Cmd)-1] != "--yes" {
		t.Fatalf("lmstudio argv: %v %v", argv, err)
	}
}

func TestPinnedRoutingAndPeers(t *testing.T) {
	// Non-loopback callers are refused before the loader runs.
	called := false
	h := loopbackOnly(pinnedLoadHandler(func(context.Context, string, PinnedRequest) (PinnedResult, error) {
		called = true
		return PinnedResult{}, nil
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/models/load", strings.NewReader(`{}`))
	req.RemoteAddr = "10.1.2.3:5555"
	h.ServeHTTP(rec, req)
	if rec.Code != 403 || called {
		t.Fatalf("non-loopback: %d called=%v", rec.Code, called)
	}

	// "self" runs on the local executor; the ollama engine sees num_ctx.
	var body string
	ex := bundledEngineExec(t, "ollama", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ps" || r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		_, _ = w.Write([]byte(`{"done":true}`))
	}))
	m := &Manager{exec: ex, peers: newPeerDirectory()}
	res, err := m.pinnedLoad(context.Background(), "self", PinnedRequest{Engine: "ollama", Model: "q", LoadOpts: LoadOpts{ContextLength: 8192}})
	if err != nil || res.Node != "self" || !strings.Contains(body, `"num_ctx":8192`) {
		t.Fatalf("self: %+v %v body=%s", res, err, body)
	}
	// Unknown node: 404.
	if _, err := m.pinnedLoad(context.Background(), "nope", PinnedRequest{}); pinnedStatus(err) != 404 {
		t.Fatalf("unknown node: %v", err)
	}

	// An old peer 404s the new route: reported as "peer too old".
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	c := &remoteClient{http: old.Client(), base: old.URL}
	if _, err := c.pinnedLoad(context.Background(), PinnedRequest{Engine: "ollama", Model: "q"}); err == nil || !strings.Contains(err.Error(), "peer too old") {
		t.Fatalf("old peer: %v", err)
	}
}
