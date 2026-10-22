#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""All `gh` calls of the PAIR upstream sync (plan section 4.4/4.5). Stdlib only; honours GH_REPO.

Subcommands: find-pr | verify-pr | stalled | disable-auto | veto | mark-conflict | ready | enroll | issue | sweep.
Each prints ONE JSON object {ok, step, ...}. Exit codes: 0 ok, 1 the outcome is not clean (enroll
refused, issue skipped), 2 a gh call failed. The workflow decides the run's exit status.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import tomllib
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PR_FIELDS = "number,baseRefName,headRefName,headRefOid,isDraft,labels,autoMergeRequest,isCrossRepository"
BAD_RULES = ("merge_queue", "pull_request", "required_linear_history")
STALL = timedelta(hours=24)


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


def emit(step, ok, code, **kw):
    print(json.dumps({"ok": ok, "step": step, **kw}, indent=2))
    return code


def fork():
    return os.environ.get("GH_REPO") or "{owner}/{repo}"   # gh api fills the placeholders itself


def marker(branch, outcome):
    return f"<!-- upstream-sync: {branch} {outcome} -->"


VETO_RE = re.compile(r"<!-- upstream-sync-veto: ([0-9a-f]{40}) (escalate|failing) -->")


def veto_of(body):
    """(sha, outcome) of the last strictly-formed veto marker in a PR body, else None."""
    found = VETO_RE.findall(body if isinstance(body, str) else "")
    return found[-1] if found else None


def vetoed_head(pr):
    """The veto outcome when the body's veto sha is still the PR's current head, else None."""
    v = veto_of(pr.get("body"))
    return v[1] if v and v[0] == pr.get("headRefOid") else None


def names(pr):
    return [x["name"] for x in pr.get("labels", [])]


def enabled_at(pr):
    return (pr.get("autoMergeRequest") or {}).get("enabledAt")


def cmd_find_pr(a):
    label, prefix = ("sync-demo", "upstream-sync-demo/") if a.demo else ("upstream-sync", "upstream-sync/")
    prs = gh_json("pr", "list", "--base", "develop", "--label", label, "--state", "open", "--limit", "50", "--json", PR_FIELDS) or []
    prs = [p for p in prs if not p["isCrossRepository"] and p["headRefName"].startswith(prefix)]
    if a.branch:   # a just-created PR: exact branch only, never "the newest"
        prs = [p for p in prs if p["headRefName"] == a.branch]
    if not prs:
        return emit("find-pr", True, 0, number=None, branch=None, draft=None, base=None, labels=[],
                    auto_merge_enabled_at=None, head_sha=None)
    p = max(prs, key=lambda x: x["number"])
    return emit("find-pr", True, 0, number=p["number"], branch=p["headRefName"], draft=p["isDraft"], base=p["baseRefName"],
                labels=names(p), auto_merge_enabled_at=enabled_at(p), head_sha=p["headRefOid"])


def file_issue(branch, outcome, title, body, sha, run_url, labels):
    """Dedup on the marker: comment on the open issue that has it, else create one."""
    m = marker(branch, outcome)
    issues = gh_json("issue", "list", "--label", "upstream-sync", "--state", "open", "--limit", "200",
                     "--json", "number,body") or []
    for i in issues:
        if m in (i.get("body") or ""):
            gh("issue", "comment", str(i["number"]), "--body",
               f"Still present at upstream sha {sha}.\n\nRun: {run_url}")
            return {"action": "commented", "number": i["number"], "marker": m}
    with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
        f.write(f"{body.rstrip()}\n\n{m}\n")
    try:
        args = ["issue", "create", "--title", title, "--body-file", f.name, "--label", "upstream-sync"]
        for lb in labels:
            args += ["--label", lb]
        out = gh(*args).stdout.strip()
    finally:
        os.unlink(f.name)
    num = re.search(r"/(\d+)\s*$", out)
    return {"action": "created", "number": int(num.group(1)) if num else None, "marker": m}


def cmd_issue(a):
    body = Path(a.body_file).read_text()
    r = file_issue(a.branch, a.outcome, a.title, body, a.sha, a.run_url, a.labels)
    return emit("issue", True, 0, **r)


