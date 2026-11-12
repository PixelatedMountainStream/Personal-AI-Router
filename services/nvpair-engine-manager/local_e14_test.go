// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestE14_Nil: a non-pinned start error is upstream's 500 with the error text;
// a pinned gate error keeps its own status.
func TestE14_Nil(t *testing.T) {
	want := httptest.NewRecorder()
	http.Error(want, "boom", http.StatusInternalServerError)
	got := httptest.NewRecorder()
	localStartError(got, errString("boom"))
	assert.Equal(t, want.Code, got.Code)
	assert.Equal(t, want.Body.String(), got.Body.String())
	got = httptest.NewRecorder()
	localStartError(got, badParam("x"))
	assert.Equal(t, pinnedStatus(badParam("x")), got.Code)
	assert.NotEqual(t, http.StatusInternalServerError, got.Code)
}

type errString string

func (e errString) Error() string { return string(e) }
