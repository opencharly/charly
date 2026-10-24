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
#   1. NEGLECT FAILS. A pin that is NOT CURRENT AND that its owner has not
#      advanced for longer than the bound is reported STALE and the task exits
#      non-zero — and the verdict names the pin, its age, and the bound applied.
#   2. CHURN PASSES. The SAME pin, the SAME distance from the served ref, the SAME
#      bound — advanced recently — passes. The two arms differ ONLY in the date of
#      the commit that recorded the pin, so the verdict flips with the AGE and with
#      nothing else. That is what makes the criterion "neglect", not "distance".
#   3. The bound that bites is the SHIPPED one (168h), asserted from the verdict
#      text — not from a constant this script could have drifted away from.
#
# NOT CURRENT IS NOT "BEHIND", and the proof has to say so. The criterion is "not
# current AND un-advanced", and "not current" is a SET of classes: a twin that is
# behind the ref it declares, a twin whose upstream advanced by a SQUASH-merge
# (whose served ref does not descend from the recorded pin, so the pin is
# `not-on-the-served-line`), and a pin with no checkout to judge at all
# (`differs`). The org merges by squash in several repos, so the second class is
# ordinary, not exotic, and the org's tree is heading for the third (a light tree
# materializes no submodule at all). A proof that only exercised `behind` would
# leave the criterion's own claim unproven for the classes that matter most —
# which is how a shipped control ends up described by a narrower sentence than the
# one it implements. So the proof runs SIX arms, in three pairs: the two above
# over a `behind` twin, the same two over a SQUASH-shaped twin whose served ref
# does not descend from the recorded pin, and the same two over a twin that is
# NOT MATERIALIZED, where the pin is not current for the third reason.
#
# The third pair also proves the control's verdict does not depend on the TREE
# SHAPE: on a tree that materializes nothing, a pin that is not current is still
# reported not current, and still goes red when it is neglected.
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
# Exit:  0 = every arm behaved as specified. 1 = a control defect.

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
# The fixture: a superproject with ONE pin whose recording commit carries a
# chosen date. The date is the ONLY variable.
#
# THREE twin shapes, because "not current" is a SET of classes:
#   subsrc    — the served ref DESCENDS from the recorded pin, so the pin is
#               `behind` (the ordinary churn shape).
#   squashsrc — the served ref does NOT descend from the recorded pin, which is
#               what a squash-merge upstream produces, so the pin is
#               `not-on-the-served-line`. The recorded commit is kept reachable
#               on a branch of its own, so the clone carries it and the ancestry
#               question is DECIDABLE (an object the clone lacks would make the
#               class unjudgeable rather than squash-shaped).
#   unmaterialized — the SAME subsrc twin with NO CHECKOUT, so there is nothing
#               to judge the ancestry from and the pin is `differs` (no checkout
#               to judge ancestry) — the third reason a pin is not current, and
#               the shape of a light tree.
# ---------------------------------------------------------------------------
(
  cd "$WORK" && git init -q -b main subsrc && cd subsrc
  echo v1 > f && git add -A && G commit -qm v1
  C1="$(git rev-parse HEAD)"
  echo v2 > f && G commit -qam v2
  C2="$(git rev-parse HEAD)"
  printf 'REC=%s\nSERVED=%s\n' "$C1" "$C2" > "$WORK/shas-behind"
)
(
  cd "$WORK" && git init -q -b main squashsrc && cd squashsrc
  echo v1 > f && git add -A && G commit -qm v1
  A="$(git rev-parse HEAD)"
  # A squash-merge: the served tip does not descend from the recorded head.
  git checkout -q --orphan squashed
  git rm -q -rf . >/dev/null 2>&1 || true
  echo v2 > f && git add -A && G commit -qm v2
  B="$(git rev-parse HEAD)"
  git checkout -q -B main "$B"
  git branch -f recorded "$A"
  printf 'REC=%s\nSERVED=%s\n' "$A" "$B" > "$WORK/shas-squash"
)

