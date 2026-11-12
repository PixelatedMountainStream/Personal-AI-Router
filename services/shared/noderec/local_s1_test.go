// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package noderec

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestS1_Nil: a zero LocalNodeExt adds no keys, so a DirectoryNode marshals
// exactly as upstream's does.
func TestS1_Nil(t *testing.T) {
	b, err := json.Marshal(DirectoryNode{HostUUID: "h"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"slotsByEngine", "jitLoadByEngine", "modelsObservedAt"} {
		if strings.Contains(string(b), k) {
			t.Errorf("zero ext leaked %s: %s", k, b)
		}
	}
}
