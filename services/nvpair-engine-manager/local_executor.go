// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "context"

// statusNoWait is Status for callers that may already hold a lifecycle lock
// (startup gating) or run under a deadline: it never blocks on opMu (as
// NVIDIA's StatusAtPort no longer does either), and its presence probe honors
// ctx where StatusAtPort uses context.Background(). Moved out of status.go at
// catch-up step 4; status.go is NVIDIA verbatim.
func (e *Executor) statusNoWait(ctx context.Context, engine string) (EngineStatus, error) {
	st, err := e.state(engine)
	if err != nil {
		return EngineStatus{}, err
	}
	if !st.opMu.TryLock() {
		return e.snapshot(engine, st), nil
	}
	defer st.opMu.Unlock()
	pathInstalled, _ := e.Detect(engine)
	st.mu.Lock()
	port := st.port
	st.mu.Unlock()
	e.reconcilePresence(ctx, engine, st, pathInstalled, port, false)
	return e.snapshot(engine, st), nil
}
