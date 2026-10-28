// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nvpair-shared/noderec"
)

// TestRefreshSlotChangeReportsChange: a slots-only or jit-only change (same
// models, same loaded set) is a directory change; an identical one is not.
func TestRefreshSlotChangeReportsChange(t *testing.T) {
	d := newRefreshTestDaemon()
	body := func(slots int, jit bool) map[string]any {
		return map[string]any{
			"models":          []string{"a"},
			"modelsByEngine":  map[string][]string{"lmstudio": {"a"}},
			"loadedByEngine":  map[string][]string{"lmstudio": {"a"}},
			"slotsByEngine":   map[string]map[string]map[string]int{"lmstudio": {"a": {"slots": slots, "ctxPerSlot": 4096}}},
			"jitLoadByEngine": map[string]bool{"lmstudio": jit},
		}
	}
	stub, port := startModelsStub(t, http.StatusOK, body(2, false))
	seedEMNode(d, "peer-S", "127.0.0.1", port)
	require.True(t, d.refreshNodeModels("peer-S", "127.0.0.1", port, ""), "initial populate should report a change")
	got, _ := d.dir.get("peer-S")
	si := got.SlotsByEngine["lmstudio"]["a"]
	require.True(t, si.Slots == 2 && si.CtxPerSlot == 4096 && !got.JITLoadByEngine["lmstudio"],
		"slots/jit not populated: %+v %+v", got.SlotsByEngine, got.JITLoadByEngine)
	assert.False(t, d.refreshNodeModels("peer-S", "127.0.0.1", port, ""), "identical slots should not report a change")
	stub.set(http.StatusOK, body(4, false))
	assert.True(t, d.refreshNodeModels("peer-S", "127.0.0.1", port, ""), "a slot-count change must report a change")
	stub.set(http.StatusOK, body(4, true))
	assert.True(t, d.refreshNodeModels("peer-S", "127.0.0.1", port, ""), "a jit_load change must report a change")
}

// TestApplyModelsObservedAtHeartbeat: an identical sweep republishes a fresher
// ModelsObservedAt only once it is a heartbeat ahead; sooner it is silent.
func TestApplyModelsObservedAtHeartbeat(t *testing.T) {
	d := newDirectory()
	d.upsert(noderec.DirectoryNode{
		HostUUID: "p", IP: "10.0.0.9", Models: []string{"a"}, ModelsObservedAt: 1000,
		Services: map[noderec.ServiceKey]noderec.ServiceStatus{noderec.ServiceEngineManager: {Port: 14322}},
	})
	inv := modelInventory{models: []string{"a"}}
	_, changed, ok := d.applyModels("p", "10.0.0.9", 14322, inv, 2000)
	require.True(t, ok && !changed, "sweep inside heartbeat: changed=%v ok=%v", changed, ok)
	n, changed, _ := d.applyModels("p", "10.0.0.9", 14322, inv, 1000+modelsObservedHeartbeat.Milliseconds())
	require.True(t, changed && n.ModelsObservedAt == 1000+modelsObservedHeartbeat.Milliseconds(),
		"sweep past heartbeat: changed=%v at=%d", changed, n.ModelsObservedAt)
}
