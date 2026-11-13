// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package affinity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAffinity_SizeCap(t *testing.T) {
	var tbl Table
	for i := 0; i < MaxKeys+10; i++ {
		tbl.Bind(string(rune('a'+i%26))+time.Duration(i).String(), "n")
	}
	if len(tbl.m) > MaxKeys {
		t.Fatalf("table grew to %d", len(tbl.m))
	}
}

func TestAffinityKey(t *testing.T) {
	req := func(h map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/responses", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	body := []byte(`{"model":"m","prompt_cache_key":"root"}`)
	if got := Key(req(map[string]string{"thread-id": "t1", "session-id": "root"}), body); got != "t1" {
		t.Fatalf("thread-id should win, got %q", got)
	}
	if got := Key(req(nil), body); got != "root" {
		t.Fatalf("prompt_cache_key fallback, got %q", got)
	}
	if got := Key(req(map[string]string{"session-id": "s"}), []byte(`{"model":"m"}`)); got != "s" {
		t.Fatalf("session-id fallback, got %q", got)
	}
	if got := Key(req(nil), []byte(`{"model":"m"}`)); got != "" {
		t.Fatalf("expected no key, got %q", got)
	}
}