def cmd_stalled(a):
    p = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS)
    at = a.enabled_at or enabled_at(p)   # the pre-disarm snapshot wins: the PR is already disarmed here
    if not at:
        return emit("stalled", True, 0, stalled=False, age_hours=None, issue=None)
    age = datetime.now(timezone.utc) - datetime.fromisoformat(at.replace("Z", "+00:00"))
    hours = round(age.total_seconds() / 3600, 1)
    if age <= STALL:
        return emit("stalled", True, 0, stalled=False, age_hours=hours, issue=None)
    branch = p["headRefName"]
    body = (f"Auto-merge has been enabled on PR #{a.pr} ({branch}) since {at} ({hours} h) without merging.\n\n"
            f"Run: {a.run_url}")
    r = file_issue(branch, "enroll_stalled", f"Upstream sync needs attention: {branch} [enroll_stalled]",
                   body, p["headRefOid"], a.run_url, [])
    return emit("stalled", True, 0, stalled=True, age_hours=hours, issue=r)


def cmd_verify_pr(a):
    """Re-verify a PR identity by number: branch, base develop, same repo. Mismatch -> exit 1 (fail the run)."""
    p = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS)
    p = p if isinstance(p, dict) else {}
    why = [w for w, bad in ((f"head is {p.get('headRefName')!r}, not {a.branch!r}", p.get("headRefName") != a.branch),
                            (f"base is {p.get('baseRefName')!r}, not 'develop'", p.get("baseRefName") != "develop"),
                            ("PR is cross-repository", p.get("isCrossRepository") is not False)) if bad]
    return emit("verify-pr", not why, 1 if why else 0, pr=a.pr, reasons=why)


def disable_auto(pr):
    p = gh_json("pr", "view", str(pr), "--json", "autoMergeRequest")
    if not (p or {}).get("autoMergeRequest"):
        return False
    gh("pr", "merge", str(pr), "--disable-auto")
    return True


def cmd_disable_auto(a):
    return emit("disable-auto", True, 0, disabled=disable_auto(a.pr))


def cmd_veto(a):
    """Head-pinned veto: report whether this PR's head still carries an escalate/failing veto."""
    p = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS + ",body")
    p = p if isinstance(p, dict) else {}
    v = vetoed_head(p)
    if v and a.cand and a.cand != p.get("headRefOid"):
        v = None   # this run's candidate is a new head: the veto is cleared
    return emit("veto", True, 0, outcome=v or "", head_sha=p.get("headRefOid") or "")


def cmd_mark_conflict(a):
    """Add the sync-conflict label to a PR and verify it is present. Not present afterwards -> exit 1 (caller aborts)."""
    gh("pr", "edit", str(a.pr), "--add-label", "sync-conflict")
    p = gh_json("pr", "view", str(a.pr), "--json", "labels")
    ok = "sync-conflict" in names(p if isinstance(p, dict) else {})
    return emit("mark-conflict", ok, 0 if ok else 1, pr=a.pr, label_present=ok)


def cmd_ready(a):
    p = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS + ",body")
    v = vetoed_head(p)
    if v:   # unchanged head after an escalate/failing run is never clean
        return emit("ready", False, 1, draft_after=p["isDraft"], label_removed=False, vetoed=v)
    if p["isDraft"]:
        gh("pr", "ready", str(a.pr))
    removed = "sync-failing" in names(p)
    if removed:
        gh("pr", "edit", str(a.pr), "--remove-label", "sync-failing")
    after = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS)
    return emit("ready", True, 0, draft_after=after["isDraft"], label_removed=removed)


def required_checks(config):
    if not Path(config).is_file():
        return None, f"config file not found: {config} (pass --config pointing at the trusted develop checkout)"
    try:
        v = tomllib.loads(Path(config).read_text()).get("required_checks")
    except (OSError, tomllib.TOMLDecodeError) as e:
        return None, f"cannot read required_checks from {config}: {e}"
    if not isinstance(v, list) or not v:
        return None, f"required_checks missing or empty in {config}"
    return set(v), None


def read_api(path):
    p = gh("api", path, check=False)
    if p.returncode:
        return None, f"{path} unreadable: {(p.stderr or p.stdout).strip()[:120]}"
    try:
        return json.loads(p.stdout), None
    except json.JSONDecodeError:
        return None, f"{path} returned non-JSON"


