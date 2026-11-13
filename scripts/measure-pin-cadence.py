#!/usr/bin/env python3
"""Measure the pin cadence of this repo's own `box/*` twins — the derivation of the
neglect bound the pin-currency control ships.

WHY THIS EXISTS. `charly task pin-current` fails on NEGLECT: a twin that is NOT
CURRENT — behind, advanced past the served ref by a squash-merge, or not
classifiable because there is no checkout to judge it — AND that this repo has
not advanced for longer than the bound. The bound (168 h) is a number somebody
chose, and a chosen number that cannot be re-derived is a guess wearing a
measurement's clothes. This script produces the derivation, from this repo's own
history, over the real remotes:

  * for every day in the window, the pin this repo RECORDED for each twin, and the
    ref that twin's upstream served at that moment;
  * the CLASS of that pair — the same classes the control names, decided by the
    same ancestry test the control uses — and how long this repo had gone without
    advancing it, the age the bound is measured against;
  * the sequence of this repo's OWN advances of each twin, so the GAP between two
    advances is visible: a gap inside the bound is ordinary operation, a gap past
    it is a lapse. The bound has to sit above the first and below the second.

Three rates come out, and the bound is placed BETWEEN them:

  behind_day_rate      — the share of days on which at least one twin was behind.
  not_current_day_rate — the share of days on which at least one twin was not
                          current in ANY class. This is the set the control judges,
                          so it is the rate a gate on "not current" is red on.
  stale_day_rate       — the share of days on which at least one twin was not
                          current AND un-advanced past the bound. These are the days
                          the control fires, and each one must be a day this repo
                          genuinely lapsed.

The CLASS TALLY over the window is printed with them, because "not current" and
"behind" are NOT the same set: a squash-merged upstream advances without
descending from its previous head, so its pins land in `not-on-the-served-line`.
The tally is what says whether the two readings coincide IN THIS REPO'S HISTORY,
rather than assuming it.

The classifier that produces the tally is itself PROVED, by `--self-test`: it
builds a scratch twin with the shapes the control has to tell apart and asserts
the class each one earns. A tally from a classifier that cannot tell the classes
apart would be a tally of nothing — and the proxy this script used to carry (the
pin is not the tip, so it is behind) fails exactly that way on a squash-advanced
pin, which is the class the org's own squash merges produce.

Usage:
  scripts/measure-pin-cadence.py [--days N] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
                                  [--bound-hours H] [--repo DIR]
  scripts/measure-pin-cadence.py --self-test

The window is named by DATES, not only by a day count, because a day count slides
with the wall clock: the same command run tomorrow measures a different window and
answers a different question. Cite the window by its dates, and re-derive it with
the same dates.

Requires the `box/*` twins to be checked out (their `origin/main` is the served
side, and the ancestry test reads their object stores) and this repo's history to
be complete.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone

TWINS = ["box/arch", "box/cachyos", "box/debian", "box/fedora", "box/ubuntu", "box/omarchy"]


def sh(cmd: str, cwd: str) -> str:
    return subprocess.run(cmd, cwd=cwd, shell=True, capture_output=True, text=True).stdout


def sh_rc(cmd: str, cwd: str) -> tuple[str, int]:
    """Run a command and return its stdout AND its OWN exit code.

    The exit code is the evidence here: `merge-base --is-ancestor` answers the
    classification question with it, and a shell pipeline would have thrown it
    away.
    """
    p = subprocess.run(cmd, cwd=cwd, shell=True, capture_output=True, text=True)
    return p.stdout, p.returncode


# The classes the CONTROL names, and the ones it treats as neglect-eligible.
# These mirror the producer's own definition (plugin-task's `staleEligible`): a
# pin that is not current AND that its owner has not advanced for longer than
# the bound is STALE.
#
# `unmerged` — a recorded commit AHEAD of the served ref — is NOT eligible: the
# producer treats it as a structural failure of its own, because a pin this
# repo recorded ahead of what the remote serves is not this repo's neglect, it
# is a pin that names a commit the repo does not serve.
#
# `unjudgeable` IS eligible, on the producer's OWN reason. Its `differs` class
# — no checkout to judge the difference — is eligible on the ground that such a
# pin "is still NOT CURRENT", and the producer's `ancestryClass` has no rc=128
# branch at all, so today it folds an absent object into `not-on-the-served-line`
# and prints a causal explanation it never measured (opencharly/plugin-task#32
# asks for the 128 to be told apart). This script keeps the NAME the producer
# loses and the JUDGEMENT the producer has: a pair whose objects are missing is
# not current, so an un-advanced one is neglect. A measurement that dropped the
# class from its own set would under-count the control's red days.
STALE_ELIGIBLE = ("behind", "not-on-the-served-line", "unjudgeable")


def classify(twin: str, rec: str, served: str) -> str:
    """The class the CONTROL would name for this (recorded, served) pair.

    The test is the producer's own: `merge-base --is-ancestor`, read in the
    twin's own git dir, and the EXIT CODE decides. A twin whose object store
    does not carry one of the two commits answers 128 — that pair is reported
    as `unjudgeable`, its OWN class: it is counted as not current, and it keeps
    its own name, because "the objects are not here" is not a claim about the
    pin. (The producer folds it into `not-on-the-served-line` and prints a
    causal explanation it never measured; that fold is opencharly/plugin-task#32,
    and it does not bite here — this window holds no unjudgeable pair.)
    """
    if rec == served:
        return "cur"
    _, rc = sh_rc(f"git -C {twin} merge-base --is-ancestor {rec} {served}", twin)
    if rc == 0:
        return "behind"
    if rc == 128:
        return "unjudgeable"
    _, rc2 = sh_rc(f"git -C {twin} merge-base --is-ancestor {served} {rec}", twin)
    if rc2 == 0:
        return "unmerged"
    if rc2 == 128:
        return "unjudgeable"
    return "not-on-the-served-line"


def day(ts: int) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%d")


# ---------------------------------------------------------------------------
# The self-test: the classifier must DISCRIMINATE the classes the criterion names.
# ---------------------------------------------------------------------------
def self_test() -> int:
    """Prove the classifier tells the classes apart, on a scratch twin.

    WHY THIS EXISTS. The criterion names a SET of classes — `behind`,
    `not-on-the-served-line`, `differs` — and this script's class tally is the
    evidence for what that set contains in this repo's history. A classifier
    that cannot tell the classes apart cannot support a criterion that names
    them: the proxy this script used to carry — "the pin is not the tip, so it
    is behind" — calls a SQUASH-advanced pin `behind`, because the two commits
    differ, and that is precisely the class the org's own squash merges
    produce. Under that proxy the tally would have said `behind=145` and
    "not current coincides with behind HERE" without ever having asked.

    So the classification is tested where it can be wrong: on a scratch twin,
    one arm per class this classifier can return, each with the (recorded,
    served) pair the control would judge and the class it must name. The old
    proxy fails the squash arm; the ancestry test passes all five.

    The arms also carry each class's NEGLECT-ELIGIBILITY, because that set is
    what the measurement does with the class it is handed: a measurement whose
    eligible set had drifted from the control's would count the control's red
    days wrongly, and silently — the number would still look like a number.

    The fixture is a scratch repo in a temp dir. Nothing here touches a real
    twin, and nothing here reads the operator's git identity.
    """
    os.environ["GIT_CONFIG_GLOBAL"] = "/dev/null"
    os.environ["GIT_CONFIG_SYSTEM"] = "/dev/null"
    work = tempfile.mkdtemp(prefix="pin-cadence-selftest-")
    try:
        twin = os.path.join(work, "twin")
        os.makedirs(twin)

        def git(*args: str) -> str:
            p = subprocess.run(
                ["git", "-C", twin, "-c", "user.name=t", "-c", "user.email=t@t", *args],
                capture_output=True, text=True,
            )
            if p.returncode != 0:
                raise SystemExit(f"SELF-TEST FIXTURE FAILED: git {' '.join(args)}\n{p.stderr}")
            return p.stdout.strip()

        git("init", "-q", "-b", "main")
        Path_f = os.path.join(twin, "f")
        with open(Path_f, "w", encoding="utf-8") as fh:
            fh.write("v1\n")
        git("add", "-A")
        git("commit", "-qm", "v1")
        c1 = git("rev-parse", "HEAD")
        with open(Path_f, "w", encoding="utf-8") as fh:
            fh.write("v2\n")
        git("commit", "-qam", "v2")
        c2 = git("rev-parse", "HEAD")
        # A squash-merge: a commit on an orphan branch, so nothing descends from c1.
        git("checkout", "-q", "--orphan", "squashed")
        subprocess.run(["git", "-C", twin, "rm", "-q", "-rf", "."], capture_output=True, text=True)
        with open(Path_f, "w", encoding="utf-8") as fh:
            fh.write("v3\n")
        git("add", "-A")
        git("commit", "-qm", "v3")
        c3 = git("rev-parse", "HEAD")
        git("checkout", "-q", "-B", "main", c2)

        # A commit the twin's object store does NOT carry: the ancestry question
        # is undecidable, and git answers 128.
        missing = "0" * 40

        # (recorded, served, expected class, what the shape is)
        arms = [
            (c2, c2, "cur", "the pin IS the served ref"),
            (c1, c2, "behind", "the served ref descends from the recorded pin"),
            (c1, c3, "not-on-the-served-line", "a squash-merge: neither descends from the other"),
            (c2, c1, "unmerged", "the recorded pin is AHEAD of the served ref"),
            (missing, c2, "unjudgeable", "the recorded commit is not in the twin's object store"),
        ]
        # The EXPECTED neglect-eligibility of each class — the independent
        # statement this test checks STALE_ELIGIBLE against. It is deliberately
        # NOT derived from STALE_ELIGIBLE: a test that recomputed the expected
        # value from the code under test could not fail, and a test that cannot
        # fail is not a test. `cur` is never neglect, `unmerged` is the
        # producer's own structural failure rather than this repo's neglect,
        # and a pair that is not demonstrably current IS not current.
        eligible = {
            "cur": False,
            "behind": True,
            "not-on-the-served-line": True,
            "unmerged": False,
            "unjudgeable": True,
        }
        failed = 0
        print("the classifier, on a scratch twin:")
        for rec, served, expected, why in arms:
            got = classify(twin, rec, served)
            cls_ok = got == expected
            want_elig = eligible[expected]
            elig_ok = (got in STALE_ELIGIBLE) == want_elig
            if not (cls_ok and elig_ok):
                failed += 1
            note = ""
            if not cls_ok:
                note += f"  — got {got!r}"
            if not elig_ok:
                note += f"  — neglect-eligible {got in STALE_ELIGIBLE}, expected {want_elig}"
            print(f"  {'ok  ' if cls_ok and elig_ok else 'FAIL'}  {expected:24s}  ({why}){note}")
        if failed:
            print(f"\nSELF-TEST FAILED: {failed} arm(s) did not behave as specified. The tally this"
                  "\nscript prints cannot be trusted, and neither can the criterion it"
                  "\nsupports.", file=sys.stderr)
            return 1
        print("PIN-CADENCE-SELF-TEST-OK")
        return 0
    finally:
        shutil.rmtree(work, ignore_errors=True)


def recorded_pins(repo: str, path: str) -> list[tuple[int, str]]:
    """(time, gitlink) for every commit of this repo that touched `path`."""
    out: list[tuple[int, str]] = []
    for line in sh(f"git log --format='%ct %H' -- {path}", repo).splitlines():
        ts, commit = line.split()
        tree = sh(f"git ls-tree {commit} {path}", repo).split()
        if len(tree) >= 3:
            out.append((int(ts), tree[2]))
    out.sort()
    return out


def served_tips(repo: str, path: str) -> list[tuple[int, str]]:
    """(time, tip) for the twin's own origin/main."""
    out: list[tuple[int, str]] = []
    for line in sh(f"git -C {path} log --format='%ct %H' origin/main", repo).splitlines():
        ts, sha = line.split()
        out.append((int(ts), sha))
    out.sort()
    return out


