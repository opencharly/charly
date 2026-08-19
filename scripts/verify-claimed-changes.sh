#!/usr/bin/env bash
# verify-claimed-changes.sh — does this branch's DIFF match what its commits CLAIM?
#
# WHY THIS EXISTS. Three defects in one cutover shared one shape: a claim was verified against
# something that was not the committed object.
#
#   1. A CHANGELOG H1 was fixed and verified in the WORKING TREE, then reverted by the
#      `git checkout -- .` half of a scoping dance because it was not in the stash. Reported
#      fixed twice before it was true; caught only by a validator reading `git show HEAD:<path>`.
#   2. A docs page was regenerated while its upstream projection sat at a stale pin, so the
#      generator faithfully emitted MAIN's text. The commit landed with a message describing a
#      fix its diff no longer contained, and the page was byte-identical to main.
#   3. A composition claim was checked against a submodule working tree left on an unmerged
#      branch instead of at its pin, so the check confirmed the branch rather than the pin.
#
# A diff review catches NONE of these. Each looks like a plausible edit; only comparing the
# resulting BLOB against the base — or noticing a claimed path missing from the change set —
# exposes them. This script is that comparison, in the two modes those failures took.
#
# THE CHECK THAT EARNS ITS KEEP is "changed then undone": a path some commit on this branch
# edited, which the branch's NET diff no longer contains. That is exactly failures 1 and 2 above,
# and it is invisible to every review that reads the net diff — the file is simply not in it.
#
# There is a second, near-vacuous check for a path that IS in the diff while its blob equals the
# base's. Git will not normally list such a path at all (an identical blob is not a change), so it
# fires only on oddities like a mode-only change. It is kept because it costs one rev-parse and
# the cutover that motivated this script proved the author's intuition about "cannot happen" is
# not evidence — but the load-bearing half is the one above, and this comment says so rather than
# advertising two equal modes.
#
# Usage:  scripts/verify-claimed-changes.sh [<base>] [<head>] [-C <repo>]
# Default base origin/main, head HEAD, repo the current directory.
# Exit 0 clean, 1 if anything is reverted or claimed-but-absent.

set -uo pipefail

base="origin/main"
head="HEAD"
repo="."
while [ $# -gt 0 ]; do
  case "$1" in
    -C) repo="$2"; shift 2 ;;
    *)  if [ "$base" = "origin/main" ] && [ "${1:-}" != "" ]; then base="$1"; else head="$1"; fi; shift ;;
  esac
done
g() { git -C "$repo" "$@"; }

if ! g rev-parse --verify -q "$base" >/dev/null; then
  echo "verify-claimed-changes: base '$base' does not resolve in $repo" >&2
  exit 2
fi

rc=0
changed=$(g diff --name-only "$base".."$head")

# The near-vacuous check: in the diff, but the blob is identical to the base's.
while IFS= read -r p; do
  [ -n "$p" ] || continue
  a=$(g rev-parse "$base:$p" 2>/dev/null || echo "")
  b=$(g rev-parse "$head:$p" 2>/dev/null || echo "")
  if [ -n "$a" ] && [ "$a" = "$b" ]; then
    echo "REVERTED — in the diff but byte-identical to $base: $p"
    rc=1
  fi
done <<< "$changed"

# MODE 2 — a path some commit on this branch CHANGED, which the branch's net diff does not
# contain. That is a change made and then undone within the same branch, and it is invisible to
# MODE 1 precisely because the path drops out of `--name-only` entirely.
#
# This is the mechanical form. An earlier draft of this script tried to read claimed paths out of
# commit messages and CHANGELOG prose; it reported CLEAN on the real failure that motivated it,
# because neither the commit subject ("the two size figures are alternative backends") nor the
# CHANGELOG ("the `ollama` page") ever wrote the path. Prose is not a reliable index of intent.
# What IS reliable: the branch touched the file, and the branch's net effect no longer does.
touched=$(g log --format=%H "$base".."$head" | while read -r c; do
  g diff --name-only "$c^" "$c" 2>/dev/null
done | sort -u)
while IFS= read -r p; do
  [ -n "$p" ] || continue
  grep -qxF "$p" <<< "$changed" && continue                  # still in the net diff — fine
  g cat-file -e "$base:$p" 2>/dev/null || continue           # not in base: an add later dropped
  g cat-file -e "$head:$p" 2>/dev/null || continue           # deleted on purpose — not this bug
  echo "CHANGED THEN UNDONE — a commit on this branch edited it, the net diff does not: $p"
  rc=1
done <<< "$touched"

if [ "$rc" -eq 0 ]; then
  echo "verify-claimed-changes: clean — every changed path really changed, and nothing this branch edited was undone."
fi
exit $rc
