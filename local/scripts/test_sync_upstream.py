# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Tiny fixture tests for sync_upstream.py on temp git repos. No gh, no network."""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "sync_upstream.py"
sys.path.insert(0, str(HERE))
import sync_upstream as su  # noqa: E402

CONF = "upstream_ref = 'develop'\ntag_pattern = '^v\\d+\\.\\d+\\.\\d+$'\n"
WF = "name: x\non:\n  push:\n    branches: [main]\n\njobs:\n  a:\n    runs-on: ubuntu-latest\n"


def git(path, *a):
    return subprocess.run(["git", "-C", str(path), "-c", "user.name=t", "-c", "user.email=t@t",
                           "-c", "commit.gpgsign=false", *a], check=True, capture_output=True, text=True).stdout.strip()


class Sync(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.up, self.fork = t / "up", t / "fork"
        self.up.mkdir()
        git(self.up, "init", "-q", "-b", "develop")
        self.c1 = self.up_commit("a.txt", "1\n", {".github/workflows/x.yml": WF})
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
        self.put(self.fork, "local/UPSTREAM_VERSION", f"sha={sha}\nlast_release_tag={tag}\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "version")

    def fetch(self):
        git(self.fork, "fetch", "-q", "upstream", "--tags")

    def sync(self, *args):
        p = subprocess.run([sys.executable, str(SCRIPT), "--root", str(self.fork), *args],
                           capture_output=True, text=True)
        return p.returncode, json.loads(p.stdout)

    def version(self):
        return (self.fork / "local/UPSTREAM_VERSION").read_text().split()

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
        code, st = self.sync("status", "--pending")
        self.assertEqual(st["mode"], "none")           # a sync PR is pending: it carries the tag
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

    def test_rewrite_on_block(self):
        out = su.rewrite_on(WF, "x")
        self.assertEqual(out, "name: x\non:\n  workflow_dispatch:\n\njobs:\n  a:\n    runs-on: ubuntu-latest\n")
        self.assertEqual(su.rewrite_on(out, "x"), out)   # idempotent
        self.assertEqual(su.rewrite_on("on: [push]\njobs: {}\n", "x"), "on:\n  workflow_dispatch:\njobs: {}\n")
        for bad in ("jobs: {}\n", "on: push\non: pull_request\n"):
            with self.assertRaises(su.Escalate):
                su.rewrite_on(bad, "x")

    def test_upstream_adds_trigger_to_neutralized_file_stays_dispatch_only(self):
        self.put(self.fork, "local/neutralized.txt", ".github/workflows/x.yml\n")
        git(self.fork, "add", "-A")
        git(self.fork, "commit", "-qm", "list")
        self.up_commit("a.txt", "2\n", {
            ".github/workflows/x.yml": WF.replace("on:\n", "on:\n  pull_request:\n"),
            ".github/workflows/new.yml": WF})
        self.fetch()
        code, mg = self.sync("merge", "--date", "20261019")
        self.assertEqual(code, 0, mg)
        for name in ("x.yml", "new.yml"):
            txt = (self.fork / ".github/workflows" / name).read_text()
            self.assertIn("on:\n  workflow_dispatch:\n", txt)
            self.assertNotIn("push:", txt.split("jobs:")[0])
            self.assertNotIn("pull_request", txt)
        self.assertEqual((self.fork / "local/neutralized.txt").read_text(),
                         ".github/workflows/new.yml\n.github/workflows/x.yml\n")

    def test_failed_rewrite_stops_before_commit(self):
        self.up_commit("a.txt", "2\n", {".github/workflows/bad.yml": "name: bad\njobs: {}\n"})
        self.fetch()
        head = git(self.fork, "rev-parse", "HEAD")
        code, mg = self.sync("merge", "--date", "20261019")
        self.assertEqual(code, 2)
        self.assertEqual(self.version()[0], f"sha={self.c1}")
        self.assertEqual(git(self.fork, "rev-parse", "HEAD"), head)   # nothing committed


if __name__ == "__main__":
    unittest.main()
