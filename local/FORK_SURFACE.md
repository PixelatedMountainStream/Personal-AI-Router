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

### L-services-nvpair-job-scheduler-schedule-test-go
files: services/nvpair-job-scheduler/schedule_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-nvpair-job-scheduler-state-go
files: services/nvpair-job-scheduler/state.go
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

### L-services-shared-schedulerwire-schedulerwire-test-go
files: services/shared/schedulerwire/schedulerwire_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

## Hook records (catch-up step 2)

`daemon.go` kept its legacy record alongside S4 until step 4 dropped the unmarked S8 hand edits (conversion rule, plan section 5.2); it is converted at step 4. `noderec.go` has no inline edit outside S1, so its legacy record is converted.

### S1
files: services/shared/noderec/noderec.go
kind: hook
recipe: Take upstream noderec.go; add `// fork:S1` then `LocalNodeExt` as the last DirectoryNode field (after DownloadedByEngine/LastSeen, so gofmt does not realign it); the type lives in services/shared/noderec/local_ext.go.
Tests: services/shared ./noderec/ -run 'TestS1_Nil|TestLocalNodeExtJSONKeys'

### S4
files: services/nvpair-node-scanner/daemon.go
kind: hook
recipe: Take upstream daemon.go; add `// fork:S4` then `local *localScanState` after the lastInventory field, `local: newLocalScanState(), // fork:S4` in the newDaemon literal, and in enrichModelsCandidates `inventory.local.applyTo(node)` after `inventory.applyTo(node)` and `cached.local.applyTo(node)` after `cached.applyTo(node)`, each marked `// fork:S4`; NVIDIA's lastInventory cache (dropSelf, forget, fallback) carries the extension inside modelInventory, so no migrate/forget/record hooks remain; bodies in local_capacity.go. daemon.go also carries S8.
Tests: services/nvpair-node-scanner . -run 'TestS4_Nil|TestLocal'

## Hook records (catch-up step 3)