make_fixture() {
  # make_fixture <dir> <recording-date> <source-repo> <shas-file> <materialize: yes|no>
  local dir="$1" stamp="$2" src="$3" shas="$4" materialize="$5"
  mkdir -p "$dir"
  (
    cd "$dir"
    git init -q -b main
    echo repo > r && git add -A && G commit -qm base
    printf '[submodule "gap"]\n\tpath = gap\n\turl = %s\n\tbranch = main\n' "$src" > .gitmodules
    git add .gitmodules && G commit -qm gm
    . "$shas"
    # The pin records REC while the served ref is SERVED.
    git update-index --add --cacheinfo "160000,$REC,gap"
    GIT_AUTHOR_DATE="$stamp" GIT_COMMITTER_DATE="$stamp" G commit -qm "pin gap"
    # Materializing the checkout is what lets the pin be CLASSIFIED (behind /
    # not-on-the-served-line) rather than merely "differs (no checkout to judge
    # ancestry)". The third pair of arms leaves it unmaterialized on purpose.
    if [ "$materialize" = yes ]; then
      git -c protocol.file.allow=always submodule update --init -q gap
    fi
  )
  cp "$WORK/charly.yml" "$dir/charly.yml"
}

# must_fail <label> <dir> <stamp> <src> <shas> <materialize> <expected-class>
# must_pass <label> <dir> <stamp> <src> <shas> <materialize> <expected-class>
must_fail() {
  local label="$1" dir="$2" stamp="$3" src="$4" shas="$5" materialize="$6" class="$7" out
  make_fixture "$dir" "$stamp" "$src" "$shas" "$materialize"
  if out="$(cd "$dir" && "$CH" task pin-current 2>&1)"; then
    echo "FAIL: $label — a pin neglected since $stamp must fail the bound" >&2
    printf '%s\n' "$out" >&2
    exit 1
  fi
  printf '%s\n' "$out" | sed 's/^/   | /'
  for needle in 'STALE pin(s)' 'has not advanced it for' '(bound 168h)' 'gap' "$class"; do
    if ! printf '%s\n' "$out" | grep -qF "$needle"; then
      echo "FAIL: the verdict must name '$needle'; got:" >&2
      printf '%s\n' "$out" >&2
      exit 1
    fi
  done
  echo "   a $class pin, un-advanced since $stamp, was reported STALE"
}

must_pass() {
  local label="$1" dir="$2" stamp="$3" src="$4" shas="$5" materialize="$6" class="$7" out
  make_fixture "$dir" "$stamp" "$src" "$shas" "$materialize"
  if out="$(cd "$dir" && "$CH" task pin-current 2>&1)"; then
    :
  else
    echo "FAIL: $label — a pin advanced just now is churn, not neglect; the bound must not bite" >&2
    printf '%s\n' "$out" >&2
    exit 1
  fi
  printf '%s\n' "$out" | sed 's/^/   | /'
  # The green arm must be green BECAUSE the pin is not neglected, not because the
  # control cannot see that it is not current: the pin is not current in BOTH arms.
  if ! printf '%s\n' "$out" | grep -qF "$class"; then
    echo "FAIL: the recent arm's pin must still be reported '$class' — otherwise the two" >&2
    echo "       arms did not differ only by the date, and the proof is void" >&2
    printf '%s\n' "$out" >&2
    exit 1
  fi
  if printf '%s\n' "$out" | grep -qF 'STALE pin(s)'; then
    echo "FAIL: the recent arm must not be reported STALE; got:" >&2
    printf '%s\n' "$out" >&2
    exit 1
  fi
  echo "   the same $class pin, advanced $stamp, passed the same bound"
}