def check_prereqs(a):
    """Reasons (list of str) enrollment is refused; the kill-switch reason is kept apart."""
    reasons, f = [], fork()
    want, err = required_checks(a.config)
    if err:
        reasons.append(err)
    r, err = read_api(f"repos/{f}")
    if err:
        reasons.append(err)
    elif not isinstance(r, dict) or not all(isinstance(r.get(k), bool) for k in ("allow_auto_merge", "allow_merge_commit")):
        reasons.append(f"malformed repos/{f}")
    else:
        for k in ("allow_auto_merge", "allow_merge_commit"):
            if r.get(k) is not True:
                reasons.append(f"repo {k} is not true")
    p, err = read_api(f"repos/{f}/branches/develop/protection")
    ep = "branches/develop/protection"
    if err:
        reasons.append(err)
    elif not isinstance(p, dict):
        reasons.append(f"malformed {ep}")
    else:
        rsc, lin, rv = p.get("required_status_checks"), p.get("required_linear_history"), p.get("required_pull_request_reviews")
        checks = rsc.get("checks") if isinstance(rsc, dict) else None
        if (not isinstance(rsc, dict) or not isinstance(rsc.get("strict"), bool) or not isinstance(checks, list)
                or not all(isinstance(c, dict) for c in checks)
                or not isinstance(lin, dict) or not isinstance(lin.get("enabled"), bool)
                or not ("required_pull_request_reviews" not in p or isinstance(rv, dict))):
            reasons.append(f"malformed {ep}")
        else:
            got = {c.get("context") for c in checks}
            if want is not None and got != want:
                reasons.append(f"required checks differ: missing {sorted(want - got)}, extra {sorted(got - want)}")
            if rsc["strict"]:
                reasons.append("protection requires up-to-date branches (strict)")
            if lin["enabled"]:
                reasons.append("protection requires linear history")
            if "required_pull_request_reviews" in p:
                reasons.append("protection reviews required")
    rules, err = read_api(f"repos/{f}/rules/branches/develop")
    if err:
        reasons.append(err)
    elif not isinstance(rules, list) or not all(isinstance(x, dict) and isinstance(x.get("type"), str) for x in rules):
        reasons.append("malformed rules/branches/develop")
    else:
        bad = sorted({x.get("type") for x in rules if x.get("type") in BAD_RULES})
        if bad:
            reasons.append(f"rules on develop: {bad}")
    pr = gh_json("pr", "view", str(a.pr), "--json", PR_FIELDS + ",body")
    if not isinstance(pr, dict) or not isinstance(pr.get("isDraft"), bool):
        reasons.append("malformed pr view")
        return reasons
    if vetoed_head(pr):
        reasons.append(f"PR head {pr.get('headRefOid')} is vetoed ({vetoed_head(pr)}); a new head is required")
    if pr.get("baseRefName") != "develop":
        reasons.append(f"PR base is {pr.get('baseRefName')!r}, not 'develop'")
    if pr.get("isCrossRepository") is not False:
        reasons.append("PR is cross-repository")
    if pr["isDraft"]:
        reasons.append("PR is still a draft")
    for veto in ("workflow-review", "sync-failing", "ci-failing", "sync-conflict"):
        if veto in names(pr):
            reasons.append(f"PR carries the {veto} label")
    return reasons


def kill_switch_ok(demo_ref):
    v = os.environ.get("SYNC_AUTO_MERGE")
    return v == "demo" if demo_ref else v == "1"


def set_enrollment_line(body, line):
    if re.search(r"^Enrollment:.*$", body, re.M):
        return re.sub(r"^Enrollment:.*$", lambda _: line, body, count=1, flags=re.M)
    return f"{body.rstrip()}\n\n{line}\n"


