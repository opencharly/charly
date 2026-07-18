#!/usr/bin/env bash
# Project-level agent-team lifecycle hook. Wired to TaskCreated, TaskCompleted,
# and TeammateIdle in settings.json. Stdout becomes a <system-reminder> for the
# team lead / teammate. SOFT — always exits 0, so it NEVER blocks task creation,
# task completion, or a teammate going idle.
#
# DOCTRINE: a POINTER reminder — the authoritative team model (bed-scoped
# ownership, persistent bed owners, the commit gate) lives in the project rulebook +
# /charly-internals:agents; this hook points at it with terse behavioral
# anchors, restating no rule bodies. See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
OPENCHARLY TEAM REMINDER (pointers — the model lives in /charly-internals:agents):
- Bed-scoped: own a DISJOINT kind:check bed (distinct names AND host ports); a
  PERSISTENT owner runs every full `charly check run <bed>` as a background
  task (/charly-internals:agents "Bed-scoped parallel real-deployment testing").
- The binding rule — disposable-only, commit gated on the pasted final-code
  run, no scope-shrinking flags (/charly-internals:agents "The binding rule").
- The LEAD owns the single atomic commit; teammates never commit or push.
EOF
