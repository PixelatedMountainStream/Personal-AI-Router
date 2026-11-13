// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

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
	d := newRefreshTestDaemon()
	_, port := startModelsStub(t, http.StatusOK, map[string]any{"models": []string{"a"}})
	n := noderec.DirectoryNode{HostUUID: "p", Services: map[noderec.ServiceKey]noderec.ServiceStatus{
		noderec.ServiceEngineManager: {Port: port}}}
	d.enrichModelsCandidates(&n, []string{"127.0.0.1"})
	assert.Equal(t, []string{"a"}, n.Models)
	assert.Zero(t, n.LocalNodeExt)
}
