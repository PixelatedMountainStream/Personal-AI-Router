#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Daily upstream sync for the PAIR fork (plan section 4). Stdlib only, no gh calls.

Subcommands: status | merge | neutralize | conflicts | check | report.
Each prints ONE JSON object {ok, step, conflicts[], build_errors[], test_failures[],
surface_violations[], next_action, ...}. Exit codes: 0 ok, 1 needs repair, 2 escalate,
3 sha= regression (upstream is not a descendant of local/UPSTREAM_VERSION.txt sha=).
"""
import argparse
import json
import re
import shlex
import shutil
import subprocess
import sys
import tomllib
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import check_fork_surface as cfs  # noqa: E402  (glob dialect + ESCALATE_KINDS shared with the checker)

ROOT = HERE.parents[1]
FORK_WORKFLOWS = ("local-*.yml", "local-*.yaml", "upstream-sync.yml")
NEVER_REWRITE = {"ci.yml", "build.yml", "release-intent-apply.yml", "release-intent-check.yml"}
SHA_RE = re.compile(r"[0-9a-f]{40}")


class Escalate(Exception):
    pass


class Regression(Exception):
    pass


def emit(step, ok, code, next_action, **kw):
    out = {"ok": ok, "step": step, "conflicts": [], "build_errors": [],
           "test_failures": [], "surface_violations": [], "next_action": next_action}
    out.update(kw)
    print(json.dumps(out, indent=2))
    return code


def run(cmd, cwd, check=False):
    p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    if check and p.returncode:
        raise Escalate(f"{' '.join(cmd)} failed: {(p.stderr or p.stdout).strip()[:300]}")
    return p


def git(root, *args, check=True):
    return run(["git", *args], root, check)


def out(root, *args):
    return git(root, *args).stdout.strip()


def is_ancestor(root, a, b):
    return git(root, "merge-base", "--is-ancestor", a, b, check=False).returncode == 0


def has_commit(root, sha):
    return git(root, "cat-file", "-e", f"{sha}^{{commit}}", check=False).returncode == 0


def config(root):
    try:
        return tomllib.loads((Path(root) / "local/upstream-sync.toml").read_text())
    except (OSError, tomllib.TOMLDecodeError) as e:
        raise Escalate(f"local/upstream-sync.toml unusable: {e}")


# ---- UPSTREAM_VERSION -------------------------------------------------------
def parse_version(text):
    kv = dict(ln.split("=", 1) for ln in text.splitlines() if "=" in ln and not ln.startswith("#"))
    sha = kv.get("sha", "").strip()
    if not SHA_RE.fullmatch(sha):
        raise Escalate("local/UPSTREAM_VERSION.txt: missing or malformed sha=<40-hex>")
    return sha, kv.get("last_release_tag", "").strip()


def read_version(root, ref=None):
    if ref:
        return parse_version(out(root, "show", f"{ref}:local/UPSTREAM_VERSION.txt"))
    return parse_version((Path(root) / "local/UPSTREAM_VERSION.txt").read_text())


def added_ignored(root, old, new):
    """Upstream-range additions under fork-owned (ignore) globs: they would be silently uncounted."""
    ignore = tomllib.loads((Path(root) / "local/fork-surface.toml").read_text()).get("ignore", [])
    names = git(root, "diff", "--name-only", "--diff-filter=A", f"{old}..{new}").stdout.split()
    return [{"kind": "upstream_adds_ignored_path", "path": n} for n in names if cfs.matches(n, ignore)]


def write_version(root, sha, tag):
    (Path(root) / "local/UPSTREAM_VERSION.txt").write_text(f"sha={sha}\nlast_release_tag={tag}\n")


# ---- upstream refs and tags -------------------------------------------------
def resolve_remote(root, cfg, wanted):
    name = wanted or cfg.get("upstream_remote", "upstream")
    if name not in git(root, "remote").stdout.split():
        raise Escalate(f"remote {name!r} not found")
    return name


def resolve_target(root, remote, cfg, ref):
    ref = ref or cfg.get("upstream_ref", "develop")
    for cand in (f"{remote}/{ref}", ref):
        p = git(root, "rev-parse", "-q", "--verify", f"{cand}^{{commit}}", check=False)
        if p.returncode == 0:
            return p.stdout.strip()
    raise Escalate(f"upstream ref {ref!r} not found (fetch the {remote} remote)")


def tag_key(tag):
    return tuple(int(x) for x in re.findall(r"\d+", tag))


def newer_tags(root, remote, cfg, last_tag, contained_in):
    """Upstream tags matching tag_pattern, newer than last_tag, whose commit is in `contained_in`."""
    rx = re.compile(cfg.get("tag_pattern", r"^v\d+\.\d+\.\d+$"))
    shas = {}
    for line in git(root, "ls-remote", "--tags", remote).stdout.splitlines():
        sha, _, ref = line.partition("\t")
        name = ref.removeprefix("refs/tags/")
        peeled = name.endswith("^{}")
        name = name.removesuffix("^{}")
        if rx.search(name) and (peeled or name not in shas):
            shas[name] = sha
    floor = tag_key(last_tag) if last_tag else ()
    keep = [t for t, s in shas.items()
            if tag_key(t) > floor and has_commit(root, s) and is_ancestor(root, s, contained_in)]
    return sorted(keep, key=tag_key)


def plan(root, remote, cfg, ref, base, force, tags_base=None):
    """Shared by status and merge. `base` is the ref whose UPSTREAM_VERSION the PR would build on (the
    open sync PR's branch when one exists); `tags_base` (develop) is where the reported tag list starts."""
    base_sha, last_tag = read_version(root, base)
    target = resolve_target(root, remote, cfg, ref)
    if target != base_sha and not is_ancestor(root, base_sha, target):
        raise Regression(f"upstream {target[:10]} is not a descendant of sha={base_sha[:10]}")
    new = target != base_sha
    rtags = newer_tags(root, remote, cfg, last_tag, target)   # tags this branch has not recorded yet
    tags = rtags
    if tags_base:
        tags = newer_tags(root, remote, cfg, read_version(root, tags_base)[1], target)
    if new or force:
        mode = "sync"
    elif rtags:
        mode = "bump"
    else:
        mode = "none"
    commits = int(out(root, "rev-list", "--count", f"{base_sha}..{target}")) if new else 0
    return {"mode": mode, "new": new, "base_sha": base_sha, "target": target, "short": target[:10],
            "commits": commits, "tags": tags, "rtags": rtags, "newest_tag": tags[-1] if tags else "",
            "release": bool(tags), "last_release_tag": last_tag, "remote": remote}


def cmd_status(a, root):
    cfg = config(root)
    remote = resolve_remote(root, cfg, a.upstream_remote)
    p = plan(root, remote, cfg, a.ref, a.base or "HEAD", a.force, a.tags_base)
    return emit("status", True, 0, p["mode"] if p["mode"] != "none" else "none", **p)


# ---- workflow neutralization ------------------------------------------------
TOP_ON = re.compile(r"""^(?:on|"on"|'on')\s*:""")


def rewrite_on(text, name):
    """Replace the single top-level `on:` block (to the next top-level key) with workflow_dispatch only."""
    lines = text.splitlines()
    starts = [i for i, ln in enumerate(lines) if TOP_ON.match(ln)]
    if len(starts) != 1:
        raise Escalate(f"{name}: expected exactly one top-level `on:`, found {len(starts)}")
    s = starts[0]
    e = s + 1
    while e < len(lines) and not (lines[e] and not lines[e][0].isspace() and lines[e][0] != "#"):
        e += 1
    while e > s + 1 and (not lines[e - 1].strip() or lines[e - 1].startswith("#")):
        e -= 1   # keep blank/comment lines that precede the next key
    new = lines[:s] + ["on:", "  workflow_dispatch:"] + lines[e:]
    return "\n".join(new) + "\n"


def neutralized_list(root):
    return cfs.read_lines(Path(root) / "local/neutralized.txt")


def neutralize(root, new_paths=()):
    """Rewrite every listed + newly added workflow. Returns (changed_paths, listed)."""
    root = Path(root)
    listed = sorted(set(neutralized_list(root)) | set(new_paths))
    changed = []
    for rel in listed:
        f = root / rel
        if not f.is_file():
            continue
        old = f.read_text()
        new = rewrite_on(old, rel)
        if new != old:
            f.write_text(new)
            changed.append(rel)
    p = root / "local/neutralized.txt"
    body = "".join(x + "\n" for x in listed)
    if not p.exists() or p.read_text() != body:
        p.write_text(body)
        changed.append("local/neutralized.txt")
    return changed, listed


def added_workflows(root, old, new):
    names = git(root, "diff", "--name-only", "--diff-filter=A", f"{old}..{new}", "--", ".github/workflows").stdout.split()
    res = []
    for n in names:
        base = Path(n).name
        if n.count("/") == 2 and n.endswith((".yml", ".yaml")) and base not in NEVER_REWRITE \
                and not any(cfs.matches(base, [g]) for g in FORK_WORKFLOWS):
            res.append(n)
    return res


def cmd_neutralize(a, root):
    changed, listed = neutralize(root)
    if changed and a.commit:
        git(root, "add", "-A", "--", *changed)
        git(root, "commit", "-s", "--no-verify", "-m", "Re-apply workflow neutralization")
    return emit("neutralize", True, 0, "none", changed=changed, neutralized=listed)


# ---- merge ------------------------------------------------------------------
def policy(root):
    cfg = tomllib.loads((Path(root) / "local/fork-surface.toml").read_text())
    return {"ignore": cfg.get("ignore", []), "generated": cfg.get("generated", []),
            "neutralized": neutralized_list(root), "allow": cfg.get("allow", [])}


def classify(path, pol):
    for name, kind in (("ignore", "local"), ("generated", "generated"), ("neutralized", "neutralized"),
                       ("allow", "hook")):
        if cfs.matches(path, pol[name]):
            return kind
    return "other"


def unmerged(root):
    o = git(root, "ls-files", "-u", "-z").stdout
    paths = sorted({e.split("\t", 1)[1] for e in o.split("\0") if "\t" in e})
    pol = policy(root)
    return [{"path": p, "kind": classify(p, pol)} for p in paths]


def conflict_result(step, root, **kw):
    cs = unmerged(root)
    if any(c["kind"] in ("other", "local") for c in cs):
        return emit(step, False, 2, "ESCALATE: conflict outside the allowlist or in a fork-owned path",
                    conflicts=cs, **kw)
    if cs:
        return emit(step, False, 1, "hook -> re-apply the FORK_SURFACE.md recipe on upstream's version; then run check",
                    conflicts=cs, **kw)
    return emit(step, True, 0, "run: sync_upstream.py check", **kw)


def cmd_merge(a, root):
    cfg = config(root)
    remote = resolve_remote(root, cfg, a.upstream_remote)
    p = plan(root, remote, cfg, a.ref, "HEAD", a.force)
    old, last_tag = p["base_sha"], p["last_release_tag"]
    target = p["target"]
    stamp = a.date or datetime.now(timezone.utc).strftime("%Y%m%d")
    if a.bump_only:
        if not p["rtags"]:
            return emit("merge", True, 0, "none: no new tag to record", **p)
        branch = a.branch or f"upstream-sync/{stamp}-{old[:10]}"
        if not a.reuse:
            git(root, "checkout", "-b", branch)
        write_version(root, old, p["rtags"][-1])
        git(root, "add", "local/UPSTREAM_VERSION.txt")
        git(root, "commit", "-s", "--no-verify", "-m", f"Record upstream release {p['rtags'][-1]}")
        return emit("merge", True, 0, "run: sync_upstream.py check", branch=branch, committed=True, **p)
    branch = a.branch or f"upstream-sync/{stamp}-{target[:10]}"
    if not a.reuse:
        git(root, "checkout", "-b", branch)
    m = git(root, "merge", "--no-commit", "--no-ff", target, check=False)
    if m.returncode and not unmerged(root):
        raise Escalate(f"merge failed: {(m.stderr or m.stdout).strip()[:300]}")
    for c in unmerged(root):
        if c["kind"] in ("generated", "neutralized"):   # take upstream's copy
            if git(root, "cat-file", "-e", f"{target}:{c['path']}", check=False).returncode == 0:
                git(root, "checkout", target, "--", c["path"])
                git(root, "add", c["path"])
            else:
                git(root, "rm", "-q", "--", c["path"])
    # Classify before committing: the commit clears the unmerged index entries.
    cs = unmerged(root)
    changed, _ = neutralize(root, added_workflows(root, old, target))   # Escalate -> exit 2, nothing committed
    write_version(root, target, p["rtags"][-1] if p["rtags"] else last_tag)
    # Stage only intended paths (the merge result is already in the index): conflicted files go in with
    # their markers so a human can repair on the draft PR branch.
    git(root, "add", "-A", "--", "local/UPSTREAM_VERSION.txt", *changed, *[c["path"] for c in cs])
    merging = git(root, "rev-parse", "-q", "--verify", "MERGE_HEAD", check=False).returncode == 0
    if not merging and git(root, "diff", "--cached", "--quiet", check=False).returncode == 0:
        return emit("merge", True, 0, "unchanged: nothing to commit; run: sync_upstream.py check",
                    branch=branch, committed=False, **p)
    msg = f"Merge upstream develop@{target[:10]} into {branch}"
    if cs:
        msg += "\n\nUNRESOLVED CONFLICTS committed with markers; repair on this branch."
    git(root, "commit", "-s", "--no-verify", "-m", msg)
    if cs:
        code = 2 if any(c["kind"] in ("other", "local") for c in cs) else 1
        return emit("merge", False, code, "repair the committed conflict markers on this branch (draft PR)",
                    conflicts=cs, branch=branch, committed=True, **p)
    return emit("merge", True, 0, "run: sync_upstream.py check", branch=branch, committed=True, **p)


def cmd_conflicts(a, root):
    return conflict_result("conflicts", root)


# ---- check ------------------------------------------------------------------
def tail_matches(text, rx, limit=30):
    return [ln.strip() for ln in text.splitlines() if re.search(rx, ln)][:limit]


def hook_tests(root):
    p = Path(root) / "local/FORK_SURFACE.md"
    if not p.exists():
        return []
    res = []
    for m in re.finditer(r"^[-*\s]*`?Tests:`?\s*`?([^`\n]+?)`?\s*$", p.read_text(), re.M):
        parts = shlex.split(m.group(1))
        if len(parts) >= 3 and parts[2] == "-run":
            res.append(parts)
    return res


def cmd_check(a, root):
    cfg = config(root)
    d = {"build_errors": [], "test_failures": [], "surface_violations": []}
    steps, skipped, escalate = [], [], []
    have_go = bool(shutil.which("go")) and not a.no_go

    def go(name, cwd, args, key, rx=r"FAIL|^#|\.go:\d+"):
        p = run(["go", *args], Path(root) / cwd)
        steps.append({"step": name, "ok": p.returncode == 0})
        if p.returncode:
            d[key] += tail_matches(p.stdout + p.stderr, rx) or [f"{name} exited {p.returncode}"]

    if have_go:
        for mod in cfg.get("go_modules", []):
            if not (Path(root) / mod / "go.mod").exists():
                continue
            go(f"build {mod}", mod, ["build", "./..."], "build_errors")
            go(f"vet {mod}", mod, ["vet", "./..."], "build_errors")
            t = ["test", "./...", "-count=1", "-timeout=10m"]
            if mod in cfg.get("go_skip", {}):
                t += ["-skip", cfg["go_skip"][mod]]
            go(f"test {mod}", mod, t, "test_failures")
        for i, (mod, pkg, _, rx) in enumerate(h[:4] for h in hook_tests(root)):
            go(f"hook-test-{i}", mod, ["test", pkg, "-run", rx], "test_failures")
    elif not a.no_go:
        skipped.append("go (not installed)")
    p = run([sys.executable, "-m", "unittest", "discover", "-s", "local/scripts", "-p", "test_*.py"], root)
    steps.append({"step": "python", "ok": p.returncode == 0})
    if p.returncode:
        d["test_failures"] += tail_matches(p.stderr, r"FAIL|ERROR") or ["python unittest failed"]
    fs = Path(root) / "local/scripts/check_fork_surface.py"
    cmd = [sys.executable, str(fs), "--root", str(root)]
    if a.base:   # budget_raised vs the PR base; sync PRs always carry upstream-sync
        cmd += ["--base", a.base]
        for lb in ["upstream-sync", *a.label]:
            cmd += ["--label", lb]
    p = run(cmd, root)
    try:
        v = json.loads(p.stdout).get("violations", []) or ([json.loads(p.stdout)["error"]] if p.returncode == 2 else [])
    except (ValueError, KeyError):
        v = [p.stderr.strip()[:300] or "check_fork_surface.py gave no JSON"]
        escalate.append("malformed checker output")
    if p.returncode not in (0, 1):   # tool error (2), regression (3) or crash: never a pass
        escalate.append(f"checker exit {p.returncode}")
    if p.returncode and not v:
        v = [f"check_fork_surface.py exited {p.returncode}"]
    if a.base:   # old sha from the base branch, before this branch advanced it
        old, new = read_version(root, a.base)[0], read_version(root)[0]
        if old != new:
            v = v + added_ignored(root, old, new)
    escalate += [x for x in v if isinstance(x, dict) and x.get("kind") in cfs.ESCALATE_KINDS]
    d["surface_violations"] += [x if isinstance(x, str) else json.dumps(x) for x in v]
    steps.append({"step": "fork-surface", "ok": p.returncode == 0})
    changed, _ = neutralize(Path(root))
    if changed:
        git(root, "checkout", "--", *changed, check=False)   # leave the tree as found; report the drift
        d["surface_violations"].append(f"workflow neutralization drift: {', '.join(changed)}")
    steps.append({"step": "neutralized", "ok": not changed})
    ok = not (d["build_errors"] or d["test_failures"] or d["surface_violations"] or skipped)
    if escalate:
        code, nxt = 2, "ESCALATE: fork-surface escalate kind; do not auto-repair"
    elif ok:
        code, nxt = 0, "run: sync_upstream.py report"
    else:
        code, nxt = 1, "repair: fix build_errors, then test_failures, then surface_violations; rerun check"
    return emit("check", ok, code, nxt, steps=steps, skipped=skipped, escalate=bool(escalate), **d)


# ---- report -----------------------------------------------------------------
def load_json(path):
    if not path:
        return {}
    try:
        d = json.loads(Path(path).read_text())
        return d if isinstance(d, dict) else {}
    except (OSError, ValueError):
        return {}


def cmd_report(a, root):
    st, mg, ck = load_json(a.status), load_json(a.merge), load_json(a.check)
    short, tags = st.get("short", "?"), st.get("tags", [])
    if (st and not st.get("ok", False)) or not st:   # status failed (exit 2/3) or missing
        outcome = "escalate"
    elif mg and not mg.get("ok", True):
        outcome = "escalate"
    elif "ok" not in ck or ck.get("escalate"):   # missing/malformed check result is never a pass
        outcome = "escalate"
    elif not ck.get("ok"):
        outcome = "failing"
    else:
        outcome = "clean"
    labels = ["upstream-sync"] + (["upstream-release"] if st.get("release") else []) \
        + ([] if outcome == "clean" else ["sync-failing"])
    if not st.get("ok", False):
        title = ""   # leave an open PR's title alone
    elif st.get("release"):
        title = f"Upstream release: {st['newest_tag']} (+develop@{short})"
    else:
        title = f"Upstream sync: develop@{short} ({st.get('commits', 0)} commits)"
    cs = mg.get("conflicts", [])
    kinds = {k: sum(1 for c in cs if c["kind"] == k) for k in ("generated", "neutralized", "hook", "local", "other")}
    L = [f"## {title}", "", f"Result: {'PASS' if outcome == 'clean' else 'FAIL'} ({outcome})", ""]
    if tags:
        L += ["### Upstream tags included", *(f"- {t}" for t in tags), ""]
    L += ["### Conflicts by kind", *(f"- {k}: {n}" for k, n in kinds.items()), ""]
    for key, name in (("build_errors", "Build errors"), ("test_failures", "Test failures"),
                      ("surface_violations", "Surface violations")):
        items = ck.get(key, [])
        L += [f"### {name}", *([f"- `{x}`" for x in items] or ["- none"]), ""]
    if not st.get("ok", False):
        L += [f"Status failed: {st.get('next_action', 'no status result')}", ""]
    if mg and not mg.get("ok", True):
        L += [f"Merge: {mg.get('next_action', '')}", ""]
    if a.run_url:
        L += [f"Run: {a.run_url}", ""]
    md = "\n".join(L)
    if a.out:
        Path(a.out).write_text(md)
    return emit("report", True, 0, "publish", outcome=outcome, title=title, labels=labels,
                draft=outcome == "escalate", issue=outcome == "escalate",
                issue_title=f"Upstream sync needs repair: develop@{short}",
                reason=st.get("next_action", "") if not st.get("ok", False) else "")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", default=str(ROOT))
    ap.add_argument("--upstream-remote", default=None)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("status"); s.set_defaults(fn=cmd_status)
    s.add_argument("--ref"); s.add_argument("--force", action="store_true")
    s.add_argument("--base", help="ref whose UPSTREAM_VERSION the PR builds on (default HEAD)")
    s.add_argument("--tags-base", help="ref (develop) the reported tag list is measured from")
    m = sub.add_parser("merge"); m.set_defaults(fn=cmd_merge)
    m.add_argument("--ref"); m.add_argument("--force", action="store_true")
    m.add_argument("--bump-only", action="store_true"); m.add_argument("--reuse", action="store_true",
                   help="stay on the current branch (the open sync PR's)")
    m.add_argument("--branch"); m.add_argument("--date", help="yyyymmdd (tests)")
    n = sub.add_parser("neutralize"); n.set_defaults(fn=cmd_neutralize)
    n.add_argument("--commit", action="store_true")
    sub.add_parser("conflicts").set_defaults(fn=cmd_conflicts)
    c = sub.add_parser("check"); c.set_defaults(fn=cmd_check)
    c.add_argument("--no-go", action="store_true")
    c.add_argument("--base", help="PR base ref (e.g. origin/develop): enables budget_raised and the old-sha range check")
    c.add_argument("--label", action="append", default=[], help="PR label (e.g. surface-budget-raise)")
    r = sub.add_parser("report"); r.set_defaults(fn=cmd_report)
    r.add_argument("--status"); r.add_argument("--merge"); r.add_argument("--check")
    r.add_argument("--run-url"); r.add_argument("--out")
    a = ap.parse_args(argv)
    try:
        return a.fn(a, Path(a.root))
    except Escalate as e:
        return emit(a.cmd, False, 2, f"ESCALATE: {e}")
    except Regression as e:
        return emit(a.cmd, False, 3, f"sha= regression: {e}")


if __name__ == "__main__":
    sys.exit(main())
