#!/usr/bin/env bash
# verify-changelog-h1.sh — every CHANGELOG/<CalVer>.md H1 must name its own filename.
#
# The convention: `# <filename-without-.md> — <title>`, byte-equal CalVer.
#
# WHY A SCRIPT. This is not a style nit, it is a RECURRING mechanical failure with a known
# population: opencharly/charly#332 records nine instances (charly 1, sdk 6, spec 2), and a tenth
# landed on the marketplace repo's main while that issue was open.
#
# The mechanism is always the same, and it is not carelessness. A merge-time stamp does
# `git mv CHANGELOG/<placeholder>.md CHANGELOG/$VER.md`, which STAGES a rename, then edits the H1
# in the WORKING TREE, then commits without `-a`. The rename lands; the H1 edit does not. The
# resulting `git status --short` reads `RM` — a staged Rename plus an UNSTAGED Modification — and
# the `M` is the half that gets missed. Self-verification with `head -1 <path>` then reads the
# working tree, where the fix IS present, and confirms a commit that does not contain it.
#
# So the check has to read the OBJECT. That is the whole point:
#     git show <ref>:<path> | head -1        NOT        head -1 <path>
#
# Usage: scripts/verify-changelog-h1.sh [<ref>] [-C <repo>]     (default ref HEAD, repo .)
# Exit 0 clean, 1 on any mismatch.

set -uo pipefail

ref="HEAD"
repo="."
npos=0
while [ $# -gt 0 ]; do
  case "$1" in
    -C) repo="$2"; shift 2 ;;
    *)  npos=$((npos+1))
        if [ "$npos" -eq 1 ]; then ref="$1"
        else echo "verify-changelog-h1: unexpected argument '$1'" >&2; exit 2
        fi
        shift ;;
  esac
done
g() { git -C "$repo" "$@"; }

if ! g rev-parse --verify -q "$ref" >/dev/null; then
  echo "verify-changelog-h1: ref '$ref' does not resolve in $repo" >&2
  exit 2
fi

rc=0
checked=0
legacy=0
while IFS= read -r f; do
  [ -n "$f" ] || continue
  cal=$(basename "$f" .md)
  # Read the H1 out of the OBJECT. A working-tree read is what this script exists to replace.
  h1=$(g show "$ref:$f" 2>/dev/null | head -1)
  checked=$((checked+1))
  # The DEFECT is narrow and mechanical: the H1 names a CalVer, and it is a DIFFERENT one from
  # the filename's. That is #332 exactly — an author-time stamp surviving a merge-time rename.
  #
  # Everything else is deliberately NOT a finding. A first draft of this script flagged any H1
  # that did not match `# <CalVer> — <title>`, which reported 35 hits on the marketplace repo's main where ONE
  # was real: 34 entries predate the convention, some carrying a bare `# <CalVer>` and some a
  # descriptive title. A check that cries wolf 34 times gets disabled by whoever inherits it —
  # the same reasoning that put --no-merges in verify-claimed-changes.sh.
  found=$(printf '%s' "$h1" | grep -oE '[0-9]{4}\.[0-9]{3}\.[0-9]{4}' | head -1 || true)
  if [ -n "$found" ] && [ "$found" != "$cal" ]; then
    echo "H1 NAMES A DIFFERENT CALVER — $f"
    echo "    filename: $cal"
    echo "    H1 says:  $found"
    echo "    line:     $h1"
    rc=1
  elif [ -z "$found" ]; then
    legacy=$((legacy+1))
  fi
done <<< "$(g ls-tree --name-only -r "$ref" -- CHANGELOG/ 2>/dev/null | grep -E 'CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$' || true)"

if [ "$checked" -eq 0 ]; then
  echo "verify-changelog-h1: no CalVer-named CHANGELOG entries under $ref in $repo (nothing to check)."
  exit 0
fi
[ "$rc" -eq 0 ] && echo "verify-changelog-h1: clean — $checked entries checked, no H1 names a CalVer other than its own ($legacy carry no CalVer in the H1 and predate the convention)."
exit $rc
