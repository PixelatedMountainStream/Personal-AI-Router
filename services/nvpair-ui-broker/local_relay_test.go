// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nvpair-shared/noderec"
)

// TestLocalBrokerRelayJSONKeys (step-4 characterization, ui-broker ext copy):
// a directory node's slots and jit_load reach AvailableNode's JSON under their
// wire keys; the scanner-only modelsObservedAt is not relayed.
func TestLocalBrokerRelayJSONKeys(t *testing.T) {
	ext := noderec.LocalNodeExt{SlotsByEngine: map[string]map[string]noderec.SlotInfo{"lmstudio": {"a": {Slots: 2}}},
		JITLoadByEngine: map[string]bool{"lmstudio": false}, ModelsObservedAt: 5}
	e := directoryToEnriched(noderec.DirectoryNode{HostUUID: "h", Name: "n", LocalNodeExt: ext})
	b, err := json.Marshal(storedNode{scanner: &e}.toAvailable())
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keys))
	assert.JSONEq(t, `{"lmstudio":{"a":{"slots":2}}}`, string(keys["slotsByEngine"]))
	assert.JSONEq(t, `{"lmstudio":false}`, string(keys["jitLoadByEngine"]))
	assert.NotContains(t, keys, "modelsObservedAt")
	b, err = json.Marshal(storedNode{scanner: &EnrichedNode{ID: "n"}}.toAvailable())
	require.NoError(t, err)
	assert.NotContains(t, string(b), "slotsByEngine")
	assert.NotContains(t, string(b), "jitLoadByEngine")
}
