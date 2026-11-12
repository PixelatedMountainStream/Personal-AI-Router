#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Patch series tooling (PLAN-pair-upstream-sync.md section 5.2). The series in
# local/patches/ is a rebuild record, not a replacement for merges.
#
#   reapply_patches.sh <upstream-sha> [<patch-dir>]
#       Check out <upstream-sha> (detached, clean tree required) and apply the
#       series with `git am -3`. Best-effort on an arbitrary upstream commit.
#   reapply_patches.sh --check <candidate-ref>
#       Advisory consistency check: in a scratch worktree at the candidate's own
#       `sha=` pin (local/UPSTREAM_VERSION.txt), apply the candidate's series and
#       require `git diff --exit-code` against the candidate over the path set:
#       files the patches touch, minus local/patches/ and files any `legacy`
#       record in local/FORK_SURFACE.md covers. An am conflict fails the check.
set -euo pipefail

export GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-pair patch-check}"
export GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-patch-check@users.noreply.github.com}"

die() { echo "reapply_patches: $*" >&2; exit 2; }

apply_series() { # <patch-dir>
    local patches=("$1"/*.patch)
    [ -e "${patches[0]}" ] || { echo "reapply_patches: empty series"; return 0; }
    git am -3 --keep-cr "${patches[@]}" || { git am --abort >/dev/null 2>&1 || true; return 1; }
}

if [ "${1:-}" != "--check" ]; then
    [ $# -ge 1 ] || die "usage: reapply_patches.sh <upstream-sha> [<patch-dir>] | --check <candidate-ref>"
    sha=$1
    src=${2:-"$(git rev-parse --show-toplevel)/local/patches"}
    [ -z "$(git status --porcelain)" ] || die "working tree not clean"
    tmp=$(mktemp -d)
    cp "$src"/*.patch "$tmp"/ 2>/dev/null || true
    git checkout -q --detach "$sha"
    apply_series "$tmp" || { echo "reapply_patches: series does not apply onto $sha" >&2; exit 1; }
    echo "reapply_patches: series applied onto $sha"
    exit 0
fi

[ $# -eq 2 ] || die "usage: reapply_patches.sh --check <candidate-ref>"
cand=$(git rev-parse --verify "$2^{commit}") || die "bad candidate $2"
pin=$(git show "$cand:local/UPSTREAM_VERSION.txt" | sed -n 's/^sha=//p')
[ -n "$pin" ] || die "no sha= in local/UPSTREAM_VERSION.txt"
tmp=$(mktemp -d)
wt="$tmp/wt"
cleanup() { git worktree remove --force "$wt" >/dev/null 2>&1 || true; }
trap cleanup EXIT
mkdir "$tmp/patches"
git ls-tree --name-only "$cand" local/patches/ | grep '\.patch$' | while read -r p; do
    git show "$cand:$p" > "$tmp/patches/$(basename "$p")"
done
legacy=$(git show "$cand:local/FORK_SURFACE.md" 2>/dev/null | awk '
    /^### /{files=""; next}
    /^files:/{sub(/^files:[ \t]*/, ""); files=$0; next}
    /^kind:[ \t]*legacy/{n=split(files, f, /[ \t]*,[ \t]*/); for (i=1;i<=n;i++) print f[i]}')
paths=()
while read -r p; do
    [ -n "$p" ] || continue
    case "$p" in local/patches/*) continue ;; esac
    grep -qxF -- "$p" <<<"$legacy" && continue
    paths+=("$p")
done < <(cat "$tmp"/patches/*.patch 2>/dev/null | sed -n 's|^diff --git a/.* b/||p' | sort -u)
if [ ${#paths[@]} -eq 0 ]; then
    echo "patch-check: path set empty (every patched file is under a legacy record); pass"
    exit 0
fi
git worktree add -q --detach "$wt" "$pin"
if ! (cd "$wt" && apply_series "$tmp/patches"); then
    echo "patch-check: FAIL: series does not apply onto pin $pin" >&2
    exit 1
fi
if ! git -C "$wt" diff --exit-code HEAD "$cand" -- "${paths[@]}"; then
    echo "patch-check: FAIL: series on $pin differs from $cand over: ${paths[*]}" >&2
    exit 1
fi
echo "patch-check: ok (${#paths[@]} paths): ${paths[*]}"
