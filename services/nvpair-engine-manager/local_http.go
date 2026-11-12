// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "net/http"

// localStartError writes a failed /start (seam E14): a pinned-load gate error
// (a busy or refused startup load) keeps its own status and victims body via
// writePinnedError; any other error is upstream's 500 with the error text.
func localStartError(w http.ResponseWriter, err error) {
	if pinnedStatus(err) != http.StatusInternalServerError {
		writePinnedError(w, err)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
