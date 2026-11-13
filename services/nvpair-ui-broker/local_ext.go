// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "nvpair-shared/noderec"

// LocalBrokerExt is the fork's extension of EnrichedNode and AvailableNode
// (seam S6): per-engine slot capacity (loaded models) and jit_load flags,
// relayed from the node directory. It is embedded, so encoding/json flattens
// it and the wire keys are unchanged; the zero value adds no keys. The
// scanner-only ModelsObservedAt is not relayed.
type LocalBrokerExt struct {
	SlotsByEngine   map[string]map[string]noderec.SlotInfo `json:"slotsByEngine,omitempty"`
	JITLoadByEngine map[string]bool                        `json:"jitLoadByEngine,omitempty"`
}

// localBrokerExt projects a directory node's extension onto the broker's.
func localBrokerExt(n noderec.DirectoryNode) LocalBrokerExt {
	return LocalBrokerExt{SlotsByEngine: n.SlotsByEngine, JITLoadByEngine: n.JITLoadByEngine}
}
