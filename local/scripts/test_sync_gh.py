# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Tiny tests for sync_gh.py against the ghstub fake `gh`. No network."""
import json
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ghstub  # noqa: E402

CHECKS = [f"job{i}" for i in range(10)]
HEAD = "a" * 40
TMP = Path(tempfile.mkdtemp())
CONF = TMP / "sync.toml"
CONF.write_text("required_checks = %s\n" % json.dumps(CHECKS))
API = "api"
R = "repos/o/r"


def hours_ago(h):
    return (datetime.now(timezone.utc) - timedelta(hours=h)).strftime("%Y-%m-%dT%H:%M:%SZ")


def entry(argv, stdout="", code=0, stderr=""):
    return {"argv": argv, "stdout": stdout, "code": code, "stderr": stderr}


def enroll_responses(repo=None, prot="ok", rules=(), draft=False, merge_code=0):
    out = [entry([API, f"{R}"], repo or ghstub.repo()),
           entry(["pr", "view"], ghstub.pr(draft=draft)),
           entry(["pr", "merge"], code=merge_code, stderr="boom" if merge_code else ""),
           entry(["pr", "edit"]),
           entry([API, f"{R}/rules/branches/develop"], [ghstub.rule(k) for k in rules])]
    out.append(ghstub.not_found(API, f"{R}/branches/develop/protection") if prot == 404 else
               entry([API, f"{R}/branches/develop/protection"], prot if prot != "ok" else ghstub.protection(CHECKS)))
    return out