def at(history: list[tuple[int, str]], when: int) -> tuple[int, str] | None:
    found = None
    for ts, sha in history:
        if ts <= when:
            found = (ts, sha)
        else:
            break
    return found


def parse_day(text: str) -> int:
    return int(datetime.strptime(text, "%Y-%m-%d").replace(tzinfo=timezone.utc).timestamp())


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--days", type=int, default=31, help="the window, in days back from now (default 31)")
    ap.add_argument("--since", help="the window's first day, YYYY-MM-DD (UTC)")
    ap.add_argument("--until", help="the window's last day, YYYY-MM-DD (UTC); default: today")
    ap.add_argument("--bound-hours", type=int, default=168, help="the neglect bound, in hours (default 168)")
    ap.add_argument("--list", action="store_true", help="list every lapse, not just their count and extent")
    ap.add_argument("--list-days", action="store_true", help="list every STALE day, with the age that made it stale")
    ap.add_argument("--repo", default=".", help="this repo's root (default .)")
    ap.add_argument("--self-test", action="store_true",
                    help="prove the classifier discriminates the classes, on a scratch twin, and exit")
    args = ap.parse_args()

    if args.self_test:
        return self_test()

    repo = args.repo
    now = int(sh("date -u +%s", repo).strip())
    if args.since:
        start = parse_day(args.since)
    else:
        start = now - args.days * 86400
    end = parse_day(args.until) if args.until else now
    days = []
    d = start
    while d <= end:
        days.append(d)
        d += 86400

    behind_days: set[int] = set()
    not_current_days: set[int] = set()
    stale_days: set[int] = set()
    classes: dict[str, int] = {}                             # class -> (day,twin) pairs
    stale_detail: list[tuple[int, str, float]] = []          # (day, path, age in days)
    healthy_gaps: list[tuple[float, str, int, int]] = []   # (days, path, from, to)
    lapses: list[tuple[float, str, int, int]] = []

    for path in TWINS:
        pins = recorded_pins(repo, path)
        tips = served_tips(repo, path)
        if not pins or not tips:
            print(f"{path}: no history — SKIPPED", file=sys.stderr)
            continue
        behind = not_current = stale = 0
        for day_ts in days:
            pin = at(pins, day_ts)
            tip = at(tips, day_ts)
            if not pin or not tip:
                continue
            state = classify(path, pin[1], tip[1])
            if state == "cur":
                continue
            not_current += 1
            not_current_days.add(day_ts)
            classes[state] = classes.get(state, 0) + 1
            if state == "behind":
                behind += 1
                behind_days.add(day_ts)
            if state not in STALE_ELIGIBLE:
                continue
            age_h = (day_ts - pin[0]) / 3600.0
            if age_h > args.bound_hours:
                stale += 1
                stale_days.add(day_ts)
                stale_detail.append((day_ts, path, age_h / 24.0))
        print(f"{path:14s} behind on {behind:2d}/{len(days)} days,"
              f" not current on {not_current:2d}, stale on {stale:2d}")

        # The gaps between this repo's OWN advances of this twin. The trailing gap
        # (last advance -> now) is included: it is the age the control measures NOW.
        edges = [(ts, sha) for ts, sha in pins if ts <= now]
        seq = [e for e in edges]
        for i in range(len(seq) - 1):
            gap_d = (seq[i + 1][0] - seq[i][0]) / 86400.0
            rec = (gap_d, path, seq[i][0], seq[i + 1][0])
            (lapses if gap_d > args.bound_hours / 24.0 else healthy_gaps).append(rec)
        if seq:
            tail_d = (now - seq[-1][0]) / 86400.0
            rec = (tail_d, path, seq[-1][0], now)
            (lapses if tail_d > args.bound_hours / 24.0 else healthy_gaps).append(rec)

    n = len(days)
    print()
    print(f"window: {n} days, {day(start)} .. {day(end)}   bound: {args.bound_hours}h")
    print(f"behind_day_rate:      {len(behind_days)}/{n} = {len(behind_days) / n * 100:.1f}%"
          "  (the legacy criterion: a gate that fails on 'behind' is red on these days)")
    print(f"not_current_day_rate: {len(not_current_days)}/{n} = {len(not_current_days) / n * 100:.1f}%"
          "  (every class but `cur`: behind, not-on-the-served-line, unmerged, unjudgeable)")
    print(f"stale_day_rate:       {len(stale_days)}/{n} = {len(stale_days) / n * 100:.1f}%"
          "  (the criterion the control ships: not current AND un-advanced past the bound)")
    if classes:
        tally = ", ".join(f"{k}={classes[k]}" for k in sorted(classes))
        print(f"classes over the window ({len(days)} days x {len(TWINS)} twins): {tally}")
        print("  (the not-current pairs, by the class the control would name — this is"
              " what says whether 'not current' and 'behind' coincide HERE)")

    if healthy_gaps:
        longest = max(healthy_gaps)
        print()
        print(f"longest gap WITHIN the bound (ordinary operation): {longest[0]:.1f} d"
              f"  ({longest[1]}, {day(longest[2])} -> {day(longest[3])})")
    if lapses:
        print(f"gaps PAST the bound (lapses): {len(lapses)}"
              f"  — shortest {min(g[0] for g in lapses):.1f} d, longest {max(g[0] for g in lapses):.1f} d")
        if args.list:
            for gap_d, path, frm, to in sorted(lapses):
                print(f"    {gap_d:5.1f} d  {path:14s} {day(frm)} -> {day(to)}")
    if args.list_days and stale_detail:
        print()
        print("every STALE day in the window, with the age that made it stale:")
        for day_ts, path, age_d in sorted(stale_detail):
            print(f"    {day(day_ts)}  {path:14s} age {age_d:5.1f} d  (bound {args.bound_hours / 24.0:.1f} d)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
