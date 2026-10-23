#!/usr/bin/env bash
# check-pin-currency.sh — prove the pin-currency control can FAIL, before it is
# trusted to gate anything.
#
# WHY THIS EXISTS. `charly task pin-current` is a GATE. A gate that cannot fail is
# not a gate: if the bound never bites, or the wrapper's params never reach the verb,
# the control is a green light that proves nothing — and a green light nobody can
# trust is worse than no control at all, because it teaches every reader that the
# pin surface is watched. The org's own rule (R7) says a test that cannot fail is
# invalid; the same is true of a control.
#
# WHAT IT PROVES, on a real git repo, with the SHIPPED task wiring:
#   1. NEGLECT FAILS. A pin that is behind AND that its owner has not advanced for
#      longer than the bound is reported STALE and the task exits non-zero — and the
#      verdict names the pin, its age, and the bound that was applied.
#   2. CHURN PASSES. The SAME pin, the SAME distance from the served ref, the SAME
#      bound — advanced recently — passes. The two arms differ ONLY in the date of
#      the commit that recorded the pin, so the verdict flips with the AGE and with
#      nothing else. That is what makes the criterion "neglect", not "distance".
#   3. The bound that bites is the SHIPPED one (168h), asserted from the verdict
#      text — not from a constant this script could have drifted away from.
#
# THE TASKS UNDER TEST ARE EXTRACTED from this repo's own charly.yml, so the fixture
# exercises the shipped wiring and cannot drift from it: change the wrapper or the
# bound in charly.yml and the fixture changes with it. A copy of the wrapper in this
# script would be a second implementation of the control (R2), and a second
# implementation is exactly how a control stops being the one that ships.
#
# WHY A FIXTURE AND NOT THE REAL TREE. The real tree's pins are healthy by
# construction — that is the whole point of the control — so the red arm cannot be
# produced from it without rewriting published history. The fixture is a scratch
# repo in a temp dir. Nothing here touches a real pin, and nothing here is a
# hand-pin: the fixture's gitlink is written by `git update-index` inside a scratch
# repo, which is how a pin is recorded, not how a pin is advanced.
#
# Usage: scripts/check-pin-currency.sh [path-to-charly-binary]
# Exit:  0 = both arms behaved as specified. 1 = a control defect.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# The binary under test, in the order that keeps it WORKTREE-LOCAL (R9): an explicit
# argument, then the binary the caller stamped (CHARLY_BIN — a bed's own binary,
# which is the right one inside a bed), then this worktree's built binary.
#
# The path is resolved to an ABSOLUTE one, because every arm below runs the binary
# from inside the fixture directory — a relative path would be resolved against the
# fixture, not against the repo.
if [ "$#" -ge 1 ] && [ -n "$1" ]; then
  CH="$1"
elif [ -n "${CHARLY_BIN:-}" ] && [ -x "${CHARLY_BIN}" ]; then
  CH="${CHARLY_BIN}"
else
  CH="$ROOT/bin/charly"
fi
CH="$(cd "$(dirname "$CH")" && pwd)/$(basename "$CH")"
if [ ! -x "$CH" ]; then
  echo "FAIL: no charly binary at $CH — build it with scripts/bootstrap-charly.sh" >&2
  exit 1
fi