class T(unittest.TestCase):
    def run_gh(self, responses, *args, switch=None):
        gh = ghstub.Stub(responses)
        env = gh.env()
        env["GH_REPO"] = "o/r"
        env.pop("SYNC_AUTO_MERGE", None)
        if switch is not None:
            env["SYNC_AUTO_MERGE"] = switch
        p = subprocess.run([sys.executable, str(HERE / "sync_gh.py"), *args], capture_output=True, text=True,
                           env=env)
        self.assertTrue(p.stdout, p.stderr)
        self.out = json.loads(p.stdout)
        self.code = p.returncode
        self.gh = gh
        return self.out

    def enroll(self, responses, demo="", switch="1"):
        report = TMP / "report.md"
        report.write_text("# Report\n\nEnrollment: pending\n")
        return self.run_gh(responses, "enroll", "--pr", "23", "--head", HEAD, "--demo-ref", demo,
                           "--body", str(report), "--config", str(CONF), switch=switch)

    def issue(self, issues):
        body = TMP / "body.md"
        body.write_text("details")
        return self.run_gh([entry(["issue", "list"], issues), entry(["issue", "comment"]),
                            entry(["issue", "create"], "https://github.com/o/r/issues/31\n")],
                           "issue", "--branch", "upstream-sync/x", "--outcome", "escalate", "--title", "T",
                           "--body-file", str(body), "--sha", "abc", "--run-url", "http://run")

    def test_issue_marker_present_comments(self):
        o = self.issue([{"number": 7, "body": "x\n<!-- upstream-sync: upstream-sync/x escalate -->"}])
        self.assertEqual((o["action"], o["number"]), ("commented", 7))
        self.assertEqual(self.gh.calls("issue", "create"), [])

    def test_issue_marker_absent_creates(self):
        o = self.issue([{"number": 7, "body": "<!-- upstream-sync: upstream-sync/x other -->"}])
        self.assertEqual((o["action"], o["number"]), ("created", 31))
        self.assertEqual(self.gh.calls("issue", "comment"), [])

    def test_issue_kill_switch_only_files_none(self):
        o = self.enroll(enroll_responses(), switch=None)
        self.assertEqual((o["outcome"], o["kill_switch_only"]), ("enroll_refused", True))
        self.assertEqual(self.code, 1)
        self.assertEqual(self.gh.calls("issue"), [])   # enroll itself never files; the workflow skips `issue`

    def test_enroll_repo_allow_auto_merge_false(self):
        o = self.enroll(enroll_responses(repo=ghstub.repo(auto=False)))
        self.assertEqual(o["outcome"], "enroll_refused")
        self.assertFalse(o["kill_switch_only"])
        self.assertEqual(self.gh.calls("pr", "merge", "23", "--auto"), [])

    def test_enroll_protection_cases(self):
        self.assertEqual(self.enroll(enroll_responses())["outcome"], "enrolled")
        self.assertEqual(self.gh.calls("pr", "merge")[0],
                         ["pr", "merge", "23", "--auto", "--merge", "--match-head-commit", HEAD])
        self.assertIn(f"enrolled at {HEAD}", (TMP / "report.md").read_text())
        for prot in (ghstub.protection(CHECKS[:-1]), ghstub.protection(CHECKS, linear=True),
                     ghstub.protection(CHECKS, strict=True), ghstub.protection(CHECKS, reviews=True), 404):
            self.assertEqual(self.enroll(enroll_responses(prot=prot))["outcome"], "enroll_refused", prot)

    def test_enroll_rules_cases(self):
        self.assertEqual(self.enroll(enroll_responses(rules=["merge_queue"]))["outcome"], "enroll_refused")
        rs = enroll_responses()
        rs[4] = ghstub.not_found(API, f"{R}/rules/branches/develop")
        self.assertEqual(self.enroll(rs)["outcome"], "enroll_refused")

    def test_kill_switch_combinations(self):
        for demo, sw, want in [("r", "demo", "enrolled"), ("", "1", "enrolled"), ("r", "1", "enroll_refused"),
                               ("", "demo", "enroll_refused"), ("", None, "enroll_refused"),
                               ("", "yes", "enroll_refused")]:
            o = self.enroll(enroll_responses(), demo=demo, switch=sw)
            self.assertEqual(o["outcome"], want, (demo, sw))
            self.assertEqual(o["kill_switch_only"], want == "enroll_refused", (demo, sw))

    def test_disable_auto_called_on_refusal(self):
        rs = enroll_responses(repo=ghstub.repo(auto=False))
        rs[1] = entry(["pr", "view"], ghstub.pr(enabled_at=hours_ago(1)))
        self.enroll(rs)
        self.assertEqual(self.gh.calls("pr", "merge"), [["pr", "merge", "23", "--disable-auto"]])
        self.run_gh([entry(["pr", "view"], ghstub.pr())], "disable-auto", "--pr", "23")
        self.assertEqual(self.gh.calls("pr", "merge"), [])   # idempotent: nothing to disable

    def test_enroll_refuses_and_disarms_on_veto_labels(self):
        for lab in ("workflow-review", "sync-failing", "ci-failing"):
            rs = enroll_responses()
            rs[1] = entry(["pr", "view"], ghstub.pr(labels=("upstream-sync", lab), enabled_at=hours_ago(1)))
            o = self.enroll(rs)
            self.assertEqual(o["outcome"], "enroll_refused", lab)
            self.assertIn(f"PR carries the {lab} label", o["reasons"])
            self.assertEqual(self.gh.calls("pr", "merge", "23", "--auto"), [])
            self.assertTrue(self.gh.calls("pr", "merge", "23", "--disable-auto"), lab)

    def test_find_pr_by_exact_branch(self):
        prs = [json.loads(json.dumps(ghstub.pr(number=n, branch=b))) for n, b in
               ((5, "upstream-sync/a"), (9, "upstream-sync/b"))]
        o = self.run_gh([entry(["pr", "list"], prs)], "find-pr", "--branch", "upstream-sync/a")
        self.assertEqual(o["number"], 5)
        o = self.run_gh([entry(["pr", "list"], prs)], "find-pr", "--branch", "upstream-sync/zzz")
        self.assertIsNone(o["number"])

    def test_enroll_refuses_other_base_or_cross_repo(self):
        for kw in ({"base": "main"}, {"cross": True}):
            rs = enroll_responses()
            rs[1] = entry(["pr", "view"], ghstub.pr(**kw))
            o = self.enroll(rs)
            self.assertEqual(o["outcome"], "enroll_refused", kw)
            self.assertEqual(self.gh.calls("pr", "merge", "23", "--auto"), [])

    def test_find_pr_filters_base_develop(self):
        self.run_gh([entry(["pr", "list"], [])], "find-pr")
        self.assertEqual(self.gh.calls("pr", "list")[0][2:4], ["--base", "develop"])

    def test_enroll_missing_config_refuses_with_reason(self):
        report = TMP / "report.md"
        report.write_text("Enrollment: pending\n")
        o = self.run_gh(enroll_responses(), "enroll", "--pr", "23", "--head", HEAD, "--body", str(report),
                        "--config", str(TMP / "nope.toml"), switch="1")
        self.assertEqual(o["outcome"], "enroll_refused")
        self.assertTrue(any("config file not found" in r for r in o["reasons"]), o["reasons"])

    def test_required_checks_read_from_explicit_config(self):
        other = TMP / "other.toml"
        other.write_text('required_checks = ["only-this"]\n')
        report = TMP / "report.md"
        report.write_text("Enrollment: pending\n")
        o = self.run_gh(enroll_responses(), "enroll", "--pr", "23", "--head", HEAD, "--body", str(report),
                        "--config", str(other), switch="1")
        self.assertEqual(o["outcome"], "enroll_refused")
        self.assertIn("only-this", " ".join(o["reasons"]))

    def test_enroll_malformed_responses_refuse(self):
        good = ghstub.protection(CHECKS)
        no_strict = json.loads(json.dumps(good)); del no_strict["required_status_checks"]["strict"]
        no_lin = json.loads(json.dumps(good)); del no_lin["required_linear_history"]
        for prot in (no_strict, no_lin, [], {"required_status_checks": None, "required_linear_history": {"enabled": False}}):
            o = self.enroll(enroll_responses(prot=prot))
            self.assertEqual(o["outcome"], "enroll_refused")
            self.assertIn("malformed branches/develop/protection", o["reasons"])
        for rules in (None, {"a": 1}, ["x"]):
            rs = enroll_responses()
            rs[4] = entry([API, f"{R}/rules/branches/develop"], rules)
            o = self.enroll(rs)
            self.assertIn("malformed rules/branches/develop", o["reasons"], rules)

    def test_enroll_strict_validation_refuses_and_disarms(self):
        rs = enroll_responses(); rs[4] = entry([API, f"{R}/rules/branches/develop"], [{}])
        self.assertIn("malformed rules/branches/develop", self.enroll(rs)["reasons"])
        prot = ghstub.protection(CHECKS); prot["required_pull_request_reviews"] = {}
        self.assertIn("protection reviews required", self.enroll(enroll_responses(prot=prot))["reasons"])
        rs = enroll_responses(); rs[0] = entry([API, R], None)
        o = self.enroll(rs)
        self.assertEqual(o["outcome"], "enroll_refused")
        self.assertIn(f"malformed {R}", o["reasons"])
        self.assertTrue(self.gh.calls("pr", "view"))   # disarm path ran (pr view autoMergeRequest)

    def test_verify_pr(self):
        ok = self.run_gh([entry(["pr", "view"], ghstub.pr())], "verify-pr", "--pr", "23", "--branch", "upstream-sync/20261021-b494470a03")
        self.assertEqual((self.code, ok["ok"]), (0, True))
        for pr in (ghstub.pr(branch="upstream-sync/other"), ghstub.pr(base="main"), ghstub.pr(cross=True)):
            self.run_gh([entry(["pr", "view"], pr)], "verify-pr", "--pr", "23", "--branch", "upstream-sync/20261021-b494470a03")
            self.assertEqual(self.code, 1)

    def test_refusal_disarms_before_editing_body(self):
        rs = enroll_responses(repo=ghstub.repo(auto=False))
        rs[1] = entry(["pr", "view"], ghstub.pr(enabled_at=hours_ago(1)))
        self.enroll(rs)
        order = [c[:2] for c in self.gh.calls("pr")]
        self.assertLess(order.index(["pr", "merge"]), order.index(["pr", "edit"]))

    def test_find_pr_demo_in_place_or_none(self):
        demo = ghstub.pr(branch="upstream-sync-demo/1", labels=("sync-demo",))
        fork_pr = ghstub.pr(number=30, branch="upstream-sync-demo/2", labels=("sync-demo",), cross=True)
        o = self.run_gh([entry(["pr", "list"], [fork_pr, demo])], "find-pr", "--demo")
        self.assertEqual((o["number"], o["branch"]), (23, "upstream-sync-demo/1"))
        self.assertIn("sync-demo", self.gh.calls("pr", "list")[0])
        o = self.run_gh([entry(["pr", "list"], [demo])], "find-pr")   # wrong prefix for the regular lookup
        self.assertIsNone(o["number"])

    def test_stalled_over_24h_files_issue(self):
        body = [entry(["issue", "list"], []), entry(["issue", "create"], "https://x/issues/9\n")]
        o = self.run_gh([entry(["pr", "view"], ghstub.pr(enabled_at=hours_ago(30)))] + body,
                        "stalled", "--pr", "23", "--run-url", "u")
        self.assertTrue(o["stalled"])
        self.assertEqual(len(self.gh.calls("issue", "create")), 1)
        o = self.run_gh([entry(["pr", "view"], ghstub.pr(enabled_at=hours_ago(2)))] + body,
                        "stalled", "--pr", "23", "--run-url", "u")
        self.assertFalse(o["stalled"])
        self.assertEqual(self.gh.calls("issue"), [])

    def test_ready_removes_sync_failing(self):
        o = self.run_gh([entry(["pr", "view"], ghstub.pr(draft=True, labels=("upstream-sync", "sync-failing"))),
                         entry(["pr", "ready"]), entry(["pr", "edit"])], "ready", "--pr", "23")
        self.assertEqual(self.gh.calls("pr", "ready"), [["pr", "ready", "23"]])
        self.assertEqual(self.gh.calls("pr", "edit"), [["pr", "edit", "23", "--remove-label", "sync-failing"]])
        self.assertIn("draft_after", o)

    def test_head_pinned_veto_blocks_ready_and_enroll_until_new_head(self):
        mark = lambda sha, o="escalate": f"report\n<!-- upstream-sync-veto: {sha} {o} -->\n"
        same = ghstub.pr(draft=True, labels=("upstream-sync", "sync-failing"), body=mark("f" * 40), enabled_at=hours_ago(1))
        o = self.run_gh([entry(["pr", "view"], same), entry(["pr", "ready"]), entry(["pr", "edit"])], "ready", "--pr", "23")
        self.assertEqual((self.code, o["vetoed"], o["label_removed"]), (1, "escalate", False))
        self.assertEqual(self.gh.calls("pr", "edit"), [])
        self.assertEqual(self.gh.calls("pr", "ready"), [])
        rs = enroll_responses()
        rs[1] = entry(["pr", "view"], same)
        o = self.enroll(rs)
        self.assertEqual(o["outcome"], "enroll_refused")
        self.assertTrue(any("vetoed" in r for r in o["reasons"]))
        self.assertEqual(self.gh.calls("pr", "merge", "23", "--auto"), [])
        self.assertTrue(self.gh.calls("pr", "merge", "23", "--disable-auto"))
        o = self.run_gh([entry(["pr", "view"], same)], "veto", "--pr", "23")
        self.assertEqual((o["outcome"], o["head_sha"]), ("escalate", "f" * 40))
        o = self.run_gh([entry(["pr", "view"], same)], "veto", "--pr", "23", "--cand", "b" * 40)
        self.assertEqual(o["outcome"], "")   # this run pushes a new head
        # new head (human push): marker pinned to an older sha, and malformed markers, do not veto
        for body in (mark("e" * 40), mark("f" * 40, "bogus"), mark("f" * 39)):
            new = ghstub.pr(labels=("upstream-sync", "sync-failing"), body=body)
            o = self.run_gh([entry(["pr", "view"], new), entry(["pr", "edit"])], "ready", "--pr", "23")
            self.assertEqual((self.code, o["label_removed"]), (0, True))
            self.assertEqual(self.run_gh([entry(["pr", "view"], new)], "veto", "--pr", "23")["outcome"], "")
            rs = enroll_responses()
            rs[1] = entry(["pr", "view"], new)
            self.assertFalse(any("vetoed" in r for r in self.enroll(rs)["reasons"]))

    def test_sweep_closes_merged_and_workflow_review(self):
        mk = lambda n, b, o="escalate": {"number": n, "body": f"<!-- upstream-sync: {b} {o} -->"}  # noqa: E731
        issues = [mk(1, "upstream-sync/m"), mk(2, "upstream-sync/open"), mk(3, "workflow-review", "workflow_review")]
        rs = [entry(["issue", "list"], issues), entry(["issue", "close"]),
              entry(["pr", "list", "--head", "upstream-sync/m"], [{"number": 5, "state": "MERGED"}]),
              entry(["pr", "list", "--head", "upstream-sync/open"], [{"number": 6, "state": "OPEN"}])]
        self.assertEqual(self.run_gh(rs, "sweep", "--no-held-drift")["closed"], [1, 3])
        self.assertEqual(self.run_gh(rs, "sweep")["closed"], [1])


if __name__ == "__main__":
    unittest.main()
