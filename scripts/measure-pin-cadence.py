#!/usr/bin/env python3
"""Measure the pin cadence of this repo's own `box/*` twins — the derivation of the
neglect bound the pin-currency control ships.

WHY THIS EXISTS. `charly task pin-current` fails on NEGLECT: a twin that is behind
the ref its own `.gitmodules` declares AND that this repo has not advanced for
longer than the bound. The bound (168 h) is a number somebody chose, and a chosen
number that cannot be re-derived is a guess wearing a measurement's clothes. This
script produces the derivation, from this repo's own history, over the real remotes:

  * for every day in the window, the pin this repo RECORDED for each twin, and the
    ref that twin's upstream served at that moment;
  * whether the twin was BEHIND on that day, and how long this repo had gone
    without advancing it — the age the bound is measured against;
  * the sequence of this repo's OWN advances of each twin, so the GAP between two
    advances is visible: a gap inside the bound is ordinary operation, a gap past
    it is a lapse. The bound has to sit above the first and below the second.

Two rates come out, and the bound is placed BETWEEN them:

  behind_day_rate — the share of days on which at least one twin was behind. A gate
                     that fails on "behind" is red on exactly these days, which is
                     why the criterion is not the distance.
  stale_day_rate  — the share of days on which at least one twin was behind AND
                     un-advanced past the bound. These are the days the control
                     fires, and each one must be a day this repo genuinely lapsed.

Usage:
  scripts/measure-pin-cadence.py [--days N] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
                                  [--bound-hours H] [--repo DIR]

The window is named by DATES, not only by a day count, because a day count slides
with the wall clock: the same command run tomorrow measures a different window and
answers a different question. Cite the window by its dates, and re-derive it with
the same dates.

Requires the `box/*` twins to be checked out (their `origin/main` is the served
side) and this repo's history to be complete.
"""

from __future__ import annotations

import argparse
import subprocess
import sys
from datetime import datetime, timezone

TWINS = ["box/arch", "box/cachyos", "box/debian", "box/fedora", "box/ubuntu", "box/omarchy"]


def sh(cmd: str, cwd: str) -> str:
    return subprocess.run(cmd, cwd=cwd, shell=True, capture_output=True, text=True).stdout


def iso(ts: int) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%d %H:%M")


def day(ts: int) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%d")


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
    args = ap.parse_args()

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
    stale_days: set[int] = set()
    stale_detail: list[tuple[int, str, float]] = []          # (day, path, age in days)
    healthy_gaps: list[tuple[float, str, int, int]] = []   # (days, path, from, to)
    lapses: list[tuple[float, str, int, int]] = []

    for path in TWINS:
        pins = recorded_pins(repo, path)
        tips = served_tips(repo, path)
        if not pins or not tips:
            print(f"{path}: no history — SKIPPED", file=sys.stderr)
            continue
        behind = stale = 0
        for day_ts in days:
            pin = at(pins, day_ts)
            tip = at(tips, day_ts)
            if not pin or not tip or tip[1] == pin[1]:
                continue
            behind += 1
            behind_days.add(day_ts)
            age_h = (day_ts - pin[0]) / 3600.0
            if age_h > args.bound_hours:
                stale += 1
                stale_days.add(day_ts)
                stale_detail.append((day_ts, path, age_h / 24.0))
        print(f"{path:14s} behind on {behind:2d}/{len(days)} days, stale on {stale:2d}")

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
    print(f"behind_day_rate: {len(behind_days)}/{n} = {len(behind_days) / n * 100:.1f}%"
          "  (a gate that fails on 'behind' is red on these days)")
    print(f"stale_day_rate:  {len(stale_days)}/{n} = {len(stale_days) / n * 100:.1f}%"
          "  (the criterion the control ships)")

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
