# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "check_fork_surface.py"
TOOL = HERE.parent / "tools" / "importscan"
sys.path.insert(0, str(HERE))
import check_fork_surface as cfs  # noqa: E402

TOML = '''ignore = ["local/**", "up/**/local/**", "up/**/local_*.go"]
allow = ["up/hook.go", "up/data.json"]
legacy_added = ["up/old.go"]
[budget]
max_lines = 10
max_files = 5
max_legacy_added_lines = 5
[go]
fork_import_prefixes = ["nvpair-proxy/local/"]
'''
LEGACY = "### L-up-hook-go\nfiles: up/hook.go\nkind: legacy\nrecipe: r\n"
DATA = "### E9\nfiles: up/data.json\nkind: data\nrecipe: r\n"


def git(root, *a):
    return subprocess.run(["git", "-C", str(root), "-c", "user.name=t", "-c", "user.email=t@t",
                           "-c", "commit.gpgsign=false", *a], check=True, capture_output=True, text=True).stdout.strip()


class Fixture(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        git(self.root, "init", "-q", "-b", "main")
        self.write("up/hook.go", "package main\n")
        self.write("up/data.json", "{}\n")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-qm", "upstream")
        sha = git(self.root, "rev-parse", "HEAD")
        self.write("local/UPSTREAM_VERSION.txt", f"sha={sha}\nlast_release_tag=v1\n")
        self.write("local/fork-surface.toml", TOML)
        self.write("local/FORK_SURFACE.md", LEGACY + DATA)
        self.write("local/neutralized.txt", "")
        shutil.copytree(TOOL, self.root / "local/tools/importscan")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-qm", "fork base")
        git(self.root, "branch", "base")

    def tearDown(self):
        self.tmp.cleanup()

    def write(self, rel, text):
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def run_check(self, *extra):
        git(self.root, "add", "-A")
        git(self.root, "commit", "-qm", "change", "--allow-empty")
        r = subprocess.run([sys.executable, str(SCRIPT), "--root", str(self.root), *extra], capture_output=True, text=True)
        out = json.loads(r.stdout)
        return r.returncode, out, [v["kind"] for v in out["violations"]]


class CheckForkSurface(Fixture):
    def test_legacy_and_data_records_pass(self):
        self.write("up/hook.go", "package main\n// edit\n")
        self.write("up/data.json", '{"a": 1}\n')
        self.write("up/old.go", "package main\n")
        code, out, kinds = self.run_check()
        self.assertEqual((code, kinds, out["total_lines"], out["legacy_added_lines"]), (0, [], 3, 1))

    def test_unmarked_nonlegacy_go_hook_fails(self):
        self.write("local/FORK_SURFACE.md", "### E1\nfiles: up/hook.go\nkind: hook\nrecipe: r\nTests: up . -run 'TestE1_Nil'\n" + DATA)
        code, _, kinds = self.run_check()
        self.assertEqual(code, 1)
        self.assertIn("over_seam_budget", kinds)

    def test_malformed_record_is_tool_error(self):
        self.write("local/FORK_SURFACE.md", "### E1\nfiles: up/hook.go\nkind: bogus\nrecipe: r\n")
        code, out, _ = self.run_check()
        self.assertEqual(code, 2)
        self.assertIn("kind", out["error"])

    def test_glob_and_ignore_beats_allow(self):
        self.assertTrue(cfs.glob_re("up/**/local/**").match("up/local/x.go"))
        self.assertTrue(cfs.glob_re("up/**/local/**").match("up/a/b/local/x/y.go"))
        self.assertFalse(cfs.glob_re("up/*.go").match("up/a/x.go"))
        # `local/x.go` is both ignored and allowed: ignore wins, so it is not surface.
        self.write("local/x.go", "package x\n" * 50)
        self.write("local/fork-surface.toml", TOML.replace('"up/data.json"', '"up/data.json", "local/x.go"'))
        self.write("local/FORK_SURFACE.md", LEGACY + DATA + "### L-local-x-go\nfiles: local/x.go\nkind: legacy\nrecipe: r\n")
        code, out, kinds = self.run_check()
        self.assertEqual((code, out["files"]), (0, []))

    def test_budget_raised_needs_label(self):
        self.write("local/fork-surface.toml", TOML.replace("max_lines = 10", "max_lines = 11"))
        code, _, kinds = self.run_check("--base", "base")
        self.assertEqual((code, kinds), (1, ["budget_raised"]))
        code, _, kinds = self.run_check("--base", "base", "--label", "surface-budget-raise")
        self.assertEqual((code, kinds), (0, []))

    def test_importscan_glue_ok_upstream_fails(self):
        imp = 'package main\nimport _ "nvpair-proxy/local/affinity"\n'
        self.write("up/hook.go", imp)
        self.write("up/local_glue.go", imp)
        code, out, kinds = self.run_check()
        self.assertEqual(code, 1)
        self.assertEqual([(v["kind"], v["path"]) for v in out["violations"]], [("upstream_imports_fork", "up/hook.go")])

    def test_added_non_local_go_file_fails(self):
        self.write("up/extra.go", "package main\n")
        code, out, kinds = self.run_check()
        self.assertEqual((code, kinds), (1, ["not_allowlisted"]))

    def test_max_seams_exceeded(self):
        self.write("local/fork-surface.toml", TOML + '[[file]]\npath = "up/hook.go"\nmax_seams = 1\n')
        self.write("local/FORK_SURFACE.md", LEGACY + DATA + "### E1\nfiles: up/data.json\nkind: data\nrecipe: r\n### E2\nfiles: up/data.json\nkind: data\nrecipe: r\n")
        self.write("up/hook.go", "package main\nvar a int // fork:E1\nvar b int // fork:E2\n")
        code, out, kinds = self.run_check()
        self.assertEqual((code, kinds), (1, ["over_seam_budget"]))

    def test_cap_removal_is_budget_raised(self):
        self.write("local/fork-surface.toml", TOML.replace("max_lines = 10\n", ""))
        code, _, kinds = self.run_check("--base", "base")
        self.assertEqual((code, kinds), (1, ["budget_raised"]))

    def test_float_cap_is_tool_error(self):
        self.write("local/fork-surface.toml", TOML.replace("max_lines = 10", "max_lines = 1e9"))
        code, out, _ = self.run_check()
        self.assertEqual(code, 2)

    def test_bad_base_is_tool_error(self):
        code, out, _ = self.run_check("--base", "no-such-ref")
        self.assertEqual(code, 2)

    def test_nested_local_pkg_importing_upstream_fails(self):
        self.write("up/shared/local/x/x.go", 'package x\nimport _ "up/thing"\n')
        self.write("up/shared/local/x/x_test.go", 'package x\nimport _ "testing"\n')
        code, out, kinds = self.run_check()
        self.assertEqual([(v["kind"], v["path"]) for v in out["violations"]], [("fork_imports_upstream", "up/shared/local/x/x.go")])


if __name__ == "__main__":
    unittest.main()
