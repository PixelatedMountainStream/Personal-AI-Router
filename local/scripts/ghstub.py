# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Test support: a fake `gh` on PATH. Stdlib only, no network.

    with ghstub.Stub([{"argv": ["pr", "view", "23"], "stdout": {...}}]) as gh:
        subprocess.run([sys.executable, "sync_gh.py", ...])      # inherits PATH + env
        gh.calls()                                               # [["pr", "view", "23", "--json", ...], ...]

Responses (a JSON list, or the GH_STUB_RESPONSES file it is written to): each entry is
{"argv": [prefix...], "stdout": str|object, "stderr": str, "code": int}. The entry with the longest
argv prefix that matches the call wins (same prefix: first listed). `stdout` that is not a string is
JSON-encoded. No match -> exit 1 with "ghstub: no response" on stderr (and the call is still logged).
Env: GH_STUB_RESPONSES (responses file), GH_STUB_LOG (JSON-lines argv log). `not_found()` builds the
entry gh produces for a 404 from `gh api`. Shapes in `samples` mirror real `gh --json` / REST output.
"""
import json
import os
import stat
import sys
import tempfile
from pathlib import Path

FAKE_GH = '''#!{python}
import json, os, sys
argv = sys.argv[1:]
with open(os.environ["GH_STUB_LOG"], "a") as f:
    f.write(json.dumps(argv) + "\\n")
with open(os.environ["GH_STUB_RESPONSES"]) as f:
    entries = json.load(f)
best = None
for e in entries:
    p = e["argv"]
    if argv[:len(p)] == p and (best is None or len(p) > len(best["argv"])):
        best = e
if best is None:
    sys.stderr.write("ghstub: no response for " + json.dumps(argv) + "\\n")
    sys.exit(1)
out = best.get("stdout", "")
sys.stdout.write(out if isinstance(out, str) else json.dumps(out))
sys.stderr.write(best.get("stderr", ""))
sys.exit(best.get("code", 0))
'''


def not_found(*argv):
    """Response entry for `gh api <path>` returning HTTP 404 (what the sync PAT gets for protection)."""
    return {"argv": list(argv), "code": 1, "stdout": {"message": "Not Found", "status": "404"},
            "stderr": "gh: Not Found (HTTP 404)\n"}


class Stub:
    def __init__(self, responses, directory=None):
        self.dir = Path(directory or tempfile.mkdtemp(prefix="ghstub-"))
        self.responses = self.dir / "responses.json"
        self.log = self.dir / "calls.log"
        self.responses.write_text(json.dumps(responses))
        self.log.write_text("")
        gh = self.dir / "gh"
        gh.write_text(FAKE_GH.format(python=sys.executable))
        gh.chmod(gh.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        self._saved = None

    def env(self):
        """Environment (copy of os.environ) with the stub first on PATH."""
        e = dict(os.environ)
        e["PATH"] = f"{self.dir}{os.pathsep}{e.get('PATH', '')}"
        e["GH_STUB_RESPONSES"] = str(self.responses)
        e["GH_STUB_LOG"] = str(self.log)
        return e

    def set_responses(self, responses):
        self.responses.write_text(json.dumps(responses))

    def calls(self, *prefix):
        return calls(self.log, *prefix)

    def __enter__(self):
        self._saved = dict(os.environ)
        os.environ.update(self.env())
        return self

    def __exit__(self, *exc):
        os.environ.clear()
        os.environ.update(self._saved)


def calls(log=None, *prefix):
    """argv lists (without the leading `gh`) logged so far, optionally only those starting with prefix."""
    path = log or os.environ["GH_STUB_LOG"]
    out = [json.loads(line) for line in Path(path).read_text().splitlines() if line]
    return [c for c in out if c[:len(prefix)] == list(prefix)]


# Real shapes (checked against PixelatedMountainStream/Personal-AI-Router on 2026-10-21; protection is
# GitHub's documented shape because the sync PAT gets 404 there).
def label(name):
    return {"id": "LA_x", "name": name, "description": "", "color": "0e8a16"}


def pr(number=23, branch="upstream-sync/20261021-b494470a03", draft=False, labels=("upstream-sync",),
       enabled_at=None, cross=False, head="f" * 40, state="OPEN"):
    return {"number": number, "headRefName": branch, "headRefOid": head, "isDraft": draft,
            "labels": [label(n) for n in labels], "isCrossRepository": cross, "state": state,
            "autoMergeRequest": None if not enabled_at else {
                "authorEmail": None, "commitBody": None, "commitHeadline": None,
                "mergeMethod": "MERGE", "enabledAt": enabled_at, "enabledBy": {"login": "bot"}}}


def repo(auto=True, merge_commit=True):
    return {"full_name": "PixelatedMountainStream/Personal-AI-Router", "default_branch": "develop",
            "allow_auto_merge": auto, "allow_merge_commit": merge_commit, "allow_squash_merge": True}


def protection(checks, strict=False, linear=False, reviews=False):
    p = {"required_status_checks": {"strict": strict, "contexts": list(checks),
                                    "checks": [{"context": c, "app_id": 15368} for c in checks]},
         "required_linear_history": {"enabled": linear}}
    if reviews:
        p["required_pull_request_reviews"] = {"required_approving_review_count": 1}
    return p


def rule(kind):
    return {"type": kind, "ruleset_source_type": "Repository", "ruleset_source": "o/r", "ruleset_id": 1}