NEGLECTED="2020-01-01T00:00:00Z"
RECENT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# ---------------------------------------------------------------------------
# The arms. ONE table, and the counts the surfaces state are DERIVED from it:
# a shape added here cannot leave a stale "SIX arms" behind in a surface, and
# a surface that disagrees with the proof turns the check at the end of this
# script RED instead of waiting for a reader to notice.
#
#   slug | label | source repo | shas file | materialize | expected class | neglected headline
# ---------------------------------------------------------------------------
ARM_TABLE=(
  "behind|the behind twin|$WORK/subsrc|$WORK/shas-behind|yes|behind|a pin NEGLECTED past the bound must FAIL"
  "squash|the squash twin|$WORK/squashsrc|$WORK/shas-squash|yes|not-on-the-served-line|a SQUASH-shaped twin NEGLECTED past the bound must FAIL"
  "nockeckout|the unmaterialized twin|$WORK/subsrc|$WORK/shas-behind|no|differs|a pin with NO CHECKOUT to judge, NEGLECTED past the bound, must FAIL"
)
SHAPE_COUNT=${#ARM_TABLE[@]}
ARM_COUNT=$(( SHAPE_COUNT * 2 ))

for row in "${ARM_TABLE[@]}"; do
  IFS='|' read -r slug label src shas materialize expected headline <<<"$row"
  echo "== the control: $headline =="
  must_fail "$label" "$WORK/neglected-$slug" "$NEGLECTED" \
    "$src" "$shas" "$materialize" "$expected"

  echo "== the control: the SAME pin, the SAME distance, advanced RECENTLY, must PASS =="
  must_pass "$label" "$WORK/recent-$slug" "$RECENT" \
    "$src" "$shas" "$materialize" "$expected"
done

# ---------------------------------------------------------------------------
# The surfaces must agree on the control's scope.
#
# The numbers the prose states — "SIX arms, in three pairs over three twin
# shapes" — are checked here against the numbers this proof actually ran, which
# are derived from the arm table above. This is the control that was MISSING
# when this change's own surfaces disagreed with each other: nothing ever read
# them against each other, and the drift was invisible until a reader did.
#
# The claim is keyed on its FORM — the arms/pairs pair, and the twin shapes — so
# a sentence that counts something else is not a hit.
# ---------------------------------------------------------------------------
SURFACES=(
  "$ROOT/AGENTS.md"
  "$ROOT/charly.yml"
  "$ROOT/scripts/check-pin-currency.sh"
  "$ROOT/.github/workflows/pin-currency.yml"
)
NUMWORDS='([0-9]+|one|two|three|four|five|six|seven|eight)'

word_num() { # word_num <a number word, or digits> -> the number, or 0
  case "$1" in
    one|1) echo 1 ;; two|2) echo 2 ;; three|3) echo 3 ;; four|4) echo 4 ;;
    five|5) echo 5 ;; six|6) echo 6 ;; seven|7) echo 7 ;; eight|8) echo 8 ;;
    *) echo 0 ;;
  esac
}

echo "== the surfaces must agree on the control's scope =="
checked=0
for f in "${SURFACES[@]}"; do
  [ -f "$f" ] || continue
  # The surface is read JOINED: a claim may wrap across lines, and a claim
  # that wraps is still a claim.
  joined="$(tr '\n' ' ' < "$f")"
  while read -r arms pairs; do
    [ -n "$arms" ] || continue
    checked=$((checked + 1))
    if [ "$(word_num "$arms")" != "$ARM_COUNT" ] || [ "$(word_num "$pairs")" != "$SHAPE_COUNT" ]; then
      echo "FAIL: ${f#"$ROOT"/} states '$arms arms, in $pairs pairs', but this proof runs" \
           "$ARM_COUNT arms, in $SHAPE_COUNT pairs" >&2
      exit 1
    fi
  done < <(printf '%s' "$joined" \
           | grep -oiE "$NUMWORDS[[:space:]]+arms,[[:space:]]+in[[:space:]]+$NUMWORDS[[:space:]]+pairs" \
           | tr 'A-Z' 'a-z' | awk '{print $1, $4}' || true)
  while read -r shapes; do
    [ -n "$shapes" ] || continue
    checked=$((checked + 1))
    if [ "$(word_num "$shapes")" != "$SHAPE_COUNT" ]; then
      echo "FAIL: ${f#"$ROOT"/} states '$shapes twin shapes', but this proof runs" \
           "$SHAPE_COUNT twin shapes" >&2
      exit 1
    fi
  done < <(printf '%s' "$joined" \
           | grep -oiE "$NUMWORDS[[:space:]]+twin[[:space:]]+shapes" \
           | tr 'A-Z' 'a-z' | awk '{print $1}' || true)
done
echo "   $checked statement(s) of the control's scope, and every one of them agrees"

echo "PIN-CURRENCY-PROOF-OK"
