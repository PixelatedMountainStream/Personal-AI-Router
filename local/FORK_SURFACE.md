<!--
SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0
-->

# Fork surface records

Hook records for every upstream file the fork edits, checked by
`local/scripts/check_fork_surface.py` against `local/fork-surface.toml`.
Grammar (a malformed record is a tool error, exit 2):

- `### <id>`: `P<n>`/`E<n>`/`S<n>`, or `L-<path-slug>` for legacy records.
- `files:` comma-separated repo-relative paths, no globs.
- `kind:` one of `hook|data|legacy`.
- `recipe:` how to re-apply the hook on upstream's version (single line).
- `Tests:` `<module dir> <pkg pattern> -run '<regex>'` (required for `kind: hook`; the regex must match `Test<Id>_Nil`).

Hooks in Go files are marked `// fork:<id>` (trailing, or a standalone comment on the line above).

## Legacy records (PR-A)

One per file in `allow` today. PR-C..F replace them with real hook records (P/E/S ids) and shrink `allow`.

### L-services-nvpair-engine-manager-manifest-md
files: services/nvpair-engine-manager/MANIFEST.md
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-actions-go
files: services/nvpair-engine-manager/actions.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-controllifecycle-go
files: services/nvpair-engine-manager/controllifecycle.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-controlmodels-go
files: services/nvpair-engine-manager/controlmodels.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-controlserver-go
files: services/nvpair-engine-manager/controlserver.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-executor-go
files: services/nvpair-engine-manager/executor.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-httpserver-go
files: services/nvpair-engine-manager/httpserver.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-lifecycle-go
files: services/nvpair-engine-manager/lifecycle.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-loadedwatch-go
files: services/nvpair-engine-manager/loadedwatch.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-main-go
files: services/nvpair-engine-manager/main.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-manager-go
files: services/nvpair-engine-manager/manager.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-manifests-llamacpp-json
files: services/nvpair-engine-manager/manifests/llamacpp.json
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-manifests-lmstudio-json
files: services/nvpair-engine-manager/manifests/lmstudio.json
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-manifests-ollama-json
files: services/nvpair-engine-manager/manifests/ollama.json
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-modelops-go
files: services/nvpair-engine-manager/modelops.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-install-artifacts-test-go
files: services/nvpair-engine-manager/install_artifacts_test.go
kind: legacy
recipe: Fallback edit: `defer cancel()` after the context.WithCancel in the cancel test, because go vet lostcancel misses require.FailNow after upstream #160. Drop when upstream fixes it.

### L-services-nvpair-engine-manager-models-test-go
files: services/nvpair-engine-manager/models_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-registry-go
files: services/nvpair-engine-manager/registry.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-remote-go
files: services/nvpair-engine-manager/remote.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-remoteclient-go
files: services/nvpair-engine-manager/remoteclient.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-remotepeers-go
files: services/nvpair-engine-manager/remotepeers.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-spec-md
files: services/nvpair-engine-manager/spec.md
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-engine-manager-status-go
files: services/nvpair-engine-manager/status.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-job-scheduler-schedule-test-go
files: services/nvpair-job-scheduler/schedule_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-job-scheduler-state-go
files: services/nvpair-job-scheduler/state.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-scanner-connreuse-test-go
files: services/nvpair-node-scanner/connreuse_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-scanner-daemon-go
files: services/nvpair-node-scanner/daemon.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-scanner-directory-go
files: services/nvpair-node-scanner/directory.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-scanner-enrich-hosts-go
files: services/nvpair-node-scanner/enrich_hosts.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-scanner-refresh-test-go
files: services/nvpair-node-scanner/refresh_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-settings-manager-go
files: services/nvpair-node-settings/manager.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-node-settings-manager-test-go
files: services/nvpair-node-settings/manager_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-proxy-facade-go
files: services/nvpair-proxy/facade.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-proxy-main-go
files: services/nvpair-proxy/main.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-proxy-proxy-go
files: services/nvpair-proxy/proxy.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-proxy-reservation-test-go
files: services/nvpair-proxy/reservation_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-ui-broker-broker-go
files: services/nvpair-ui-broker/broker.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-ui-broker-discovery-go
files: services/nvpair-ui-broker/discovery.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-ui-broker-discovery-test-go
files: services/nvpair-ui-broker/discovery_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-shared-schedulerwire-schedulerwire-test-go
files: services/shared/schedulerwire/schedulerwire_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

## Hook records (catch-up step 2)

