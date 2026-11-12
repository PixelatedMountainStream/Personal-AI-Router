// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

// LocalNodeExt is the fork's extension of DirectoryNode (seam S1): per-engine
// slot capacity, jit_load flags and the models-observed heartbeat. It is
// embedded, so encoding/json flattens it and the wire keys are unchanged.
type LocalNodeExt struct {
	// SlotsByEngine is the parallel capacity of each engine's loaded models
	// (engine -> model -> SlotInfo), enriched from engine-manager's em /v1/models
	// slotsByEngine field with node-settings overrides applied. A missing engine
	// or model means "capacity unknown" (never "one slot"). Absent on records from
	// older peers.
	SlotsByEngine map[string]map[string]SlotInfo `json:"slotsByEngine,omitempty"`
	// JITLoadByEngine says whether an engine loads a missing model on demand
	// (engine -> bool), resolved from the jit_load.<engine> setting or the engine
	// default (see DefaultJITLoad). A missing engine key means the peer did not
	// say; treat as the engine default.
	JITLoadByEngine map[string]bool `json:"jitLoadByEngine,omitempty"`
	// ModelsObservedAt is when (Unix milliseconds) the scanner last completed a
	// successful models fetch for this node, whether or not the inventory
	// changed. An unchanged sweep advances it only once it is 30s ahead of the
	// published value, so it is a bounded-staleness heartbeat (<= ~30s plus a
	// sweep behind the true last fetch), not a per-sweep value. 0 when never set
	// (older peers).
	ModelsObservedAt int64 `json:"modelsObservedAt,omitempty"`
}

// SlotInfo is a loaded model's parallel capacity: concurrent request slots and
// the context window each slot gets (0 when unknown). It mirrors the
// engine-manager wire shape.
type SlotInfo struct {
	Slots      int `json:"slots"`
	CtxPerSlot int `json:"ctxPerSlot,omitempty"`
	// Bytes is the model's resident size when a record declares "bytes"
	// (model_sizes); internal, never part of the wire shape.
	Bytes int64 `json:"-"`
}
