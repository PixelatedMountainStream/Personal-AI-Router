// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE11_Nil: a nil slot sweep leaves the result exactly as upstream builds
// it, and a zero LocalModelsExt adds no wire keys.
func TestE11_Nil(t *testing.T) {
	var lx *localSlotSweep
	res := ModelsResult{Models: []string{"a"}, LoadedByEngine: map[string][]string{"e": {"a"}}}
	lx.prepare(0, &Manifest{})
	lx.collect(context.Background(), nil, 0, "e", true, []string{"a"})
	lx.finish(nil, &res, []string{"e"})
	assert.Nil(t, res.SlotsByEngine, "slots")
	assert.Nil(t, res.JITLoadByEngine, "jit")
	b, err := json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"models":["a"],"loadedByEngine":{"e":["a"]}}`, string(b))
}
