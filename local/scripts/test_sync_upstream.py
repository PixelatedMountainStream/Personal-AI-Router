# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Tiny fixture tests for sync_upstream.py on temp git repos. No gh, no network."""
import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

# Self-contained git: a CI runner has no user.name/email, and init.defaultBranch may differ. Inherited by the
# script under test, whose merge/commit calls need an identity.
os.environ.update({"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t",
                   "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_COUNT": "1",
                   "GIT_CONFIG_KEY_0": "init.defaultBranch", "GIT_CONFIG_VALUE_0": "main"})
os.environ["HOME"] = tempfile.mkdtemp()

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "sync_upstream.py"
sys.path.insert(0, str(HERE))
import sync_upstream as su  # noqa: E402

CONF = "upstream_ref = 'develop'\ntag_pattern = '^v\\d+\\.\\d+\\.\\d+$'\n"
WF = "name: x\non:\n  push:\n    branches: [main]\n\njobs:\n  a:\n    runs-on: ubuntu-latest\n"


def git(path, *a):
    return subprocess.run(["git", "-C", str(path), "-c", "user.name=t", "-c", "user.email=t@t",
                           "-c", "commit.gpgsign=false", *a], check=True, capture_output=True, text=True).stdout.strip()


class Out(dict):
    """JSON result whose missing keys fail with the subprocess stderr and next_action, not a bare KeyError."""
    def __init__(self, d, stderr):
        super().__init__(d)
        self.stderr = stderr

    def __missing__(self, key):
        raise AssertionError(f"missing {key!r}; next_action={self.get('next_action')!r}; stderr={self.stderr[-800:]!r}")


