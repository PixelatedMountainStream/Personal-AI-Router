// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nvpair-shared/noderec"
)

// TestS4_Nil: with no fork state the hooks are inert and enrichment publishes a
// zero LocalNodeExt, i.e. upstream's record.
func TestS4_Nil(t *testing.T) {
	var s *localScanState
	assert.Zero(t, s.ext(localModelsWire{}))
	var n0 noderec.DirectoryNode
	modelInventoryExt{}.applyTo(&n0)
	assert.Zero(t, n0.LocalNodeExt)
	d := newSelfTestDaemon("self", "127.0.0.1")
	require.Nil(t, d.local)
	d.reg.register(noderec.RegisterParams{Service: noderec.ServiceEngineManager, Port: 1})
	d.publishSelf()
	n, ok := d.dir.get("self")
	require.True(t, ok)
	assert.Zero(t, n.LocalNodeExt)
}

// TestLocalPublishSelfCarriesExt: the owned inventory's extension is published
// on the local record (S4).
func TestLocalPublishSelfCarriesExt(t *testing.T) {
	d := newSelfTestDaemon("self", "127.0.0.1")
	d.reg.register(noderec.RegisterParams{Service: noderec.ServiceEngineManager, Port: 1})
	d.localModels = modelInventory{local: modelInventoryExt{jitByEngine: map[string]bool{"lmstudio": true}, observedAt: 7}}
	d.publishSelf()
	n, _ := d.dir.get("self")
	assert.True(t, n.JITLoadByEngine["lmstudio"])
	assert.Equal(t, int64(7), n.ModelsObservedAt)
}
