# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Tiny tests for sync_ci_event.py against the ghstub fake `gh`. No network."""
import base64
import json
import subprocess
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ghstub  # noqa: E402

BR = "upstream-sync/20261021-b494470a03"
HEAD = "a" * 40
UP = "b" * 40
CONTENTS = {"content": base64.b64encode(f"sha={UP}\nmode=sync\n".encode()).decode(), "encoding": "base64"}


def entry(argv, stdout="", code=0):
    return {"argv": argv, "stdout": stdout, "code": code}


def responses(pr, issues=()):
    return [entry(["pr", "list"], [pr] if pr else []),
            entry(["api"], CONTENTS),
            entry(["issue", "list"], list(issues)),
            entry(["issue", "create"], "https://github.com/o/r/issues/31\n"),
            entry(["issue", "comment"]), entry(["pr", "edit"]), entry(["pr", "merge"]), entry(["label"])]


class T(unittest.TestCase):
    def run_ci(self, resp, conclusion="failure", branch=BR, repo="o/r", sha=HEAD):
        gh = ghstub.Stub(resp)
        env = gh.env()
        env.update(GH_REPO="o/r", HEAD_BRANCH=branch, HEAD_SHA=sha, HEAD_REPO=repo,
                   CONCLUSION=conclusion, RUN_URL="https://x/run/1")
        p = subprocess.run([sys.executable, str(HERE / "sync_ci_event.py")], capture_output=True, text=True, env=env)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return json.loads(p.stdout), gh

    def test_failure_cancelled_timed_out(self):
        for c in ("failure", "cancelled", "timed_out"):
            with self.subTest(c):
                pr = ghstub.pr(branch=BR, head=HEAD, enabled_at="2026-10-21T10:00:00Z")
                out, gh = self.run_ci(responses(pr), c)
                self.assertEqual(out["action"], "ci_failure")
                self.assertIn(["pr", "edit", "23", "--add-label", "ci-failing"], gh.calls("pr", "edit"))
                self.assertEqual(len(gh.calls("issue", "create")), 1)
                self.assertIn(["pr", "merge", "23", "--disable-auto"], gh.calls("pr", "merge"))
                self.assertFalse([x for x in gh.calls() if "sync-failing" in x])

    def test_disarm_before_reporting_ops(self):
        pr = ghstub.pr(branch=BR, head=HEAD, enabled_at="2026-10-21T10:00:00Z")
        out, gh = self.run_ci(responses(pr))
        order = [c[:2] for c in gh.calls() if c[0] in ("pr", "label", "issue")]
        self.assertEqual(order.index(["pr", "merge"]), 1)   # right after the PR lookup, before label/edit/issue

    def test_disarm_survives_contents_error(self):
        pr = ghstub.pr(branch=BR, head=HEAD, enabled_at="2026-10-21T10:00:00Z")
        resp = [entry(["api"], "", 1)] + responses(pr)
        gh = ghstub.Stub(resp)
        env = gh.env()
        env.update(GH_REPO="o/r", HEAD_BRANCH=BR, HEAD_SHA=HEAD, HEAD_REPO="o/r", CONCLUSION="failure", RUN_URL="u")
        p = subprocess.run([sys.executable, str(HERE / "sync_ci_event.py")], capture_output=True, text=True, env=env)
        self.assertEqual(p.returncode, 2)
        self.assertIn(["pr", "merge", "23", "--disable-auto"], gh.calls("pr", "merge"))

    def test_ambiguous_or_other_base_noop(self):
        for prs in ([ghstub.pr(branch=BR, head=HEAD), ghstub.pr(number=24, branch=BR, head=HEAD)],
                    [ghstub.pr(branch=BR, head=HEAD, base="main")]):
            resp = responses(None)
            resp[0] = entry(["pr", "list"], prs)
            out, gh = self.run_ci(resp)
            self.assertEqual(out["action"], "noop")
            self.assertEqual(gh.calls("pr", "merge"), [])

    def test_failure_not_enrolled_skips_disable_auto(self):
        out, gh = self.run_ci(responses(ghstub.pr(branch=BR, head=HEAD)))
        self.assertEqual(gh.calls("pr", "merge"), [])

    def test_success_same_head_removes_label_and_comments(self):
        pr = ghstub.pr(branch=BR, head=HEAD, labels=("upstream-sync", "ci-failing"))
        issue = {"number": 31, "body": f"x\n<!-- upstream-sync: {BR} ci_failure -->\n"}
        out, gh = self.run_ci(responses(pr, [issue]), "success")
        self.assertEqual(out["action"], "ci_success")
        self.assertEqual(gh.calls("pr", "edit"), [["pr", "edit", "23", "--remove-label", "ci-failing"]])
        self.assertEqual(len(gh.calls("issue", "comment", "31")), 1)

    def test_stale_head_noop(self):
        out, gh = self.run_ci(responses(ghstub.pr(branch=BR, head="c" * 40)))
        self.assertEqual((out["action"], out["reason"]), ("noop", "stale head"))
        self.assertEqual(gh.calls("pr", "edit") + gh.calls("issue") + gh.calls("api"), [])

    def test_fork_pr_noop(self):
        out, gh = self.run_ci(responses(ghstub.pr(branch=BR, head=HEAD)), repo="evil/r")
        self.assertEqual(out["action"], "noop")
        self.assertEqual(gh.calls(), [])

    def test_wrong_prefix_noop(self):
        out, gh = self.run_ci(responses(ghstub.pr(branch="feature/x", head=HEAD)), branch="feature/x")
        self.assertEqual(out["action"], "noop")
        self.assertEqual(gh.calls(), [])


if __name__ == "__main__":
    unittest.main()