`directory.go` kept its legacy record alongside S5, and `connreuse_test.go` kept its legacy record, until step 4 dropped the unmarked S8 hand edits (conversion rule, plan section 5.2); step 4 converts `directory.go` and returns `connreuse_test.go` to NVIDIA verbatim. `models.go`, `schedule.go` and `schedulerwire.go` have no inline edit outside E11, S3 and S2, so their legacy records are converted.

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
recipe: Take upstream directory.go (NVIDIA's 4-argument applyModels, verbatim); add `defer d.localMergeCapacity(hostUUID, inventory, &node, &changed, &ok)` marked `// fork:S5` right after `defer d.mu.Unlock()` (so it runs under the lock and adjusts the named results); it reads inventory.local and stores it when upstream's compare (including downloadedByEngine) changed, the capacity changed or the observed-at heartbeat is due; body in local_capacity.go.
Tests: services/nvpair-node-scanner . -run 'TestS5_Nil|TestLocalApplyModels|TestApplyModelsObservedAtHeartbeat|TestRefreshSlotChangeReportsChange|TestLocalDownloadedChangeKeepsCapacity'

### E11
files: services/nvpair-engine-manager/models.go
kind: hook
recipe: Take upstream models.go; add `// fork:E11` then `LocalModelsExt` as the last ModelsResult field (after DownloadedByEngine); in ModelsResult add `lx := newLocalSlotSweep(e, engineNames)` after `perEngine := make(...)`, `lx.collect(ctx, e, i, name, r.loadedOK, r.loaded)` after `perEngine[i] = r` (the normal path; NVIDIA's loadedOnlyEngineModels path is not hooked) and `lx.finish(e, &res, engineNames)` before `return res`; in extractStringsResult add `arr, _ = expandInstances(arr, spec, false)` before the row loop; each marked `// fork:E11`; bodies in local_models.go (loaded-only engines: jit_load forced false, slot overrides keyed on the served name).
Tests: services/nvpair-engine-manager . -run 'TestE11_Nil|TestLocalModelsResultJSONKeys|TestExtractRecords|TestBundledSlotsQueryThroughManifests|TestApplySettingsOverrides|TestLocalLMStudioModelsThroughBundledManifest|TestLocalLoadedOnlyEngineNeverJITAndSlotsOnServedName'

## Hook records (catch-up step 4)

NVIDIA #181 (vLLM) and #5. NVIDIA owns vLLM model switching (an operator action that restarts the engine); the fork's JIT does not drive it: `noderec.ApplyCapacityOverrides` forces jit_load false for engines whose manifest advertises `"loaded"`, the proxy's jit_load fallback is `noderec.DefaultJITLoad`, vLLM slot overrides and proxy residency use the served name (`modelsByEngine`), and a downloaded-but-not-served vLLM model is never eligible (`TestLocalVLLMDownloadedOnlyNeverEligible`). `daemon.go`, `directory.go`, `enrich_hosts.go`, `registry.go`, `controllifecycle.go`, `broker.go` and `discovery.go` have no inline edit outside S4/S8, S5, S8, E10, E14 and S6, so their legacy records are converted; `status.go`, `connreuse_test.go`, `refresh_test.go` and `discovery_test.go` are NVIDIA verbatim and leave the surface (`statusNoWait` moves to local_executor.go, `TestDirectoryRelaysSlotCapacity` to local_s6_test.go).

### S8
files: services/nvpair-node-scanner/enrich_hosts.go, services/nvpair-node-scanner/daemon.go
kind: hook
recipe: Take upstream enrich_hosts.go and daemon.go (NVIDIA's #181 modelInventory, applyTo, fetchModels and 4-argument applyModels verbatim; no signature change, no test call-site edit); add `local modelInventoryExt // fork:S8` as the last modelInventory field; in fetchModels add `// fork:S8` then `localModelsWire` as the last field of the decode struct and `local: d.local.ext(body.localModelsWire), // fork:S8` as the last field of the returned literal; types and ext in local_capacity.go. observedAt lives in modelInventoryExt.observedAt, stamped at fetch time by a non-nil localScanState (0 with nil state), so NVIDIA's lastInventory cache carries it with the inventory.
Tests: services/nvpair-node-scanner . -run 'TestS8_Nil|TestLocalDownloadedChangeKeepsCapacity|TestRefreshSlotChangeReportsChange'

### S6
files: services/nvpair-ui-broker/broker.go, services/nvpair-ui-broker/discovery.go
kind: hook
recipe: Take upstream broker.go and discovery.go; add `// fork:S6` then `LocalBrokerExt` as the last AvailableNode and EnrichedNode field; set `LocalBrokerExt: localBrokerExt(n), // fork:S6` last in the directoryToEnriched literal and `LocalBrokerExt: n.LocalBrokerExt, // fork:S6` last in the toAvailable literal; type and projection in local_ext.go.
Tests: services/nvpair-ui-broker . -run 'TestS6_Nil|TestDirectoryRelaysSlotCapacity|TestLocalBrokerRelayJSONKeys'

### E10
files: services/nvpair-engine-manager/registry.go
kind: hook
recipe: Take upstream registry.go; add `// fork:E10` then `LocalActionExt`, `LocalResultExt` and `LocalMatchExt` as the last field of Action, ActionResult and ResultMatch; in Action.validate, just before upstream's result.array/result.field check, add `if err := a.localValidate(name); err != nil { // fork:E10` (returning err) and add `a.Result.Record == nil &&` to that check, marked `// fork:E10`; types, regexps and localValidate in local_registry.go.
Tests: services/nvpair-engine-manager . -run 'TestE10_Nil|TestLocalActionExtValidate|TestOptionalArgv'

### E14
files: services/nvpair-engine-manager/controllifecycle.go
kind: hook
recipe: Take upstream controllifecycle.go; in handleStart replace the start-failure `http.Error(w, err.Error(), http.StatusInternalServerError)` with `localStartError(w, err) // fork:E14` (pinned gate errors keep their status; others are the same 500); body in local_http.go.
Tests: services/nvpair-engine-manager . -run 'TestE14_Nil'

## Hook records (catch-up step 5)

NVIDIA #144 (first-byte cooldown) and #186. proxy.go is NVIDIA's with the fork's proxy behaviour behind P1-P9 (bodies in local_proxy.go, local_capacity.go and local/{affinity,capacity,routing,respnorm}); a nil Proxy.local runs NVIDIA's path. NVIDIA's cooling demotion is the outer stable partition over the fork's slot-aware order (routing.Better: a cooling node never outranks a non-cooling one; all-cooling falls back to slots; a cooling affinity-bound node is rebound). proxy.go, facade.go and main.go have no inline edit outside P1-P9, so their legacy records are converted; reservation_test.go is NVIDIA verbatim again (the nil-state proxy gives NVIDIA's b,b,c) and leaves the surface; the proxy's fork-added files moved to local_*.go and local/ and leave legacy_added.

### P1
files: services/nvpair-proxy/main.go
kind: hook
recipe: Take upstream main.go; after `proxy := NewProxy(codec)` add `proxy.local = newLocalState(nil) // fork:P1`; body in local_proxy.go (directory capacity layered under NVPAIR_PROXY_CAPACITY).
Tests: services/nvpair-proxy . -run 'TestP1_Nil'

### P2
files: services/nvpair-proxy/facade.go
kind: hook
recipe: Take upstream facade.go; in replaceSubscribed, after the snapshot decodes, add `f.host.local.observeNodes(f.profile.Name, res.Nodes) // fork:P2`.
Tests: services/nvpair-proxy . -run 'TestP2_Nil|TestDirectoryResidencyAndProjection|TestLocalJIT'

### P3
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: Take upstream proxy.go; add `// fork:P3` then `local *localState` as the last Proxy field (after NVIDIA's coolUntil); state and methods in local_proxy.go and local_capacity.go.
Tests: services/nvpair-proxy . -run 'TestP3_Nil'

### P4
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In handleHTTP add `bodyBytes = p.local.rewriteBody(r, bodyBytes) // fork:P4` right after bufferBodyAndModel; body in local_proxy.go and local/respnorm.
Tests: services/nvpair-proxy . -run 'TestP4_Nil|TestCharResponsesInputNormalisedBeforeForward'

### P5
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In handleHTTP add `candidates, notLoaded := p.local.filterCandidates(f, routingModel, candidates, !lookup) // fork:P5` after the first resolveCandidates and `round, _ = p.local.filterCandidates(f, routingModel, round, true) // fork:P5` after the round re-resolution; add `rejectionBody, rejectionError, rejectionStatus = localNotLoadedRejection(...) // fork:P5` after the routingModel rejection block; add `// fork:P5` then `local bool` as the last candidate field. In resolveCandidates' add closure add `seenHost[u.Host] = p.local.claimsHost(f, model, n.ID) // fork:P5` after `seenHost[u.Host] = true`, so an ineligible ID does not claim its host. The filter runs after NVIDIA's demoteCooling and keeps its order.
Tests: services/nvpair-proxy . -run 'TestP5_Nil|TestHandleHTTP_ModelLookupIgnoresResidency|TestCharLMStudio|TestLocalFilterKeepsCoolingOrder|TestLocalSameHostLoadedIDSurvivesDedup'

### P6
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In handleHTTP add `lr := p.local.request(...) // fork:P6` after P5, replace both `p.reserveCandidate(f, ...)` calls with `p.localReserve(lr, f, ...) // fork:P6`, add `lr.releaseEarly(&resMu, &held) // fork:P6` after `committed = true`, `Affinity: lr.affinityDecision(), // fork:P6` last in the finalize RequestEvent and `"affinity", lr.affinityDecision(), // fork:P6` last in its slog; add `// fork:P6` then `Affinity` as the last RequestEvent field and `localClaim` as the last reservation field; open reserveCandidate with `if out, res, ok := p.localReserveHook(f, candidates); ok { // fork:P6`, releaseReservation (after its Lock/defer) with `if p.localDropLocked(r) { // fork:P6`, moveReservation (after its Lock/defer) with `if res, ok := p.localMoveLocked(r, nodeID); ok { // fork:P6`; add `p.local.applySnapshot(priority) // fork:P6` before setPriorityLocked's return. Cooling (NVIDIA #144) is routing.Better's outer key.
Tests: services/nvpair-proxy . -run 'TestP6_Nil|TestReserveCandidate_|TestAffinity|TestSlotScore_|TestLocalCooling|TestLocalAllCooling|TestLocalAffinityLeavesCoolingBoundNode|TestLocalAffinityDoesNotYieldToCoolingNode|TestCharLMStudioSlots'

### P7
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In handleHTTP add `lr.onMove(nodeID, held) // fork:P7` in moveHeld after moveReservation, `lr.resetTried() // fork:P7` after the round's `next = 0`, `cand, skipRound := lr.pick(round, next, cand, &resMu, &held) // fork:P7` plus its 4-line skip block after `cand := round[next]`, `lr.attemptStart(...) // fork:P7` after moveHeld(cand.id) and `lr.attemptEnd(...) // fork:P7` after cancelAttempt().
Tests: services/nvpair-proxy . -run 'TestP7_Nil|TestSlotScore_FailoverPicksFreeOverFull|TestReserveRechecksEligibility'

### P8
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In serveModelList replace `models = append(models, item.raw)` with `models = append(models, p.local.annotateModel(f, openAI, candidates, item.key, item.raw)) // fork:P8`.
Tests: services/nvpair-proxy . -run 'TestP8_Nil|TestMinContextLength|TestContextLengthReplacedOrRemoved'

### P9
files: services/nvpair-proxy/proxy.go
kind: hook
recipe: In the attempt's ModifyResponse, first add `if lr.notLoaded(attemptCtx, resp, cand.id, last, attemptStart) { // fork:P9` with `retry = true; return retrySignal{}` (4 lines); classifier in local/routing.
Tests: services/nvpair-proxy . -run 'TestP9_Nil|TestNotLoaded_|TestSlotScore_NotLoaded'
