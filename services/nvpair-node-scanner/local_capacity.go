// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"time"

	"nvpair-shared/noderec"
)

// modelInventoryExt is the fork's part of a /v1/models answer (seam S8), held
// in modelInventory.local so NVIDIA's lastInventory cache, dropSelf migration
// and forget carry it with the upstream fields: slot capacity and jit_load as
// the engine manager reported them, and observedAt, the Unix milliseconds of
// the fetch, stamped at fetch time by a non-nil localScanState (0 otherwise).
// The zero value is an upstream-shaped answer.
type modelInventoryExt struct {
	slotsByEngine map[string]map[string]noderec.SlotInfo
	jitByEngine   map[string]bool
	observedAt    int64
}

// node is the extension published on a directory entry.
func (x modelInventoryExt) node() noderec.LocalNodeExt {
	return noderec.LocalNodeExt{SlotsByEngine: x.slotsByEngine, JITLoadByEngine: x.jitByEngine, ModelsObservedAt: x.observedAt}
}

// applyTo publishes the extension on a directory entry, alongside upstream's
// modelInventory.applyTo (seam S4, enrichment and its cached fallback).
func (x modelInventoryExt) applyTo(node *noderec.DirectoryNode) {
	node.LocalNodeExt = x.node()
}

// localModelsWire is embedded in fetchModels' decode struct (seam S8), so the
// fork's /v1/models keys decode in the same pass as upstream's.
type localModelsWire struct {
	SlotsByEngine   map[string]map[string]noderec.SlotInfo `json:"slotsByEngine"`
	JITLoadByEngine map[string]bool                        `json:"jitLoadByEngine"`
}

// localScanState is the daemon's fork state (seam S4). Its only job since
// step 4 is the observed-at clock: NVIDIA's lastInventory caches the whole
// modelInventory, extension included. Every method is nil-safe; a nil state
// stamps no observedAt, so with an upstream-shaped answer the extension is zero.
type localScanState struct {
	now func() time.Time
}

func newLocalScanState() *localScanState {
	return &localScanState{now: time.Now}
}

// ext builds a fetch's extension from the decoded fork keys (seam S8).
func (s *localScanState) ext(w localModelsWire) modelInventoryExt {
	x := modelInventoryExt{slotsByEngine: w.SlotsByEngine, jitByEngine: w.JITLoadByEngine}
	if s != nil {
		x.observedAt = s.now().UnixMilli()
	}
	return x
}

// modelsObservedHeartbeat bounds how often an unchanged inventory republishes a
// fresher ModelsObservedAt.
const modelsObservedHeartbeat = 30 * time.Second

// localMergeCapacity is applyModels' exit hook (seam S5), deferred under the
// directory lock so it sees applyModels' named results. Upstream's compare
// covers models, modelsByEngine, loadedByEngine and downloadedByEngine; on a
// valid apply this stores the fetch's extension when that compare changed, when
// the slots or jit_load changed, or when an identical inventory is a heartbeat
// newer than the stored observation; the last two report a change. With a zero
// extension (an upstream-shaped answer to a nil localScanState) it stores the
// zero extension, leaving upstream's result unchanged.
func (d *directory) localMergeCapacity(hostUUID string, inv modelInventory, node *noderec.DirectoryNode, changed, ok *bool) {
	if !*ok {
		return
	}
	n, x := d.nodes[hostUUID], inv.local
	if !*changed && reflect.DeepEqual(n.SlotsByEngine, x.slotsByEngine) &&
		reflect.DeepEqual(n.JITLoadByEngine, x.jitByEngine) &&
		x.observedAt-n.ModelsObservedAt < modelsObservedHeartbeat.Milliseconds() {
		return
	}
	x.applyTo(&n)
	d.nodes[hostUUID] = n
	*node, *changed = n, true
}
