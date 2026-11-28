#!/usr/bin/env bash
# check-pin-current.sh — prove the pin-current control can FAIL, before it is
# trusted to gate anything.
#
# WHY THIS EXISTS. `charly task pin-current` is a GATE. A gate that cannot fail is
# not a gate: if the currency verdict never turned red on a behind pin, or the
# wrapper's mode never reached the verb, the control would be a green light that
# proves nothing — and a green light nobody can trust is worse than no control at
# all, because it teaches every reader that the pin surface is watched. R7 asks for
# a test that FAILS without the change; this is that test for THIS repo's wiring.
#
# WHAT IT PROVES, on real scratch git repos, with the SHIPPED task wiring, and with
# NO neglect bound (the old bound is deleted — "behind" alone now FAILS):
#   1. BEHIND FAILS. A pin recorded at an ANCESTOR of the branch's served tip is
#      reported `behind`, the not-current count leads the verdict, the remedy
#      (`charly task ff`) is named, and the task exits non-zero.
#   2. AT THE TIP PASSES. The same fixture with the gitlink recorded at the served
#      tip exits 0 and reports `0 of 1 pin(s) NOT current`.
#   3. THE WIRING IS THE SHIPPED ONE. The `pins` and `pin-current` entities are
#      EXTRACTED from this repo's own charly.yml, so the fixture cannot drift from
#      what ships. The extracted `pin-current` must carry `MODE=currency` and must
#      NOT carry `STALE_AFTER`: a wrapper that regressed to the deleted bound fails
#      the proof before any arm runs.
#
# WHY A FIXTURE AND NOT THE REAL TREE. The real tree's pins are current by
# construction — that is the point of the control — so the red arm cannot be
# produced from it without rewriting published history. The fixture is scratch
# repos in a temp dir; nothing here touches a real pin. The gitlink is written by
# `git update-index` inside a scratch repo (how a pin is RECORDED), never by a
# hand-pin on a real tree.
#
# THE REMOTE IS BUILT WITH `git clone --bare`, never `git push` — the org forbids a
# direct push to `main`, and the clone-level pre-push hook blocks one even inside a
# temp fixture, so a proof script must not need a push to run.
#
# Usage: scripts/check-pin-current.sh [path-to-charly-binary]
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

# The extracted wrapper must be the SHIPPED one: currency mode, and no bound. This
# is judged on the WRAPPER alone (not the whole fixture): the `pins:` description
# also mentions `MODE=currency`, so a file-wide grep could pass over a regressed
# wrapper.
WRAPPER="$(extract_entity "pin-current:" "$MANIFEST")"
if ! printf '%s\n' "$WRAPPER" | grep -qF 'param: [MODE=currency]'; then
  echo "FAIL: the extracted pin-current wrapper does not carry param: [MODE=currency]" >&2
  exit 1
fi
if printf '%s\n' "$WRAPPER" | grep -q 'STALE_AFTER'; then
  echo "FAIL: the extracted pin-current wrapper still carries STALE_AFTER — the" >&2
  echo "      deleted neglect bound regressed into the shipped wiring" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# The fixture: a superproject with ONE leaf submodule declared `branch = main`.
# The leaf's upstream has two commits, and the bare remote (the SERVED tip) is
# cloned from it. The ONLY variable between the arms is the gitlink recorded:
# the ancestor (behind) or the tip (current).
# ---------------------------------------------------------------------------
(
  cd "$WORK" && git init -q -b main leafwork && cd leafwork
  echo v1 > f && git add -A && G commit -qm v1
  echo v2 > f && G commit -qam v2
  git clone -q --bare "$WORK/leafwork" "$WORK/leaf.git"
)
SERVED="$(git -C "$WORK/leafwork" rev-parse HEAD)"
RECORDED="$(git -C "$WORK/leafwork" rev-parse HEAD^)"

make_fixture() {
  # make_fixture <dir> <recorded-sha> — a scratch superproject whose ONLY pin is
  # `leaf`, recorded at <recorded-sha>, with the checkout materialized so the pin
  # is judgeable (`behind`/`cur`) rather than merely unjudgeable.
  local dir="$1" rec="$2"
  mkdir -p "$dir"
  (
    cd "$dir"
    git init -q -b main
    echo repo > r && git add -A && G commit -qm base
    printf '[submodule "leaf"]\n\tpath = leaf\n\turl = %s\n\tbranch = main\n' "$WORK/leaf.git" > .gitmodules
    git add .gitmodules && G commit -qm gm
    git update-index --add --cacheinfo "160000,$rec,leaf"
    G commit -qm "pin leaf"
    git -c protocol.file.allow=always submodule update --init -q leaf
  )
  cp "$WORK/charly.yml" "$dir/charly.yml"
}

# ---------------------------------------------------------------------------
# Arm BEHIND — the behaviour this change ships: a pin that is not the tip FAILS,
# with no age and no bound to argue about.
# ---------------------------------------------------------------------------
echo "== the control: a pin recorded at the ANCESTOR of the served tip (behind) must FAIL =="
make_fixture "$WORK/behind" "$RECORDED"
set +e
out="$(cd "$WORK/behind" && "$CH" task pin-current 2>&1)"
rc=$?
set -e
printf '%s\n' "$out" | sed 's/^/   | /'
if [ "$rc" -eq 0 ]; then
  echo "FAIL: the BEHIND arm — a pin behind the served tip must fail the control" >&2
  exit 1
fi
for needle in 'currency: 1 of 1 pin(s) NOT current' 'leaf: behind' 'charly task ff'; do
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    echo "FAIL: the BEHIND arm — the verdict must name '$needle'; got the output above" >&2
    exit 1
  fi
done
echo "   a behind pin (recorded $RECORDED, served $(printf '%.10s' "$SERVED")) was reported NOT current and the control exited $rc"

# ---------------------------------------------------------------------------
# Arm AT THE TIP — the same fixture, the only difference the recorded gitlink.
# ---------------------------------------------------------------------------
echo "== the control: the SAME leaf recorded at the SERVED TIP must PASS =="
make_fixture "$WORK/attip" "$SERVED"
set +e
out="$(cd "$WORK/attip" && "$CH" task pin-current 2>&1)"
rc=$?
set -e
printf '%s\n' "$out" | sed 's/^/   | /'
if [ "$rc" -ne 0 ]; then
  echo "FAIL: the AT-TIP arm — a pin at the tip its intent names must pass" >&2
  exit 1
fi
if ! printf '%s\n' "$out" | grep -qF 'currency: 0 of 1 pin(s) NOT current'; then
  echo "FAIL: the AT-TIP arm — the verdict must report '0 of 1 pin(s) NOT current'" >&2
  exit 1
fi
echo "   the leaf recorded at the served tip passed the same control"

echo "PIN-CURRENT-PROOF-OK"
