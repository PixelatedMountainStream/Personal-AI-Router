// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"time"

	"nvpair-shared/noderec"
)

// localScanState is the fork's enrichment cache (seam S4): per-node slot
// capacity, jit_load flags and models-observed time from the last successful
// /v1/models fetch, reused on a transient miss exactly like upstream's
// lastModels caches. Every method is nil-safe; a nil state leaves the daemon on
// upstream's code path. Callers hold d.infoMu, which guards it with the
// upstream caches it parallels.
type localScanState struct {
	ext map[string]noderec.LocalNodeExt // keyed by hostUuid
}

func newLocalScanState() *localScanState {
	return &localScanState{ext: make(map[string]noderec.LocalNodeExt)}
}

// record caches a successful fetch's slot/jit data observed at now and returns
// the node extension to publish (zero when s is nil).
func (s *localScanState) record(hostUUID string, inv modelInventory, now time.Time) noderec.LocalNodeExt {
	if s == nil {
		return noderec.LocalNodeExt{}
	}
	e := noderec.LocalNodeExt{SlotsByEngine: inv.slotsByEngine, JITLoadByEngine: inv.jitByEngine,
		ModelsObservedAt: now.UnixMilli()}
	s.ext[hostUUID] = e
	return e
}

// cached returns the last recorded extension for a node (zero if none).
func (s *localScanState) cached(hostUUID string) noderec.LocalNodeExt {
	if s == nil {
		return noderec.LocalNodeExt{}
	}
	return s.ext[hostUUID]
}

// migrate moves a node's cache to its new host UUID (dropSelf).
func (s *localScanState) migrate(oldHostUUID, newHostUUID string) {
	if s == nil {
		return
	}
	if e, ok := s.ext[oldHostUUID]; ok {
		s.ext[newHostUUID] = e
	}
	delete(s.ext, oldHostUUID)
}

// forget drops a node's cache on eviction.
func (s *localScanState) forget(hostUUID string) {
	if s == nil {
		return
	}
	delete(s.ext, hostUUID)
}
