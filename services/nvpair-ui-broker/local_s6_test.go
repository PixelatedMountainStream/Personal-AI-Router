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

// TestS6_Nil: a directory node without the fork's fields relays a zero
// extension and the broker's JSON is upstream's (no fork keys).
func TestS6_Nil(t *testing.T) {
	e := directoryToEnriched(noderec.DirectoryNode{HostUUID: "h", Name: "n"})
	assert.Zero(t, e.LocalBrokerExt)
	b, err := json.Marshal(storedNode{scanner: &e}.toAvailable())
	require.NoError(t, err)
	assert.NotContains(t, string(b), "slotsByEngine")
	assert.NotContains(t, string(b), "jitLoadByEngine")
}

// TestDirectoryRelaysSlotCapacity: slot capacity and jit_load on a directory node
// reach both the enriched projection and the client-facing AvailableNode.
// (Moved from discovery_test.go at catch-up step 4.)
func TestDirectoryRelaysSlotCapacity(t *testing.T) {
	slots := map[string]map[string]noderec.SlotInfo{"ollama": {"q": {Slots: 4, CtxPerSlot: 8192}}}
	jit := map[string]bool{"ollama": true}
	e := directoryToEnriched(noderec.DirectoryNode{HostUUID: "h", Name: "n", LocalNodeExt: noderec.LocalNodeExt{SlotsByEngine: slots, JITLoadByEngine: jit}})
	if e.SlotsByEngine["ollama"]["q"].Slots != 4 || !e.JITLoadByEngine["ollama"] {
		t.Fatalf("enriched lost capacity: %+v", e)
	}
	av := storedNode{scanner: &e}.toAvailable()
	if av.SlotsByEngine["ollama"]["q"].CtxPerSlot != 8192 || !av.JITLoadByEngine["ollama"] {
		t.Fatalf("available lost capacity: %+v", av)
	}
}
