#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Enforce the PAIR fork surface (plan section 3.2). Stdlib only.

Prints one JSON object {ok, upstream, files[], total_lines, legacy_added_lines,
violations[]}; exit 0 ok, 1 violations, 2 tool error (bad config, malformed
record, missing ref, go tool failure). Policy: local/fork-surface.toml.

Options: --base <ref> enables budget_raised (compares against the base branch),
--label <name> (repeatable, or env PR_LABELS=a,b) supplies PR labels,
--new-upstream <ref> enables upstream_adds_ignored_path.
"""
import argparse
import json
import os
import re
import shlex
import subprocess
import sys
import tomllib
from pathlib import Path

# Glob precedence when a path matches several lists (first wins).
PRECEDENCE = ("ignore", "generated", "neutralized", "allow")
ESCALATE_KINDS = {"over_line_budget", "over_file_budget", "not_allowlisted", "upstream_imports_fork",
                  "upstream_adds_ignored_path", "budget_raised", "fork_imports_upstream"}

ID_RE = re.compile(r"^(?:[PES]\d+|L-[a-z0-9][a-z0-9-]*)$")
MARKER_RE = re.compile(r"//\s*fork:([A-Za-z0-9][A-Za-z0-9-]*)")
KINDS = ("hook", "data", "legacy")


class ToolError(Exception):
    pass


def glob_re(pattern):
    """`*` within a segment, `**` zero or more directories, `?` one non-slash char."""
    out, i = [], 0
    while i < len(pattern):
        if pattern.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif pattern.startswith("**", i):
            out.append(".*")
            i += 2
        else:
            c = pattern[i]
            out.append("[^/]*" if c == "*" else "[^/]" if c == "?" else re.escape(c))
            i += 1
    return re.compile("".join(out) + r"\Z")


def matches(path, patterns):
    return any(glob_re(p).match(path) for p in patterns)


def run(root, *args, cwd=None, check=True):
    try:
        r = subprocess.run(list(args), cwd=cwd or root, capture_output=True, text=True)
    except OSError as e:
        raise ToolError(f"{args[0]}: {e}")
    if check and r.returncode:
        raise ToolError(f"{' '.join(args[:4])}: {(r.stderr or r.stdout).strip()[-300:]}")
    return r


def git(root, *args):
    return run(root, "git", "-C", str(root), *args).stdout


def read_lines(path):
    if not path.exists():
        return []
    return [ln.strip() for ln in path.read_text().splitlines() if ln.strip() and not ln.strip().startswith("#")]


def parse_version(root):
    kv = dict(ln.split("=", 1) for ln in read_lines(Path(root) / "local/UPSTREAM_VERSION") if "=" in ln)
    sha = kv.get("sha", "")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ToolError("local/UPSTREAM_VERSION: missing or malformed sha=<40-hex>")
    return sha


def parse_records(root):
    """Parse local/FORK_SURFACE.md; a malformed record is a ToolError."""
    path = Path(root) / "local/FORK_SURFACE.md"
    if not path.exists():
        raise ToolError("local/FORK_SURFACE.md not found")
    recs, cur = [], None
    for ln in path.read_text().splitlines():
        if ln.startswith("### "):
            cur = {"id": ln[4:].strip(), "f": {}}
            recs.append(cur)
        elif cur is not None:
            m = re.match(r"^(files|kind|recipe|Tests):\s*(.*)$", ln)
            if m:
                cur["f"][m.group(1)] = m.group(2).strip()
    out = []
    for r in recs:
        rid, f = r["id"], r["f"]
        bad = lambda why: ToolError(f"FORK_SURFACE.md record {rid!r}: {why}")
        if not ID_RE.match(rid):
            raise bad("id must be P<n>/E<n>/S<n> or L-<path-slug>")
        files = [x.strip() for x in f.get("files", "").split(",") if x.strip()]
        if not files:
            raise bad("files: is required")
        if any(re.search(r"[*?\[]", x) for x in files):
            raise bad("files: must not contain globs")
        kind = f.get("kind")
        if kind not in KINDS:
            raise bad("kind: must be hook|data|legacy")
        if rid.startswith("L-") != (kind == "legacy"):
            raise bad("L-<slug> ids are exactly the legacy records")
        if not f.get("recipe"):
            raise bad("recipe: is required")
        tests = None
        if "Tests" in f:
            try:
                parts = shlex.split(f["Tests"])
            except ValueError as e:
                raise bad(f"Tests: {e}")
            if len(parts) != 4 or parts[2] != "-run":
                raise bad("Tests: must be `<module dir> <pkg pattern> -run '<regex>'`")
            tests = parts
        if kind == "hook" and tests is None:
            raise bad("Tests: is required for kind: hook")
        out.append({"id": rid, "files": files, "kind": kind, "tests": tests})
    return out


def diff_stats(root, ref):
    """Per-path {status, lines, hunks} for `ref...HEAD`."""
    stats = {}
    for ln in git(root, "diff", "--numstat", "--no-renames", f"{ref}...HEAD").splitlines():
        a, d, p = ln.split("\t", 2)
        stats[p] = {"lines": (int(a) + int(d)) if a != "-" else 0, "hunks": 0, "added": False}
    for p in git(root, "diff", "--name-only", "--no-renames", "--diff-filter=A", f"{ref}...HEAD").splitlines():
        if p in stats:
            stats[p]["added"] = True
    cur = None
    for ln in git(root, "diff", "--no-renames", f"{ref}...HEAD").splitlines():
        if ln.startswith("diff --git "):
            cur = ln.rsplit(" b/", 1)[-1]
        elif ln.startswith("@@") and cur in stats:
            stats[cur]["hunks"] += 1
    return stats


def importscan(root, files):
    if not files:
        return {}
    tool = Path(root) / "local/tools/importscan"
    r = run(root, "go", "run", ".", *[str(Path(root) / f) for f in files], cwd=tool)
    res = {}
    for ln in r.stdout.splitlines():
        o = json.loads(ln)
        res[os.path.relpath(o["file"], root).replace(os.sep, "/")] = o["imports"]
    return res


def local_pkg_violations(root, cfg):
    """Packages under <module>/local/... may import only stdlib, allowed prefixes, other local packages."""
    allowed = cfg.get("go", {}).get("fork_allowed_imports", [])
    out, std = [], None
    for gomod in git(root, "ls-files", "*go.mod").splitlines():
        mod = Path(root) / Path(gomod).parent
        if not (mod / "local").is_dir():
            continue
        if std is None:
            std = set(run(root, "go", "list", "std").stdout.split())
        r = run(root, "go", "list", "-f", '{{.ImportPath}} {{join .Imports " "}}', "./local/...", cwd=mod)
        for ln in r.stdout.splitlines():
            pkg, *imps = ln.split()
            for imp in imps:
                if imp in std or any(imp.startswith(p) for p in allowed) or re.search(r"(^|/)local(/|$)", imp):
                    continue
                out.append({"kind": "fork_imports_upstream", "package": pkg, "import": imp})
    return out


def hook_test_violations(root, records):
    out = []
    for r in records:
        if r["kind"] != "hook":
            continue
        mod, pkg, _, regex = r["tests"]
        name = f"Test{r['id']}_Nil"
        res = run(root, "go", "test", "-list", regex, pkg, cwd=Path(root) / mod, check=False)
        if res.returncode or name not in res.stdout.split():
            out.append({"kind": "missing_hook_test", "id": r["id"], "expected": name})
    return out


def _max_keys(d):
    return {k: v for k, v in d.items() if k.startswith("max_") and isinstance(v, int)}


def budget_raised(root, base, labels):
    """Compare policy files against <base>; growth needs a label."""
    def show(path):
        r = run(root, "git", "-C", str(root), "show", f"{base}:{path}", check=False)
        return r.stdout if r.returncode == 0 else None

    out = []
    old_toml = show("local/fork-surface.toml")
    if old_toml is not None and "surface-budget-raise" not in labels:
        old, new = tomllib.loads(old_toml), tomllib.loads((Path(root) / "local/fork-surface.toml").read_text())
        for k, v in _max_keys(new.get("budget", {})).items():
            if k in old.get("budget", {}) and v > old["budget"][k]:
                out.append({"kind": "budget_raised", "what": f"budget.{k}", "from": old["budget"][k], "to": v})
        oldf = {f["path"]: f for f in old.get("file", [])}
        for f in new.get("file", []):
            for k, v in _max_keys(f).items():
                if f["path"] in oldf and v > oldf[f["path"]].get(k, v):
                    out.append({"kind": "budget_raised", "what": f"{f['path']}.{k}", "from": oldf[f['path']][k], "to": v})
        for key in ("allow", "legacy_added", "generated", "ignore"):
            for e in sorted(set(new.get(key, [])) - set(old.get(key, []))):
                out.append({"kind": "budget_raised", "what": f"{key} += {e}"})
    old_py = show("local/scripts/check_fork_surface.py")
    if old_py is not None and "surface-budget-raise" not in labels:
        cur_py = Path(__file__).read_text()
        for name in ("PRECEDENCE", "ESCALATE_KINDS"):
            pat = re.compile(rf"^{name} = .*?(?=^\S)", re.S | re.M)
            a, b = pat.search(old_py), pat.search(cur_py)
            if (a and a.group(0)) != (b and b.group(0)):
                out.append({"kind": "budget_raised", "what": f"checker {name} changed"})
    old_neu = show("local/neutralized.txt")
    if old_neu is not None and "upstream-sync" not in labels:
        old_set = {ln.strip() for ln in old_neu.splitlines() if ln.strip() and not ln.startswith("#")}
        for e in sorted(set(read_lines(Path(root) / "local/neutralized.txt")) - old_set):
            out.append({"kind": "budget_raised", "what": f"neutralized += {e}"})
    return out


def check(root, base=None, labels=(), new_upstream=None):
    root = Path(root)
    sha = parse_version(root)
    try:
        git(root, "rev-parse", "--verify", "--quiet", sha + "^{commit}")
    except ToolError:
        raise ToolError(f"upstream sha not found: {sha} (fetch the upstream remote)")
    cfg = tomllib.loads((root / "local/fork-surface.toml").read_text())
    lists = {"ignore": cfg.get("ignore", []), "generated": cfg.get("generated", []),
             "neutralized": read_lines(root / "local/neutralized.txt"), "allow": cfg.get("allow", [])}
    legacy = cfg.get("legacy_added", [])
    budget = cfg.get("budget", {})
    records = parse_records(root)
    rec_ids = {r["id"] for r in records}
    rec_files = {f for r in records for f in r["files"]}

    def category(path):
        for name in PRECEDENCE:
            if matches(path, lists[name]):
                return name
        return None

    viol, files, total, legacy_lines = [], [], 0, 0
    scan_go = []
    for path, st in diff_stats(root, sha).items():
        cat = category(path)
        if path.endswith(".go") and cat != "ignore":
            scan_go.append(path)
        if cat in ("ignore", "generated", "neutralized"):
            continue
        if st["added"]:
            in_legacy = matches(path, legacy)
            # Added .go files must be fork-owned (ignored) or legacy; `allow` never rescues them.
            ok = in_legacy or (cat == "allow" and not path.endswith(".go"))
            if not ok:
                viol.append({"kind": "not_allowlisted", "path": path, "added": True})
            if in_legacy:
                legacy_lines += st["lines"]
                continue
        elif cat != "allow":
            viol.append({"kind": "not_allowlisted", "path": path})
        seams = sorted(set(MARKER_RE.findall((root / path).read_text(errors="replace")))) \
            if path.endswith(".go") and (root / path).exists() else []
        files.append({"path": path, "lines": st["lines"], "hunks": st["hunks"], "seams": seams})
        total += st["lines"]
    files.sort(key=lambda f: f["path"])
    by_path = {f["path"]: f for f in files}

    for key, val, n, kind in (("max_lines", total, total, "over_line_budget"),
                              ("max_files", len(files), len(files), "over_file_budget"),
                              ("max_legacy_added_lines", legacy_lines, legacy_lines, "over_legacy_budget")):
        if key in budget and val > budget[key]:
            viol.append({"kind": kind, "actual": val, "max": budget[key]})
    for fb in cfg.get("file", []):
        f = by_path.get(fb["path"])
        if not f:
            continue
        for key, actual, kind in (("max_lines", f["lines"], "over_file_lines"), ("max_hunks", f["hunks"], "over_file_hunks"),
                                  ("max_seams", len(f["seams"]), "over_seam_budget")):
            if key in fb and actual > fb[key]:
                viol.append({"kind": kind, "path": fb["path"], "actual": actual, "max": fb[key]})

    # Seam markers vs records (Go files only; legacy and data records are matched by path).
    for f in files:
        for sid in f["seams"]:
            if sid not in rec_ids:
                viol.append({"kind": "over_seam_budget", "path": f["path"], "marker": sid, "reason": "marker names no record"})
    for r in records:
        if r["kind"] == "hook":
            for p in r["files"]:
                if p.endswith(".go"):
                    fp = root / p
                    has = fp.exists() and r["id"] in MARKER_RE.findall(fp.read_text(errors="replace"))
                    if not has:
                        viol.append({"kind": "over_seam_budget", "path": p, "id": r["id"], "reason": "hook record has no marker"})
    for pat in lists["allow"]:
        if not any(glob_re(pat).match(f) for f in rec_files):
            viol.append({"kind": "missing_hook_record", "path": pat})

    # Go import rules.
    cfg_go = cfg.get("go", {})
    prefixes = cfg_go.get("fork_import_prefixes", [])
    all_go = [p for p in git(root, "ls-files", "*.go").splitlines() if category(p) != "ignore"]
    if prefixes:
        for path, imps in sorted(importscan(root, all_go).items()):
            for imp in imps:
                if any(imp.startswith(p) for p in prefixes):
                    viol.append({"kind": "upstream_imports_fork", "path": path, "import": imp})
        viol += local_pkg_violations(root, cfg)
    viol += hook_test_violations(root, records)

    if new_upstream:
        ignore = lists["ignore"]
        for p in git(root, "diff", "--name-only", "--diff-filter=A", f"{sha}..{new_upstream}").splitlines():
            if matches(p, ignore):
                viol.append({"kind": "upstream_adds_ignored_path", "path": p})
    if base:
        viol += budget_raised(root, base, set(labels))

    return {"ok": not viol, "upstream": sha, "files": files, "total_lines": total,
            "legacy_added_lines": legacy_lines, "violations": viol}


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=None, help="repo root (default: parent of local/)")
    ap.add_argument("--base", default=None)
    ap.add_argument("--label", action="append", default=[])
    ap.add_argument("--new-upstream", default=None)
    args = ap.parse_args()
    root = args.root or Path(__file__).resolve().parents[2]
    labels = args.label + [x for x in os.environ.get("PR_LABELS", "").split(",") if x]
    try:
        result = check(root, args.base, labels, args.new_upstream)
    except (ToolError, OSError, tomllib.TOMLDecodeError, ValueError, KeyError) as e:
        print(json.dumps({"ok": False, "error": str(e), "violations": []}))
        return 2
    print(json.dumps(result))
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
