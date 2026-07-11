#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context. SOFT — never
# emits blocking JSON; a trivial-reply turn still completes normally.
#
# DOCTRINE: a SECOND-PASS COMPLIANCE REMINDER with an AUTONOMY anchor — it points
# at CLAUDE.md "Acceptance checklist" / "Post-Execution Policies" / "Hard Cutover
# by Default" and reminds that work continues AUTOMATICALLY until done. It carries
# pointer-shaped behavioral anchors (continue-if-work-remains, RCA-every-issue,
# re-audit, CHANGELOG entry) but restates no rule bodies — CLAUDE.md is the single
# source. Deterministic enforcement is in the PreToolUse gates. See
# /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
END-OF-TURN SECOND PASS (soft — does not block): before claiming done,
re-verify THIS turn for FULL CLAUDE.md compliance.
- Is ANY cutover, task, or TODO still unfinished? Then "should I continue?" is
  ALWAYS YES — keep going AUTOMATICALLY (Hard Cutover runs end-to-end through
  R10; disposable:true targets need no extra permission). Do NOT stop to ask
  whether to proceed.
- Tempted to stop because context is running out / "it needs a fresh session" /
  continuing "would leave a broken, unbuildable tree"? Those are FORBIDDEN excuses
  (CLAUDE.md "Hard Cutover by Default" — the Size/budget/context bullet; "would
  break the tree" is FABRICATED, zero evidence). Running low on context is NEVER a
  stop reason: COMPACT-and-continue, OR DELEGATE the unit to a fresh teammate /
  sub-agent (`Agent`/team/workflow) — that IS "a fresh session," delivered on
  demand. NEVER hand the user a "start a fresh session on task #N" note.
- Did EVERY failure / warning / anomaly this turn get a proper RCA and a real
  fix per CLAUDE.md (R1)? "flake" / "transient" / "out of scope" / "follow-up"
  are FORBIDDEN framings (R2) — fix it now, or open its own immediate-next cutover.
- Was the approved plan executed AS WRITTEN (no mid-execution narrow/widen/re-approach;
  plan = CONTRACT, only STOP-and-ask)? Were ALL transitional/legacy/deprecated/dual-mode
  paths deleted BEFORE the R10 acceptance run, so R10 hit the FINAL code? (CLAUDE.md "Hard
  Cutover by Default")
- Re-audit EVERY code/config change against R0-R10 + walk the "Acceptance
  checklist" (all three groups) + "Post-Execution Policies" box-by-box.
- CHANGELOG: did EACH repo you changed record its entry in its per-CalVer
  CHANGELOG/<YYYY.DDD.HHMM>.md (the version shared by the changelog file and the
  release tag)? A behavioral cutover with no CHANGELOG entry is NOT done.
STOP only when (a) all work is done AND verified, or (b) a GENUINE question blocks
you that you cannot resolve from the request, the code, the loaded skills, or
sensible defaults — then ask ONE specific, actionable question. Otherwise: KEEP
WORKING.
EOF
