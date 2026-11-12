# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
"""Two-file temp-repo fixture for reapply_patches.sh --check: one patch reproduces, one conflicts."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "reapply_patches.sh"
ENV = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t",
       "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}


def git(repo, *a):
    return subprocess.run(["git", "-C", str(repo), *a], env=ENV, check=True, capture_output=True, text=True).stdout


def commit(repo, files, msg):
    for name, text in files.items():
        p = repo / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
    git(repo, "add", "-A")
    git(repo, "commit", "-qm", msg)
    return git(repo, "rev-parse", "HEAD").strip()


class ReapplyPatchesTest(unittest.TestCase):
    def check(self, feature, fork_b, surface="# records\n"):
        with tempfile.TemporaryDirectory() as d:
            repo = Path(d)
            git(repo, "init", "-q", "-b", "main")
            pin = commit(repo, {"a.txt": "1\n2\n3\n", "b.txt": "x\ny\nz\n"}, "upstream")
            commit(repo, {"b.txt": fork_b, "local/UPSTREAM_VERSION.txt": f"sha={pin}\n",
                          "local/FORK_SURFACE.md": surface}, "fork")
            commit(repo, feature, "feature")
            patch = git(repo, "format-patch", "-1", "HEAD", "--stdout")
            commit(repo, {"local/patches/001-feature.patch": patch}, "export")
            return subprocess.run(["bash", str(SCRIPT), "--check", "HEAD"], cwd=repo, env=ENV,
                                  capture_output=True, text=True)

    def test_patch_reproduces(self):
        r = self.check({"a.txt": "1\nTWO\n3\n"}, "x\ny\nz\n")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_patch_conflicts(self):
        # The fork edited b.txt before the feature, so the series cannot rebuild it on the pin.
        r = self.check({"b.txt": "x\nYY\nz\n"}, "x\nY\nz\n")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("does not apply", r.stderr)

    def test_conflict_on_legacy_file_fails(self):
        # Legacy records only narrow the comparison; an am conflict still fails.
        legacy = "### L-b\nfiles: b.txt\nkind: legacy\nrecipe: x\n"
        r = self.check({"b.txt": "x\nYY\nz\n"}, "x\nY\nz\n", legacy)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("does not apply", r.stderr)


if __name__ == "__main__":
    unittest.main()