# The fixture must not read the operator's git identity, and the commits it makes
# must not be signed or filtered by a global config.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
G() { git -c user.name=t -c user.email=t@t "$@"; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ---------------------------------------------------------------------------
# The task wiring under test, EXTRACTED from this repo's charly.yml.
# ---------------------------------------------------------------------------
extract_entity() {
  # extract_entity <key> <file> — the top-level entity <key> and its body, up to the
  # next top-level key. The comment lines that trail an entity belong to the NEXT
  # entity, so they are dropped: the fixture must carry the wiring, not the prose.
  awk -v key="$1" '
    !inside && $0 == key { inside = 1; print; next }
    inside && $0 ~ /^[^[:space:]#]/ { exit }
    inside { print }
  ' "$2" | awk '
    { line[NR] = $0 }
    END {
      last = 0
      for (i = 1; i <= NR; i++) if (line[i] !~ /^[[:space:]]*#/ && line[i] !~ /^[[:space:]]*$/) last = i
      for (i = 1; i <= last; i++) print line[i]
    }'
}

MANIFEST="$ROOT/charly.yml"
for key in pins pin-current; do
  if ! extract_entity "$key:" "$MANIFEST" | grep -q "^$key:"; then
    echo "FAIL: the manifest has no top-level '$key:' entity — the control's wiring moved" >&2
    exit 1
  fi
done
{
  echo "# The pin-currency control's own wiring, extracted from this repo's charly.yml."
  extract_entity "pins:" "$MANIFEST"
  extract_entity "pin-current:" "$MANIFEST"
} > "$WORK/charly.yml"

# The extracted wrapper must still carry the shipped bound; if it does not, the
# control this script proves is not the control that ships.
if ! grep -q 'STALE_AFTER=168h' "$WORK/charly.yml"; then
  echo "FAIL: the extracted pin-current wrapper does not carry STALE_AFTER=168h" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# The fixture: a superproject with ONE pin, behind the served ref, whose
# recording commit carries a chosen date. The date is the ONLY variable.
# ---------------------------------------------------------------------------
(
  cd "$WORK" && git init -q -b main subsrc && cd subsrc
  echo v1 > f && git add -A && G commit -qm v1
  C1="$(git rev-parse HEAD)"
  echo v2 > f && G commit -qam v2
  printf 'C1=%s\n' "$C1" > "$WORK/shas"
)

make_fixture() {
  # make_fixture <dir> <recording-date>
  local dir="$1" stamp="$2"
  mkdir -p "$dir"
  (
    cd "$dir"
    git init -q -b main
    echo repo > r && git add -A && G commit -qm base
    printf '[submodule "gap"]\n\tpath = gap\n\turl = %s\n\tbranch = main\n' "$WORK/subsrc" > .gitmodules
    git add .gitmodules && G commit -qm gm
    . "$WORK/shas"
    # The pin records C1 while the served ref is C2 — the pin is BEHIND.
    git update-index --add --cacheinfo "160000,$C1,gap"
    GIT_AUTHOR_DATE="$stamp" GIT_COMMITTER_DATE="$stamp" G commit -qm "pin gap"
    # Materialize the checkout, so the pin is judged against the served ref
    # (behind) rather than merely "differs (no checkout to judge ancestry)".
    git -c protocol.file.allow=always submodule update --init -q gap
  )
  cp "$WORK/charly.yml" "$dir/charly.yml"
}

NEGLECTED="2020-01-01T00:00:00Z"
RECENT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "== the control: a pin NEGLECTED past the bound must FAIL =="
make_fixture "$WORK/neglected" "$NEGLECTED"
if neg_out="$(cd "$WORK/neglected" && "$CH" task pin-current 2>&1)"; then
  echo "FAIL: a pin neglected since $NEGLECTED must fail the bound" >&2
  printf '%s\n' "$neg_out" >&2
  exit 1
fi
printf '%s\n' "$neg_out" | sed 's/^/   | /'
for needle in 'STALE pin(s)' 'has not advanced it for' '(bound 168h)'; do
  if ! printf '%s\n' "$neg_out" | grep -qF "$needle"; then
    echo "FAIL: the verdict must name '$needle'; got:" >&2
    printf '%s\n' "$neg_out" >&2
    exit 1
  fi
done
if ! printf '%s\n' "$neg_out" | grep -qF 'gap'; then
  echo "FAIL: the verdict must name the neglected pin; got:" >&2
  printf '%s\n' "$neg_out" >&2
  exit 1
fi
echo "   a pin behind its served ref and un-advanced since $NEGLECTED was reported STALE"

echo "== the control: the SAME pin, the SAME distance, advanced RECENTLY, must PASS =="
make_fixture "$WORK/recent" "$RECENT"
if recent_out="$(cd "$WORK/recent" && "$CH" task pin-current 2>&1)"; then
  :
else
  echo "FAIL: a pin advanced just now is churn, not neglect — the bound must not bite" >&2
  printf '%s\n' "$recent_out" >&2
  exit 1
fi
printf '%s\n' "$recent_out" | sed 's/^/   | /'
# The green arm must be green BECAUSE the pin is current, not because the control
# cannot see that it is behind: the pin is behind in BOTH arms.
if ! printf '%s\n' "$recent_out" | grep -qF 'behind'; then
  echo "FAIL: the recent arm's pin must still be reported behind — otherwise the two" >&2
  echo "       arms did not differ only by the date, and the proof is void" >&2
  printf '%s\n' "$recent_out" >&2
  exit 1
fi
if printf '%s\n' "$recent_out" | grep -qF 'STALE pin(s)'; then
  echo "FAIL: the recent arm must not be reported STALE; got:" >&2
  printf '%s\n' "$recent_out" >&2
  exit 1
fi
echo "   the same pin at the same distance, advanced $RECENT, passed the same bound"

echo "PIN-CURRENCY-PROOF-OK"