class Sync(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.up, self.fork = t / "up", t / "fork"
        self.up.mkdir()
        git(self.up, "init", "-q", "-b", "develop")
        self.c1 = self.up_commit("a.txt", "1\n", {".github/workflows/x.yml": WF, ".github/workflows/ci.yml": WF})
        git(t, "clone", "-q", str(self.up), "fork")
        git(self.fork, "remote", "add", "upstream", str(self.up))
        self.put(self.fork, "local/upstream-sync.toml", CONF)
        self.put(self.fork, "local/fork-surface.toml", "allow = []\n")
        self.put(self.fork, "local/neutralized.txt", "")
        self.set_version(self.c1, "v1.0.0")
        self.fetch()

    def tearDown(self):
        self.tmp.cleanup()

    def put(self, repo, rel, text):
        p = Path(repo) / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def up_commit(self, rel, text, more=None, tag=None):
        for r, t in {rel: text, **(more or {})}.items():
            self.put(self.up, r, t)
        git(self.up, "add", "-A")
        git(self.up, "commit", "-qm", "c")
        if tag:
            git(self.up, "tag", tag)
        return git(self.up, "rev-parse", "HEAD")

    def set_version(self, sha, tag):
        self.put(self.fork, "local/UPSTREAM_VERSION.txt", f"sha={sha}\nlast_release_tag={tag}\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "version")

    def fetch(self):
        git(self.fork, "fetch", "-q", "upstream", "--tags")

    def sync(self, *args):
        p = subprocess.run([sys.executable, str(SCRIPT), "--root", str(self.fork), *args],
                           capture_output=True, text=True)
        try:
            out = json.loads(p.stdout)
        except ValueError:
            self.fail(f"{args}: no JSON (exit {p.returncode}); stderr: {p.stderr[-800:]}")
        return p.returncode, Out(out, p.stderr)

    def version(self):
        return (self.fork / "local/UPSTREAM_VERSION.txt").read_text().split()

    def report(self, st, tmp_name="st.json"):
        f = Path(self.tmp.name) / tmp_name
        f.write_text(json.dumps(st))
        return self.sync("report", "--status", str(f))[1]

    def test_tag_in_range_labelled_and_recorded(self):
        c2 = self.up_commit("a.txt", "2\n", tag="v1.1.0")
        self.fetch()
        code, st = self.sync("status")
        self.assertEqual((code, st["mode"], st["tags"], st["release"]), (0, "sync", ["v1.1.0"], True))
        code, mg = self.sync("merge", "--date", "20261019")
        self.assertEqual((code, mg["branch"]), (0, f"upstream-sync/20261019-{c2[:10]}"))
        self.assertEqual(self.version(), [f"sha={c2}", "last_release_tag=v1.1.0"])
        r = self.report(st)
        self.assertIn("upstream-release", r["labels"])
        self.assertTrue(r["title"].startswith("Upstream release: v1.1.0"))

    def test_tag_already_in_develop_gets_bump_only(self):
        git(self.up, "tag", "v1.0.5", self.c1)
        self.fetch()
        code, st = self.sync("status")
        self.assertEqual((st["mode"], st["new"], st["tags"]), ("bump", False, ["v1.0.5"]))
        code, mg = self.sync("merge", "--bump-only", "--date", "20261019")
        self.assertEqual(code, 0)
        self.assertEqual(self.version(), [f"sha={self.c1}", "last_release_tag=v1.0.5"])
        self.assertIn("upstream-release", self.report(st)["labels"])

    def test_two_tags_record_newest_and_list_both(self):
        self.up_commit("a.txt", "2\n", tag="v1.1.0")
        c3 = self.up_commit("a.txt", "3\n", tag="v1.2.0")
        self.fetch()
        code, st = self.sync("status")
        self.assertEqual(st["tags"], ["v1.1.0", "v1.2.0"])
        self.assertEqual(self.sync("merge", "--date", "20261019")[0], 0)
        self.assertEqual(self.version(), [f"sha={c3}", "last_release_tag=v1.2.0"])
        self.sync("report", "--status", self._dump(st), "--out", str(Path(self.tmp.name) / "r.md"))
        md = (Path(self.tmp.name) / "r.md").read_text()
        self.assertIn("- v1.1.0", md)
        self.assertIn("- v1.2.0", md)

    def _dump(self, st):
        f = Path(self.tmp.name) / "s.json"
        f.write_text(json.dumps(st))
        return str(f)

    def test_no_tag_is_noop(self):
        code, st = self.sync("status")
        self.assertEqual((code, st["mode"], st["new"], st["release"]), (0, "none", False, False))
        code, mg = self.sync("merge", "--bump-only")
        self.assertEqual(code, 0)
        self.assertEqual(self.version(), [f"sha={self.c1}", "last_release_tag=v1.0.0"])

    def test_sha_regression_exits_3(self):
        c2 = self.up_commit("a.txt", "2\n")
        self.fetch()
        self.set_version(c2, "v1.0.0")
        for cmd in ("status", "merge"):
            code, out = self.sync(cmd, "--ref", self.c1)
            self.assertEqual(code, 3, cmd)
        self.assertEqual(self.version()[0], f"sha={c2}")

    def test_upstream_range_adding_ignored_path_is_flagged(self):
        self.put(self.fork, "local/fork-surface.toml", 'ignore = ["local/**"]\n')
        c2 = self.up_commit("a.txt", "2\n", {"local/sneaky.txt": "x\n"})
        self.fetch()
        self.assertEqual(su.added_ignored(self.fork, self.c1, c2),
                         [{"kind": "upstream_adds_ignored_path", "path": "local/sneaky.txt"}])

    def test_conflict_is_committed_on_branch_with_draft_and_issue(self):
        self.put(self.fork, "local/fork-surface.toml", 'allow = ["a.txt"]\n')
        self.put(self.fork, "a.txt", "fork\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "fork edit")
        self.up_commit("a.txt", "upstream\n")
        self.fetch()
        code, st = self.sync("status")
        code, mg = self.sync("merge", "--date", "20261019")
        self.assertEqual((code, mg["committed"], mg["conflicts"]), (1, True, [{"path": "a.txt", "kind": "hook"}]))
        self.assertEqual(git(self.fork, "branch", "--show-current"), mg["branch"])
        self.assertIn("<<<<<<<", (self.fork / "a.txt").read_text())
        self.assertEqual(git(self.fork, "status", "--porcelain"), "")   # committed, not left mid-merge
        mf = Path(self.tmp.name) / "m.json"
        mf.write_text(json.dumps(mg))
        sf = Path(self.tmp.name) / "s2.json"
        sf.write_text(json.dumps(st))
        r = self.sync("report", "--status", str(sf), "--merge", str(mf))[1]
        self.assertEqual((r["draft"], r["issue"], "sync-failing" in r["labels"]), (True, True, True))
        self.assertIn("sync-conflict", r["labels"])   # conflict escalation: sticky label for a human to clear
        ck = Path(self.tmp.name) / "ck.json"
        ck.write_text(json.dumps({"ok": True, "escalate": False, "conflicts": [], "build_errors": [],
                                  "test_failures": [], "surface_violations": []}))
        r = self.sync("report", "--status", str(sf), "--check", str(ck), "--check-code", "0")[1]
        self.assertEqual(r["outcome"], "clean")
        self.assertNotIn("sync-conflict", r["labels"])

    def test_pending_branch_picks_up_later_tag_and_reuse_is_noop_safe(self):
        self.up_commit("a.txt", "2\n", tag="v1.1.0")
        self.fetch()
        code, mg = self.sync("merge", "--date", "20261019")
        br = mg["branch"]
        self.put(self.fork, "h.txt", "human\n")          # a human commit on the sync branch
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "human")
        head = git(self.fork, "rev-parse", "HEAD")
        # unchanged re-run: not an error, nothing committed
        code, mg = self.sync("merge", "--reuse", "--branch", br, "--force")
        self.assertEqual((code, mg["committed"]), (0, False))
        self.assertEqual(git(self.fork, "rev-parse", "HEAD"), head)
        # a later tag on an already-merged commit: bump in place on the pending branch
        git(self.up, "tag", "v1.2.0")
        self.fetch()
        code, st = self.sync("status", "--base", "HEAD", "--tags-base", "develop")
        self.assertEqual((st["mode"], st["tags"], st["rtags"]), ("bump", ["v1.1.0", "v1.2.0"], ["v1.2.0"]))
        self.assertEqual(self.sync("merge", "--bump-only", "--reuse", "--branch", br)[0], 0)
        self.assertEqual(self.version()[1], "last_release_tag=v1.2.0")
        self.assertTrue((self.fork / "h.txt").exists())

    def test_merge_stages_only_intended_paths(self):
        self.up_commit("a.txt", "2\n")
        self.fetch()
        self.put(self.fork, "status.json", "{}")
        self.sync("merge", "--date", "20261019")
        self.assertEqual(git(self.fork, "ls-files", "status.json"), "")

    def test_report_never_clean_on_missing_check_or_status_failure(self):
        ok = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        mg = Path(self.tmp.name) / "mg.json"
        mg.write_text(json.dumps({"ok": True}))
        self.assertEqual(self.sync("report", "--status", self._dump(ok), "--merge", str(mg))[1]["outcome"], "escalate")
        bad = {"ok": False, "step": "status", "next_action": "sha= regression"}
        r = self.sync("report", "--status", self._dump(bad))[1]
        self.assertEqual((r["outcome"], r["issue"], r["title"]), ("escalate", True, ""))

    def test_recheck_when_pending_and_nothing_new(self):
        self.assertEqual(self.sync("status")[1]["mode"], "none")
        self.assertEqual(self.sync("status", "--pending")[1]["mode"], "recheck")

    def test_inconsistent_check_is_failing(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        for ck, code in (({"ok": True, "escalate": False}, "1"),
                         ({"ok": True, "escalate": False, "test_failures": ["t"]}, "0"),
                         ({"ok": True, "escalate": False, "build_errors": ["b"]}, "0")):
            args = self.results(st, {"ok": True}, ck)
            self.assertEqual(self.sync("outcome", *args, "--check-code", code)[1]["outcome"], "failing", (ck, code))

    def test_tool_errors_escalate_and_malformed_check_never_clean(self):
        self.up_commit("a.txt", "2\n")
        self.fetch()
        self.set_version(git(self.up, "rev-parse", "HEAD"), "v1.0.0")
        code, out = self.sync("status", "--ref", self.c1)          # regression -> exit 3
        self.assertEqual((code, out["escalate"]), (3, True))
        st = self._dump({"ok": True, "tags": [], "short": "abc", "commits": 1})
        mg = Path(self.tmp.name) / "mg.json"
        mg.write_text(json.dumps({"ok": True}))
        good = {"ok": True, "escalate": False}
        for ck, extra in [(good, []), ({"ok": "false"}, []), ({"ok": True, "escalate": "no"}, []),
                          ({"ok": True, "surface_violations": ['{"kind": "made_up"}']}, []),
                          (good, ["--check-code", "2"])]:
            extra = extra or ["--check-code", "0"]
            f = Path(self.tmp.name) / "ck.json"
            f.write_text(json.dumps(ck))
            r = self.sync("report", "--status", st, "--merge", str(mg), "--check", str(f), *extra)[1]
            self.assertEqual(r["outcome"], "clean" if ck is good and extra == ["--check-code", "0"] else "escalate", (ck, extra))

    def test_null_list_in_check_escalates_and_renders(self):
        st = self._dump({"ok": True, "tags": [], "short": "abc", "commits": 1})
        mg = Path(self.tmp.name) / "mg.json"
        mg.write_text(json.dumps({"ok": True}))
        ck = Path(self.tmp.name) / "ck.json"
        ck.write_text('{"ok": false, "build_errors": null}')
        code, r = self.sync("report", "--status", st, "--merge", str(mg), "--check", str(ck))
        self.assertEqual((code, r["outcome"]), (0, "escalate"))

    def merge(self):
        return self.sync("merge", "--base-ref", "develop", "--date", "20261019")

    def guard(self):
        return self.sync("workflow-guard", "--base", "develop", "--cand", "HEAD")

    def test_upstream_edit_to_ci_yml_is_held_reported_and_pushable(self):
        self.up_commit("a.txt", "2\n", {".github/workflows/ci.yml": WF.replace("on:\n", "on:\n  pull_request:\n")})
        self.fetch()
        code, mg = self.merge()
        self.assertEqual(code, 0, mg)
        self.assertEqual((self.fork / ".github/workflows/ci.yml").read_text(), WF)   # held at base
        self.assertEqual(mg["held"]["files"], [{"status": "M", "path": ".github/workflows/ci.yml"}])
        self.assertTrue(mg["held"]["commits"])
        self.assertEqual(self.guard()[0], 0)                                        # no workflow diff: pushable
        f = Path(self.tmp.name)
        (f / "m.json").write_text(json.dumps(mg))
        (f / "c.json").write_text(json.dumps({"ok": True, "escalate": False}))
        r = self.sync("report", "--status", self._dump({"ok": True, "tags": [], "short": "abc", "commits": 1}),
                      "--merge", str(f / "m.json"), "--check", str(f / "c.json"), "--check-code", "0")[1]
        self.assertEqual((r["outcome"], r["issue"], r["push"], "workflow-review" in r["labels"]),
                         ("workflow_review", True, True, True))   # pushed, PR opened, never enrolled

    def test_added_workflow_with_yaml_tricks_is_held(self):
        trick = 'name: "multi\n  on: [push]"\n"on": push\njobs: {}\n'
        self.up_commit("a.txt", "2\n", {".github/workflows/x2.yml": trick})
        self.fetch()
        code, mg = self.merge()
        self.assertEqual(mg["held"]["files"], [{"status": "A", "path": ".github/workflows/x2.yml"}])
        self.assertFalse((self.fork / ".github/workflows/x2.yml").exists())
        self.assertEqual(self.guard()[0], 0)

    def test_filename_with_space_is_held(self):
        self.up_commit("a.txt", "2\n", {".github/workflows/evil task.yml": WF})
        self.fetch()
        code, mg = self.merge()
        self.assertEqual(mg["held"]["files"], [{"status": "A", "path": ".github/workflows/evil task.yml"}])
        self.assertFalse((self.fork / ".github/workflows/evil task.yml").exists())
        self.assertEqual(self.guard()[0], 0)

    def test_recheck_still_reports_held_changes(self):
        self.up_commit("a.txt", "2\n", {".github/workflows/ci.yml": WF + "# up\n"})
        self.fetch()
        self.merge()                                  # on the sync branch, ci.yml held back
        code, st = self.sync("status", "--pending", "--base", "HEAD", "--tags-base", "develop")
        self.assertEqual((st["mode"], st["held"]["files"]), ("recheck", [{"status": "M", "path": ".github/workflows/ci.yml"}]))
        r = self.sync("report", "--status", self._dump(st))[1]
        self.assertIn("workflow-review", r["labels"])

    def test_guard_rejects_any_workflow_diff(self):
        self.up_commit("a.txt", "2\n")
        self.fetch()
        self.merge()
        self.put(self.fork, ".github/workflows/ci.yml", WF + "# x\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "sneak")
        code, g = self.guard()
        self.assertEqual((code, g["touched"]), (1, [".github/workflows/ci.yml"]))
        f = Path(self.tmp.name) / "g.json"
        f.write_text(json.dumps(g))
        r = self.sync("report", "--status", self._dump({"ok": True, "tags": [], "short": "a", "commits": 1}),
                      "--guard", str(f))[1]
        self.assertEqual((r["outcome"], r["push"]), ("escalate", False))

    def results(self, st=None, mg=None, ck=None):
        """Write status/merge/check JSON files; returns the CLI args that pass them."""
        t = Path(self.tmp.name)
        self.nres = getattr(self, "nres", 0) + 1   # unique names: one test builds several arg lists
        args = []
        for flag, name, d in (("--status", "st", st), ("--merge", "mg", mg), ("--check", "ck", ck)):
            if d is not None:
                (t / f"{name}{self.nres}.json").write_text(json.dumps(d))
                args += [flag, str(t / f"{name}{self.nres}.json")]
        return args

    def test_outcome_table(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        held = {"held": {"files": [{"status": "M", "path": ".github/workflows/ci.yml"}], "commits": []}}
        ck, bad = {"ok": True, "escalate": False}, {"ok": False, "escalate": False}
        for name, args, want in [
                ("clean", self.results(st, {"ok": True}, ck), ("clean", [], True)),
                ("failing", self.results(st, {"ok": True}, bad), ("failing", [], False)),
                ("escalate", self.results({"ok": False}, None, ck), ("escalate", [], False)),
                ("workflow_review", self.results({**st, **held}, {"ok": True}, ck), ("workflow_review", [], False)),
                ("held+failing", self.results({**st, **held}, {"ok": True}, bad), ("failing", [], False)),
                ("stalled", [*self.results(st, {"ok": True}, ck), "--stalled"], ("clean", ["enroll_stalled"], True))]:
            code, o = self.sync("outcome", *args, "--check-code", "0")
            self.assertEqual((code, o["outcome"], o["extra"], o["enroll_eligible"]), (0, *want), name)

    def test_missing_check_exit_is_failing_never_clean(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        args = self.results(st, {"ok": True}, {"ok": True, "escalate": False})
        self.assertEqual(self.sync("outcome", *args)[1]["outcome"], "failing")
        self.assertEqual(self.sync("outcome", *args, "--check-code", "")[1]["outcome"], "failing")
        self.assertEqual(self.sync("outcome", *args, "--check-code", "0")[1]["outcome"], "clean")

    def test_issue_key_branch_order(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        args = self.results(st, {"ok": True, "branch": "upstream-sync/bundle"}, {"ok": False, "escalate": False})
        key = lambda *x: self.sync("report", *args, *x, "--check-code", "0")[1]["issue_key"]
        self.assertEqual(key("--pr-branch", "upstream-sync/orig"), "upstream-sync/orig failing")
        self.assertEqual(key(), "upstream-sync/bundle failing")
        bad = self.results({"ok": False, "short": "?"}, None, None)   # status failed, no merge, no bundle
        r = self.sync("report", *bad, "--pr-branch", "upstream-sync/orig")[1]
        self.assertEqual(r["issue_key"], "upstream-sync/orig escalate")
        nokey = self.results(st, None, {"ok": True, "escalate": False})
        r = self.sync("report", *nokey, "--check-code", "1")[1]
        self.assertRegex(r["issue_key"], r"^upstream-sync/\d{8}-abc failing$")

    def test_report_outcome_and_enrollment_lines(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        md = Path(self.tmp.name) / "r.md"
        args = self.results(st, {"ok": True, "branch": "upstream-sync/20261019-abc"}, {"ok": False, "escalate": False})
        r = self.sync("report", *args, "--out", str(md), "--check-code", "0")[1]
        self.assertEqual((r["issue_key"], r["issue_title"]),
                         ("upstream-sync/20261019-abc failing", "Upstream sync needs attention: develop@abc [failing]"))
        self.assertIn("\nOutcome: failing\nEnrollment: pending\n", md.read_text())
        r = self.sync("report", *args, "--out", str(md), "--demo", "--check-code", "0")[1]
        self.assertEqual((r["labels"], r["title"]), (["sync-demo"], "Upstream sync demo: develop@abc (1 commits)"))
        held = {"ok": True, "held": {"files": [{"status": "A", "path": ".github/workflows/n.yml"}], "commits": []}}
        r = self.sync("report", *self.results(st, held, {"ok": True, "escalate": False}), "--check-code", "0")[1]
        self.assertEqual(r["issue_key"], "workflow-review workflow_review")

    def test_veto_pins_marker_and_blocks_clean(self):
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        md = Path(self.tmp.name) / "v.md"
        sha = "c" * 40
        ok = self.results(st, {"ok": True, "branch": "upstream-sync/20261019-abc"}, {"ok": True, "escalate": False})
        r = self.sync("report", *ok, "--check-code", "0", "--out", str(md), "--head-sha", sha)[1]
        self.assertEqual(r["outcome"], "clean")
        self.assertNotIn("upstream-sync-veto", md.read_text())
        r = self.sync("report", *ok, "--check-code", "0", "--out", str(md), "--head-sha", sha, "--veto-outcome", "escalate")[1]
        self.assertEqual((r["outcome"], r["draft"]), ("escalate", True))
        self.assertIn(f"<!-- upstream-sync-veto: {sha} escalate -->", md.read_text())
        bad = self.results(st, {"ok": True}, {"ok": False, "escalate": False})
        self.sync("report", *bad, "--check-code", "0", "--out", str(md), "--head-sha", sha)
        self.assertIn(f"<!-- upstream-sync-veto: {sha} failing -->", md.read_text())
        self.sync("report", *bad, "--check-code", "0", "--out", str(md), "--head-sha", "zz")
        self.assertNotIn("upstream-sync-veto", md.read_text())
        o = self.sync("outcome", *ok, "--check-code", "0", "--veto-outcome", "failing")[1]
        self.assertEqual((o["outcome"], o["enroll_eligible"]), ("failing", False))

    def test_required_checks_match_local_ci(self):
        ci = (HERE.parents[1] / ".github/workflows/local-ci.yml").read_text()
        names = re.findall(r"^ {8}name: (.+)$", ci, re.M)          # job-level names (8 spaces); steps are deeper
        mods = re.search(r"^ +module:\n((?: +- .+\n)+)", ci, re.M).group(1).split()[1::2]
        want = {n for n in names if "${{" not in n} | {n.replace("${{ matrix.module }}", m) for n in names
                                                       if "${{" in n for m in mods}
        self.assertEqual(set(su.tomllib.loads((HERE.parent / "upstream-sync.toml").read_text())["required_checks"]), want)

    def make_sim(self, name="test/upstream-sim-1", files=None, parent=None):
        """A simulated-upstream branch on origin (the fork's clone source), one commit past `parent`."""
        git(self.up, "checkout", "-q", "-b", name, parent or self.c1)
        sha = self.up_commit("sim.txt", "sim\n", files)
        git(self.up, "checkout", "-q", "develop")
        self.fetch()
        git(self.fork, "fetch", "-q", "origin")
        return sha

    def test_demo_ref_validation_exits_2(self):
        self.up_commit("a.txt", "2\n")                      # develop moves on; origin/develop is not sha=
        self.make_sim("test/upstream-sim-2", parent="develop")   # parent != sha=
        self.make_sim("test/other-1")
        for ref in ("test/other-1", "test/upstream-sim-9", "test/upstream-sim-2", "test/upstream-sim-1x"):
            for cmd in ("status", "merge"):
                self.assertEqual(self.sync(cmd, "--demo-ref", ref)[0], 2, (cmd, ref))

    def test_demo_ref_tag_without_branch_rejected(self):
        git(self.up, "tag", "origin/test/upstream-sim-7", self.c1)
        self.fetch()
        for cmd in ("status", "merge"):
            self.assertEqual(self.sync(cmd, "--demo-ref", "test/upstream-sim-7")[0], 2, cmd)
        self.assertEqual(self.sync("check", "--no-go", "--demo-ref", "test/upstream-sim-7")[0], 2)

    def test_check_escalates_committed_conflict_markers(self):
        self.put(self.fork, "doc.md", "Title\n=======\n")   # banner in a file the merge does not change
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "banner")
        git(self.fork, "update-ref", "refs/remotes/origin/develop", "HEAD")
        self.put(self.fork, "f.txt", "a\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> up\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "conflicted")
        code, ck = self.sync("check", "--no-go", "--base", "origin/develop")
        self.assertEqual(code, 2)
        self.assertTrue(ck["escalate"])
        self.assertEqual(ck["conflict_markers"], 3)   # counted even though go is skipped (fix 2)
        self.assertEqual([x for x in ck["surface_violations"] if "conflict_markers" in x],
                         [json.dumps({"kind": "conflict_markers", "path": "f.txt"})])
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        ck["escalate"] = ck["escalate"]
        args = self.results(st, {"ok": True}, ck)
        o = self.sync("outcome", *args, "--check-code", "2")[1]
        self.assertEqual((o["outcome"], o["enroll_eligible"]), ("escalate", False))

    def test_check_job_runs_on_hook_conflict_exit(self):
        wf = (HERE.parents[1] / ".github/workflows/upstream-sync.yml").read_text()
        self.assertRegex(wf, r"needs\.merge\.outputs\.code == '1'.*mode == 'recheck'")

    def test_recheck_keeps_last_known_conflict_counts(self):
        body = Path(self.tmp.name) / "prev.md"
        md = Path(self.tmp.name) / "r.md"
        st = {"ok": True, "tags": [], "short": "abc", "commits": 1}
        mg = {"ok": False, "conflicts": [{"path": "a", "kind": "hook"}, {"path": "b", "kind": "legacy-hook"}]}
        self.sync("report", *self.results(st, mg, {"ok": True}), "--out", str(body), "--check-code", "0")
        self.assertIn("- legacy-hook (manual re-merge): 1\n", body.read_text())   # not folded into hook
        self.assertIn("- hook: 1\n", body.read_text())
        ck = {"ok": True, "conflict_markers": 3}
        self.sync("report", *self.results(st, {}, ck), "--out", str(md), "--check-code", "0", "--prev-body", str(body))
        self.assertIn("- hook: 1\n", md.read_text())
        self.sync("report", *self.results(st, {}, ck), "--out", str(md), "--check-code", "0")   # no prior body
        self.assertNotIn("- hook: 0", md.read_text())

    def test_legacy_only_record_conflict_is_legacy_hook(self):
        self.put(self.fork, "local/fork-surface.toml", 'allow = ["a.txt", "b.txt"]\n')
        self.put(self.fork, "local/FORK_SURFACE.md", "### L-a\nfiles: a.txt\nkind: legacy\nrecipe: by hand\n"
                 "### S1\nfiles: b.txt\nkind: data\nrecipe: r\n")
        self.assertEqual([su.classify(p, su.policy(self.fork)) for p in ("a.txt", "b.txt")], ["legacy-hook", "hook"])

    def test_demo_merge_keeps_version_and_demo_prefix(self):
        sim = self.make_sim()
        code, st = self.sync("status", "--demo-ref", "test/upstream-sim-1")
        self.assertEqual((code, st["mode"], st["target"], st["tags"], st["demo"]), (0, "sync", sim, [], True))
        code, mg = self.sync("merge", "--demo-ref", "test/upstream-sim-1", "--date", "20261019")
        self.assertEqual((code, mg["branch"]), (0, f"upstream-sync-demo/20261019-{sim[:10]}"))
        self.assertEqual(self.version(), [f"sha={self.c1}", "last_release_tag=v1.0.0"])
        self.assertTrue((self.fork / "sim.txt").exists())

    def test_check_exempts_sync_demo_only_with_demo_ref(self):
        self.put(self.fork, "local/fork-surface.toml", 'ignore = ["local/**"]\n')
        sim = self.make_sim(files={"local/sync-demo/x.txt": "x\n", "local/sneaky.txt": "x\n"})
        want = [{"kind": "upstream_adds_ignored_path", "path": p} for p in ("local/sneaky.txt", "local/sync-demo/x.txt")]
        self.assertEqual(su.added_ignored(self.fork, self.c1, sim), want)
        self.assertEqual(su.added_ignored(self.fork, self.c1, sim, su.DEMO_EXEMPT), want[:1])


if __name__ == "__main__":
    unittest.main()