`daemon.go` keeps its legacy record alongside S4 until step 4 drops the unmarked S8 hand edits (conversion rule, plan section 5.2). `noderec.go` has no inline edit outside S1, so its legacy record is converted.

### S1
files: services/shared/noderec/noderec.go
kind: hook
recipe: Take upstream noderec.go; add `// fork:S1` then `LocalNodeExt` as the last DirectoryNode field (after LastSeen, so gofmt does not realign it); the type lives in services/shared/noderec/local_ext.go.
Tests: services/shared ./noderec/ -run 'TestS1_Nil|TestLocalNodeExtJSONKeys'

### S4
files: services/nvpair-node-scanner/daemon.go
kind: hook
recipe: Take upstream daemon.go; add the `local *localScanState` field and its newDaemon init, `d.local.migrate` in dropSelf, `d.local.forget` in forget, `node.LocalNodeExt = d.local.record(...)` on enrich success, `cachedExt := d.local.cached(...)` plus `node.LocalNodeExt = cachedExt` on the enrich fallback, and `observed := d.local.record(...).ModelsObservedAt` in refreshNodeModelsCandidates, each marked `// fork:S4`; bodies in local_capacity.go.
Tests: services/nvpair-node-scanner . -run 'TestS4_Nil|TestLocal'

## Hook records (catch-up step 3)

`directory.go` keeps its legacy record alongside S5, and `connreuse_test.go` keeps its legacy record, until step 4 drops the unmarked S8 hand edits (conversion rule, plan section 5.2). `models.go`, `schedule.go` and `schedulerwire.go` have no inline edit outside E11, S3 and S2, so their legacy records are converted.

### S2
files: services/shared/schedulerwire/schedulerwire.go
kind: hook
recipe: Take upstream schedulerwire.go; add `// fork:S2` then `LocalRankExt` as the last NodeRank field; replace the two `append([]NodeRank(nil), p.Ranks...)` copies (Clone, Snapshot) with `CloneRanks(p.Ranks)` and SameRanking's `slices.Equal(p.Ranks, other.Ranks)` with `slices.EqualFunc(p.Ranks, other.Ranks, NodeRank.Equal)`, each marked `// fork:S2` (the embedded map makes NodeRank non-comparable); the type and helpers live in services/shared/schedulerwire/local_ext.go.
Tests: services/shared ./schedulerwire/ -run 'TestS2_Nil|TestSameRankingIgnoresComputedAtButNotPendingBy'

### S3
files: services/nvpair-job-scheduler/schedule.go
kind: hook
recipe: Take upstream schedule.go; in rankAt add `m.localAnnotateRanks(ranks, now)` before the m.mu.Unlock that follows the ranks loop, and in emitIfChanged replace `!slices.Equal(prev.ranks, ranks)` with `!localRankUnchanged(prev.ranks, ranks)`, each marked `// fork:S3`; bodies in local_rank.go.
Tests: services/nvpair-job-scheduler . -run 'TestS3_Nil|TestLocalRank|TestEmit_IncludesPendingRanksLocalExt'

### S5
files: services/nvpair-node-scanner/directory.go
kind: hook
recipe: Take upstream directory.go; in applyModels add `defer d.localMergeCapacity(hostUUID, inv, observedAt, &node, &changed, &ok)` marked `// fork:S5` right after `defer d.mu.Unlock()` (so it runs under the lock and adjusts the named results); body in local_capacity.go. The applyModels signature/compare/assign edits are S8 (unmarked until step 4).
Tests: services/nvpair-node-scanner . -run 'TestS5_Nil|TestLocalApplyModels|TestApplyModelsObservedAtHeartbeat|TestRefreshSlotChangeReportsChange'

### E11
files: services/nvpair-engine-manager/models.go
kind: hook
recipe: Take upstream models.go; add `// fork:E11` then `LocalModelsExt` as the last ModelsResult field; in ModelsResult add `lx := newLocalSlotSweep(len(engineNames))` after the per-engine slices, `lx.prepare(i, mf)` after the registry lookup, `lx.collect(ctx, e, i, name, loadedOK[i], loaded[i])` at the end of the per-engine goroutine and `lx.finish(e, &res, engineNames)` before `return res`; in extractStringsResult add `arr, _ = expandInstances(arr, spec, false)` before the row loop; each marked `// fork:E11`; bodies in local_models.go.
Tests: services/nvpair-engine-manager . -run 'TestE11_Nil|TestLocalModelsResultJSONKeys|TestExtractRecords|TestBundledSlotsQueryThroughManifests|TestApplySettingsOverrides'
