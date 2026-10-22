#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Handle a finished `Local CI` run on an upstream-sync PR (plan section 4.5). Stdlib only; honours GH_REPO.

All inputs come from the environment (set by upstream-sync-ci.yml, never interpolated into a shell):
HEAD_BRANCH, HEAD_SHA, HEAD_REPO, CONCLUSION, RUN_URL, GH_REPO. Flags of the same names override them.
failure|cancelled|timed_out -> label `ci-failing`, `<branch> ci_failure` issue, `--disable-auto` if enrolled.
success -> remove `ci-failing` if present and comment on the marker issue. Never touches `sync-failing`.
Prints ONE JSON object {ok, step, action, ...}. Exit codes: 0 ok / noop, 2 a gh call failed.
"""
import argparse
import base64
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
PREFIX = "upstream-sync/"
LABEL = "ci-failing"
BAD = ("failure", "cancelled", "timed_out")
PR_FIELDS = "number,headRefOid,labels,autoMergeRequest,isCrossRepository"


class GhError(Exception):
    pass


def gh(*args, check=True):
    p = subprocess.run(["gh", *args], capture_output=True, text=True)
    if check and p.returncode:
        raise GhError(f"gh {' '.join(args[:3])} failed: {(p.stderr or p.stdout).strip()[:300]}")
    return p


def gh_json(*args):
    try:
        return json.loads(gh(*args).stdout or "null")
    except json.JSONDecodeError as e:
        raise GhError(f"gh {' '.join(args[:3])}: bad JSON: {e}")


def emit(ok, code, action, **kw):
    print(json.dumps({"ok": ok, "step": "ci-event", "action": action, **kw}, indent=2))
    return code


def noop(reason, **kw):
    return emit(True, 0, "noop", reason=reason, **kw)


def read_sha(fork, ref):
    """`sha=` line of local/UPSTREAM_VERSION.txt at `ref` (contents API: JSON+base64, or raw text)."""
    out = gh("api", f"repos/{fork}/contents/local/UPSTREAM_VERSION.txt?ref={ref}").stdout
    try:
        d = json.loads(out)
        text = base64.b64decode(d["content"]).decode() if isinstance(d, dict) else out
    except (json.JSONDecodeError, KeyError, ValueError):
        text = out
    for line in text.splitlines():
        if line.startswith("sha="):
            return line[4:].strip()
    raise GhError("no sha= line in local/UPSTREAM_VERSION.txt")


def marker_issue(branch):
    m = f"<!-- upstream-sync: {branch} ci_failure -->"
    issues = gh_json("issue", "list", "--label", "upstream-sync", "--state", "open", "--limit", "200",
                     "--json", "number,body") or []
    return next((i["number"] for i in issues if m in (i.get("body") or "")), None)


def run(a):
    fork = os.environ.get("GH_REPO") or "{owner}/{repo}"
    if not a.head_branch.startswith(PREFIX):
        return noop("branch is not an upstream-sync branch")
    if a.head_repo != os.environ.get("GH_REPO"):
        return noop("head repo is not the fork")
    prs = gh_json("pr", "list", "--head", a.head_branch, "--state", "open", "--json", PR_FIELDS) or []
    prs = [p for p in prs if not p.get("isCrossRepository")]
    if not prs:
        return noop("no open PR for branch")
    pr = max(prs, key=lambda p: p["number"])
    n = pr["number"]
    if pr["headRefOid"] != a.head_sha:
        return noop("stale head", pr=n, pr_head=pr["headRefOid"])
    sha = read_sha(fork, a.head_sha)
    has_label = LABEL in [x["name"] for x in pr.get("labels", [])]
    if a.conclusion in BAD:
        gh("label", "create", LABEL, "--color", "d93f0b", "--description",
           "Local CI failed on an upstream-sync PR", "--force", check=False)
        gh("pr", "edit", str(n), "--add-label", LABEL)
        title = f"Upstream sync needs attention: {a.head_branch} [ci_failure]"
        body = (f"Local CI concluded `{a.conclusion}` on PR #{n} ({a.head_branch}) at head {a.head_sha}.\n\n"
                f"Run: {a.run_url}\n")
        with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
            f.write(body)
        try:
            p = subprocess.run([sys.executable, str(HERE / "sync_gh.py"), "issue", "--branch", a.head_branch,
                                "--outcome", "ci_failure", "--title", title, "--body-file", f.name,
                                "--sha", sha, "--run-url", a.run_url], capture_output=True, text=True)
        finally:
            os.unlink(f.name)
        if p.returncode:
            raise GhError(f"sync_gh.py issue failed: {(p.stdout or p.stderr).strip()[:300]}")
        disabled = bool(pr.get("autoMergeRequest"))
        if disabled:
            gh("pr", "merge", str(n), "--disable-auto")
        return emit(True, 0, "ci_failure", pr=n, conclusion=a.conclusion, sha=sha, label_added=True,
                    auto_merge_disabled=disabled)
    if a.conclusion == "success":
        if has_label:
            gh("pr", "edit", str(n), "--remove-label", LABEL)
        issue = marker_issue(a.head_branch) if has_label else None
        if issue:
            gh("issue", "comment", str(issue), "--body",
               f"Local CI now passes on the same head {a.head_sha}.\n\nRun: {a.run_url}")
        return emit(True, 0, "ci_success", pr=n, sha=sha, label_removed=has_label, issue=issue)
    return noop(f"conclusion {a.conclusion!r} ignored", pr=n)


def main(argv=None):
    e = os.environ.get
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for flag, var in (("head-branch", "HEAD_BRANCH"), ("head-sha", "HEAD_SHA"), ("conclusion", "CONCLUSION"),
                      ("run-url", "RUN_URL"), ("head-repo", "HEAD_REPO")):
        ap.add_argument(f"--{flag}", default=e(var, ""))
    a = ap.parse_args(argv)
    try:
        return run(a)
    except GhError as ex:
        return emit(False, 2, "error", escalate=True, error=str(ex))


if __name__ == "__main__":
    sys.exit(main())
