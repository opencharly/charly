#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt. Stdout
# becomes a <system-reminder> at the start of Claude's next response.
#
# DOCTRINE: a SECOND-PASS COMPLIANCE REMINDER — it names the full R0-R10 + RDD +
# ADE roster as terse TRIGGERS (rule label + a few-word essence + a project-rulebook
# anchor) so every rule gets a "go verify THIS turn against it" nudge. It is a
# reminder, NOT a duplication: it never restates rule BODIES — the project rulebook is the
# single current source for the rule text, and that is where each trigger points.
# Deterministic enforcement lives in the PreToolUse gates (pre-commit-gate.sh,
# pre-push-gate.sh). See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
OPENCHARLY COMPLIANCE REMINDER — trigger a SECOND PASS: verify THIS turn
against each rule; authoritative text is the project rulebook (AGENTS.md / CLAUDE.md) (a reminder to GO CHECK,
not the rule itself):
- R0 SKILLS FIRST — load ALL matching skills before acting (Skill Dispatcher)
- RDD — prove HIGH-RISK claims on a `disposable: true` bed EARLY
- ADE — every candy ships `description:` + `plan:` with >=1 deterministic `check:`
- R1 RCA every failure/warning — never "flake"/"transient"
- R2 fix every cutover-surfaced issue now — never "out of scope"/"follow-up"
- R3 no duplication — one shared, generic abstraction
- R4 no ad-hoc workarounds — a sync primitive, not sleep/retry
- R5 hard cutover — delete old path + ALL stale refs + ALL transitional/dual-mode code (grep self-test)
- R6 check git status/stashes before destructive working-tree actions
- R7 unit tests != runtime — run the end-to-end bed gate
- R8 verify Containerfile sections + OCI labels post-build
- R9 deployed binary == source; runtime deps in package mgmt
- R10 verify on `disposable: true`; prove on a FRESH rebuild; tier == proof
- ONE PHASE through R10 (Hard Cutover): approved plan = an immutable CONTRACT (no mid-execution change); transitional/legacy/deprecated code gone BEFORE the R10 acceptance run (FINAL code only); run the R10 gate by change class
- AUTONOMY: run fully autonomously to done (only the defined STOP-and-ask exceptions). Size/difficulty/PRIORITY are never a reason not to do something. "Out of context / needs a fresh session / continuing would break the tree" are FORBIDDEN excuses ("break the tree" is fabricated) — compact-and-continue OR DELEGATE the unit to a fresh teammate/sub-agent (that IS a fresh session, on demand; the primitives' primary purpose)
Detail: the project rulebook R0-R10 / RDD / ADE / "Hard Cutover by Default" / "Agents, Workflows & Teams"; load skills per the Skill Dispatcher.
EOF
