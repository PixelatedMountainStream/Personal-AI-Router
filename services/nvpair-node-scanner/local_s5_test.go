// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestS5_Nil: upstream-shaped inventories (no slots/jit) observed at 0, as a
// nil localScanState reports, give upstream's changed/ok and no extension.
func TestS5_Nil(t *testing.T) {
	d := newDirectory()
	n0 := emNode("p", 14322)
	n0.IP = "10.0.0.9"
	n0.Models = []string{"a"}
	d.upsert(n0)
	_, changed, ok := d.applyModels("p", "10.0.0.9", 14322, modelInventory{models: []string{"a"}}, 0)
	assert.True(t, ok && !changed, "identical inventory: changed=%v ok=%v", changed, ok)
	n, changed, ok := d.applyModels("p", "10.0.0.9", 14322, modelInventory{models: []string{"b"}}, 0)
	assert.True(t, ok && changed, "new inventory: changed=%v ok=%v", changed, ok)
	assert.Zero(t, n.LocalNodeExt, "extension")
	_, changed, ok = d.applyModels("p", "10.0.0.1", 14322, modelInventory{models: []string{"c"}}, 0)
	assert.True(t, !ok && !changed, "stale address: changed=%v ok=%v", changed, ok)
}
