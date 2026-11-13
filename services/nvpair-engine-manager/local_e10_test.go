// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE10_Nil: a zero extension validates exactly as upstream (a result still
// needs array and field), adds no manifest keys, and the bundled manifests
// load.
func TestE10_Nil(t *testing.T) {
	var a *Action
	assert.NoError(t, a.localValidate("x"))
	ok := Action{HTTP: &ActionHTTP{Method: "GET", Path: "/m"}, Result: &ActionResult{Array: "data", Field: "id"}}
	assert.NoError(t, ok.validate("list_models"))
	bad := Action{HTTP: &ActionHTTP{Method: "GET", Path: "/m"}, Result: &ActionResult{Array: "data"}}
	assert.ErrorContains(t, bad.validate("list_models"), "result.array and result.field are required")
	b, err := json.Marshal(ok)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "optional_args")
	assert.NotContains(t, string(b), "record")
	assert.NotContains(t, string(b), "loading_in")
	assert.NotEmpty(t, bundledManifestSet(t))
}

// TestLocalActionExtValidate: the fork keys decode through the embeds and are
// validated (a whole-response record may omit array and field).
func TestLocalActionExtValidate(t *testing.T) {
	var a Action
	require.NoError(t, json.Unmarshal([]byte(`{"http":{"method":"GET","path":"/props"},
		"result":{"record":{"fields":{"slots":"total_slots"}},"each":"inst","match":{"field":"s","in":["loaded"],"loading_in":["loading"]}}}`), &a))
	assert.Equal(t, "total_slots", a.Result.Record.Fields["slots"])
	assert.Equal(t, "inst", a.Result.Each)
	assert.Equal(t, []string{"loading"}, a.Result.Match.LoadingIn)
	assert.NoError(t, a.validate("slots"))
	a.Result.Record.Fields = map[string]string{"ctx": "n_ctx"}
	assert.ErrorContains(t, a.validate("slots"), "requires a slots or bytes")
	c := Action{Cmd: []string{"x"}, LocalActionExt: LocalActionExt{OptionalArgs: map[string][]string{"model": {"--m"}}}}
	assert.ErrorContains(t, c.validate("load_model"), "invalid or reserved")
}