def cmd_enroll(a):
    try:
        reasons = check_prereqs(a)
    except (GhError, AttributeError, KeyError, TypeError, ValueError) as e:
        reasons = [f"malformed prerequisite response: {str(e)[:100]}"]
    ks_reason = None
    if not kill_switch_ok(a.demo_ref):
        ks_reason = (f"kill switch SYNC_AUTO_MERGE={os.environ.get('SYNC_AUTO_MERGE')!r} does not permit "
                     f"{'demo' if a.demo_ref else 'regular'} enrollment")
        reasons.append(ks_reason)
    if not reasons:
        p = gh("pr", "merge", str(a.pr), "--auto", "--merge", "--match-head-commit", a.head, check=False)
        if p.returncode:
            reasons.append(f"gh pr merge --auto failed: {(p.stderr or p.stdout).strip()[:200]}")
    ok = not reasons
    armed = ok   # auto-merge is on from here: any failure below must disarm it
    try:
        if not ok:
            disable_auto(a.pr)   # disarm first: a failing report edit must never leave an armed auto-merge
        line = f"Enrollment: enrolled at {a.head}" if ok else f"Enrollment: refused: {'; '.join(reasons)}"
        report = Path(a.body)
        report.write_text(set_enrollment_line(report.read_text() if report.exists() else "", line))
        gh("pr", "edit", str(a.pr), "--body-file", str(report))
    except Exception as e:
        if not armed:
            raise
        try:
            disable_auto(a.pr)
        except Exception:
            pass   # best effort: the workflow's next-run disarm and the red run remain
        return emit("enroll", False, 1, outcome="enroll_refused", reasons=[f"post-arm error: {str(e)[:200]}"],
                    kill_switch_only=False)
    return emit("enroll", ok, 0 if ok else 1, outcome="enrolled" if ok else "enroll_refused",
                reasons=reasons, kill_switch_only=reasons == [ks_reason])


def cmd_sweep(a):
    issues = gh_json("issue", "list", "--label", "upstream-sync", "--state", "open", "--limit", "200",
                     "--json", "number,body") or []
    closed, state = [], {}
    for i in issues:
        m = re.search(r"<!-- upstream-sync: (\S+) (\S+) -->", i.get("body") or "")
        if not m:
            continue
        branch = m.group(1)
        if branch == "workflow-review":
            if a.no_held_drift:
                gh("issue", "close", str(i["number"]), "--comment", "No held-workflow drift in the latest run.")
                closed.append(i["number"])
            continue
        if branch not in state:
            prs = gh_json("pr", "list", "--head", branch, "--state", "all", "--json", "number,state") or []
            state[branch] = bool(prs) and all(p["state"] in ("MERGED", "CLOSED") for p in prs)
        if state[branch]:
            gh("issue", "close", str(i["number"]), "--comment", f"Sync PR for {branch} is merged or closed.")
            closed.append(i["number"])
    return emit("sweep", True, 0, closed=closed)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = ap.add_subparsers(dest="cmd", required=True)

    def add(name, fn):
        s = sub.add_parser(name)
        s.set_defaults(fn=fn)
        return s

    s = add("find-pr", cmd_find_pr)
    s.add_argument("--demo", action="store_true")
    s.add_argument("--branch", help="exact head branch (a PR just created)")
    s = add("stalled", cmd_stalled)
    s.add_argument("--pr", type=int, required=True); s.add_argument("--run-url", required=True)
    s.add_argument("--enabled-at", default="", help="enrollment timestamp snapshotted by find-pr before the disarm")
    s = add("disable-auto", cmd_disable_auto)
    s.add_argument("--pr", type=int, required=True)
    s = add("verify-pr", cmd_verify_pr)
    s.add_argument("--pr", type=int, required=True); s.add_argument("--branch", required=True)
    s = add("ready", cmd_ready)
    s.add_argument("--pr", type=int, required=True)
    s = add("veto", cmd_veto)
    s.add_argument("--pr", type=int, required=True); s.add_argument("--cand", default="")
    s = add("mark-conflict", cmd_mark_conflict)
    s.add_argument("--pr", type=int, required=True)
    s = add("enroll", cmd_enroll)
    s.add_argument("--pr", type=int, required=True); s.add_argument("--head", required=True)
    s.add_argument("--demo-ref", default=""); s.add_argument("--body", required=True, help="report.md")
    s.add_argument("--config", required=True, help="trusted develop checkout's local/upstream-sync.toml")
    s = add("issue", cmd_issue)
    s.add_argument("--branch", required=True); s.add_argument("--outcome", required=True)
    s.add_argument("--title", required=True); s.add_argument("--body-file", required=True)
    s.add_argument("--sha", required=True); s.add_argument("--run-url", required=True)
    s.add_argument("--labels", nargs="*", default=[])
    s = add("sweep", cmd_sweep)
    s.add_argument("--no-held-drift", action="store_true")
    a = ap.parse_args(argv)
    try:
        return a.fn(a)
    except GhError as e:
        return emit(a.cmd, False, 2, escalate=True, error=str(e))


if __name__ == "__main__":
    sys.exit(main())
