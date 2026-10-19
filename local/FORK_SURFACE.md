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

### L-services-nvpair-engine-manager-models-go
files: services/nvpair-engine-manager/models.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

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

### L-services-nvpair-job-scheduler-schedule-go
files: services/nvpair-job-scheduler/schedule.go
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

### L-services-shared-noderec-noderec-go
files: services/shared/noderec/noderec.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-shared-schedulerwire-schedulerwire-go
files: services/shared/schedulerwire/schedulerwire.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.

### L-services-shared-schedulerwire-schedulerwire-test-go
files: services/shared/schedulerwire/schedulerwire_test.go
kind: legacy
recipe: Pre-refactor inline fork edit; re-apply by merging the fork diff against upstream until PR-C..E convert it to a hook.
