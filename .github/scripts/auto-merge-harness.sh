#!/usr/bin/env bash
# RDD harness for the auto-merge run block (.github/scripts/auto-merge-runblock.sh).
# Runs the EXACT (de-templated) run block against a stub `gh` + a real local git repo, then
# asserts on observed behavior. Each scenario is a function; the harness runs
# them all and reports PASS/FAIL per scenario. This is the check-coverage for
# .github/workflows/auto-merge.yml — the engine that merges + tags every PR.
set -u

RUNBLOCK="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/auto-merge-runblock.sh"
WORK=/tmp/amfix-rdd4-work
PASS=0; FAIL=0
REPO="opencharly/fedora"
PR=20
HEAD_SHA="abc123"
HEAD_BRANCH="feat/x"
BASE_REF="main"

# ---- fresh git repo per scenario: base + head + bare remote.
#      Pass "noplaceholder" to skip the CHANGELOG placeholder (for scenarios
#      that test the merge/tag/wait/verify logic without the finalization path).
setup_git() {
  local noplaceholder="${1:-}"
  rm -rf "$WORK"; mkdir -p "$WORK"
  git init -q -b main "$WORK/remote.git" --bare
  git init -q -b main "$WORK/base"
  git -C "$WORK/base" config user.email t@t; git -C "$WORK/base" config user.name t
  echo "base" > "$WORK/base/README.md"
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm base
  git -C "$WORK/base" remote add origin "$WORK/remote.git"
  git -C "$WORK/base" push -q origin main
  # head branch, optionally with a CHANGELOG placeholder
  git -C "$WORK/base" checkout -qb feat/x
  if [ -z "$noplaceholder" ]; then
    mkdir -p "$WORK/base/CHANGELOG"
    printf '# 2026.232.0001 — placeholder\n\nbody\n' > "$WORK/base/CHANGELOG/2026.232.0001.md"
  fi
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm "feat: add changelog placeholder"
  git -C "$WORK/base" push -q origin feat/x
  # the run block executes in a fresh clone of the remote
  git clone -q "$WORK/remote.git" "$WORK/run"
  git -C "$WORK/run" config user.email t@t; git -C "$WORK/run" config user.name t
  git -C "$WORK/run" remote set-url origin "$WORK/remote.git"
}

# ---- the stub gh, exported so the run-block subshell sees it ----
gh() {
  local verb="$1"; shift
  local args="$*"
  echo "gh $verb $args" >> "$GHLOG"
  case "$verb $args" in
    *"/commits/$HEAD_SHA/pulls"*) echo '20' ;;
    *"/pulls/$PR"*".head.repo.full_name"*) echo "$HEAD_REPO" ;;
    *"/pulls/$PR"*".head.ref"*) echo "$HEAD_BRANCH" ;;
    *"/pulls/$PR"*".head.sha"*) echo "$HEAD_SHA" ;;
    *"/pulls/$PR"*".base.ref"*) echo "$BASE_REF" ;;
    *"/pulls/$PR"*".merge_commit_sha"*) if [ "$MERGED" = "true" ]; then echo "deadbeef"; else echo ""; fi ;;
    *"/pulls/$PR"*".state"*) echo "$STATE" ;;
    *"/commits/deadbeef"*) echo "${COMMIT_DATE:-2026-08-19T12:34:56Z}" ;;
    *"/actions/runs?head_sha="*)
      echo "VALIDATOR_RUN_QUERY $args" >> "$GHLOG"
      if [ "$VALIDATOR_RUN_ID" = "none" ]; then echo ""; else echo "$VALIDATOR_RUN_ID"; fi ;;
    *"/actions/runs/"*"/rerun"*)
      echo "VALIDATOR_RERUN $args" >> "$GHLOG"
      VALIDATOR_CHECKRUN="present" ;;
    *"/actions/runs/"*)
      echo "VALIDATOR_CONCLUSION_QUERY $args" >> "$GHLOG"
      echo "$VALIDATOR_CONCLUSION" ;;
    *"/commits/"*"/check-runs"*)
      case "$args" in
        *"select(.name == \"charly/pr-validator\")] | length"*)
          if [ "$VALIDATOR_CHECKRUN" = "present" ]; then echo '1'; else echo '0'; fi ;;
        *"@tsv"*)
          # Faithful A1 model: the re-run validator re-reviews the FINALIZED
          # head against the PR body. A body that still names the author's
          # placeholder (no auto-finalization marker) reads as a
          # description↔code mismatch and the re-run concludes failure — the
          # live-block observed on the finalized head. The run block's
          # body-append (gh pr edit) adds the marker, which resolves it.
          #
          # A1 applies only to a NEW head (the finalized sha differs from the
          # author's original $HEAD_SHA, which was already validator-green —
          # that green status is what triggered this run); the initial
          # verification must NOT be subject to it. The body-append's success
          # must be read from $GHLOG, never a shell variable: every `gh` call
          # in the run block sits in a pipeline, so an assignment inside the
          # stub dies in a subshell and the parent never sees it.
          ts_head=$(printf '%s' "$args" | grep -oE '/commits/[0-9a-f]+/' | head -1 | sed 's#^/commits/##; s#/$##')
          if [ "${A1_BLOCK:-false}" = "true" ] \
             && [ -n "$ts_head" ] && [ "$ts_head" != "$HEAD_SHA" ] \
             && ! grep -q 'BODY_EDIT_OK' "$GHLOG"; then
            echo -e "charly/pr-validator\tfailure"
          else
            echo -e "charly/pr-validator\t$CHECK_CONCLUSION"
          fi ;;
        *)
          POLL=$(grep -c '/check-runs' "$GHLOG")
          if [ "$POLL" -le "$CHECK_PENDING" ]; then echo '1'; else echo '0'; fi ;;
      esac ;;
    *"/commits/"*"/status"*)
      POLL=$(grep -c '/check-runs' "$GHLOG")
      if [ "$POLL" -le "$CHECK_PENDING" ]; then
        case "$args" in
          *"select(.state == \"pending\")] | length"*) echo '1' ;;
          *) echo 'pending' ;;
        esac
      else
        case "$args" in
          *"select(.state == \"pending\")] | length"*) echo '0' ;;
          *".statuses | length"*) echo "$STATUS_COUNT" ;;
          *) echo "$STATUS_STATE" ;;
        esac
      fi ;;
    *"/git/refs"*)
      # The fixed ensure_tag no longer queries tag existence via the API (the
      # real gh prints the 404 body to stdout, so the old guard always read it
      # as present and silently skipped every mint — the PR #27 live defect).
      # Existence is a LOCAL git ref check; this POST is the actual mint. A
      # POST failure models a concurrent run's 422 ("Reference already exists").
      if [ "${POST_FAIL:-0}" = "1" ]; then
        echo "POST_FAILED $args" >> "$GHLOG"
        echo "mint failed" >&2; return 1
      fi
      echo "CREATED_TAG $(printf '%s' "$args" | grep -oE 'refs/tags/[^ "]+')" >> "$GHLOG"
      echo '{}' ;;
    "pr view"*"--json body"*)
      # reached on the finalize path (the marker check + the body-append
      # read); the A1 model keys off the head sha, not this variable
      printf '%s' "$PR_BODY" ;;
    "pr edit"*"--body-file"*)
      echo "BODY_EDIT $args" >> "$GHLOG"
      local newbody
      newbody=$(cat)
      if [ "${BODY_EDIT_FAIL:-0}" = "1" ]; then
        echo "BODY_EDIT_FAILED" >> "$GHLOG"
        echo "edit failed" >&2; return 1
      fi
      PR_BODY="$newbody"
      echo "BODY_EDIT_OK" >> "$GHLOG" ;;
    "pr"*)
      echo "MERGE_CALL $verb $args" >> "$GHLOG"
      if [ "$MERGE_RESULT" = "fail" ]; then echo "merge failed" >&2; return 1; fi
      MERGED="true"; STATE="closed" ;;
    *) echo "UNHANDLED: $args" >> "$GHLOG" ;;
  esac
}
export -f gh

# run the exact run block with the stub gh in scope (subshell, sleep stubbed)
run_block() {
  GHLOG="$WORK/gh.log"; : > "$GHLOG"
  export GHLOG HEAD_SHA REPO HEAD_REPO HEAD_BRANCH BASE_REF PR
  STATUS_COUNT="${STATUS_COUNT:-1}"
  export STATE MERGED MS CHECK_PENDING CHECK_CONCLUSION STATUS_STATE STATUS_COUNT MERGE_RESULT TAG_EXISTS POST_FAIL
  export VALIDATOR_RUN_ID VALIDATOR_CONCLUSION VALIDATOR_CHECKRUN
  export PR_BODY A1_BLOCK BODY_EDIT_FAIL
  PR_BODY="${PR_BODY:-docs change — a single new CHANGELOG/2026.232.0001.md markdown}"
  export GH_TOKEN=x GITHUB_REPOSITORY="$REPO"
  export COMMIT_DATE
  POLL=0; export POLL
  # the run block's git ops (fetch/checkout/mv/commit/push) must run in the
  # run clone; the cd is in a subshell so the harness CWD is not left inside
  # a directory the next setup_git will rm -rf
  ( cd "$WORK/run" && bash -c 'sleep() { :; }; source "$1"' _ "$RUNBLOCK" ) > "$WORK/out.log" 2>&1
  local rc=$?
  echo "rc=$rc" >> "$WORK/out.log"
  return $rc
}

assert() { # name, condition-string
  local name="$1"; shift
  if eval "$1"; then echo "PASS: $name"; PASS=$((PASS+1)); else echo "FAIL: $name"; FAIL=$((FAIL+1)); fi
}

# ============================================================
# S1: all green, open PR → enable auto-merge, wait, tag
# ============================================================
s1() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S1 exit 0" "[ $rc -eq 0 ]"
  assert "S1 merge --auto called" "grep -q 'MERGE_CALL pr merge 20 --squash --auto --delete-branch=false' $WORK/gh.log"
  assert "S1 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
  assert "S1 all-green message" "grep -q 'All status checks green' $WORK/out.log"
}

# ============================================================
# S2: pending checks then green → waits, then merges
# ============================================================
s2() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=2; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S2 exit 0" "[ $rc -eq 0 ]"
  assert "S2 waited (pending logged)" "grep -q 'still pending' $WORK/out.log"
  assert "S2 merged after wait" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
}

# ============================================================
# S3: failed check → abort, exit 1, NO merge
# ============================================================
s3() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=failure; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S3 exit 1" "[ $rc -eq 1 ]"
  assert "S3 no merge" "! grep -q 'MERGE_CALL' $WORK/gh.log"
  assert "S3 failure reported" "grep -q 'Not all checks pass' $WORK/out.log"
}

# ============================================================
# S3b: combined status state = error → abort, exit 1, NO merge
# ============================================================
s3b() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=error; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S3b exit 1" "[ $rc -eq 1 ]"
  assert "S3b no merge" "! grep -q 'MERGE_CALL' $WORK/gh.log"
  assert "S3b error state reported" "grep -q 'commit-status contexts (state=error)' $WORK/out.log"
}

# ============================================================
# S4: already-closed PR → idempotent tag, exit 0
# ============================================================
s4() {
  setup_git noplaceholder
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S4 exit 0" "[ $rc -eq 0 ]"
  assert "S4 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
  assert "S4 already-closed message" "grep -q 'already closed' $WORK/out.log"
}

# ============================================================
# S4b: already-closed + tag ALREADY EXISTS (a real local git ref — the fixed
#      ensure_tag checks the checkout's refs/tags, not the API) → reported,
#      not re-created.
# ============================================================
s4b() {
  setup_git noplaceholder
  # the run clone must actually carry the tag for the local-ref check to fire;
  # MS=deadbeef's stub committer date 2026-08-19T12:34:56Z → VER=v2026.231.1234
  git -C "$WORK/run" tag v2026.231.1234 HEAD
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=true; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S4b exit 0" "[ $rc -eq 0 ]"
  assert "S4b no tag POST" "! grep -q 'CREATED_TAG' $WORK/gh.log"
  assert "S4b already-exists message" "grep -q 'Tag v.* already exists' $WORK/out.log"
}

# ============================================================
# S5: same-repo CHANGELOG finalize — placeholder renamed to merge stamp,
#     H1 rewritten, pushed, validator check-run appears (normal dispatch),
#     revalidated, then merged
# ============================================================
s5() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=in_progress; VALIDATOR_CHECKRUN=present
  run_block; local rc=$?
  assert "S5 exit 0" "[ $rc -eq 0 ]"
  assert "S5 finalize message" "grep -q 'Finalizing CHANGELOG/2026.232.0001.md' $WORK/out.log"
  assert "S5 merged after finalize" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  # the remote head branch must now carry the renamed file with the new H1
  local newfile=$(git -C "$WORK/remote.git" ls-tree -r --name-only feat/x | grep -E '^CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$' | head -1)
  assert "S5 placeholder gone from head" "[ \"$newfile\" != 'CHANGELOG/2026.232.0001.md' ]"
  assert "S5 new file present" "[ -n \"$newfile\" ]"
  local h1=$(git -C "$WORK/remote.git" show "feat/x:$newfile" | head -1)
  local stamp=$(basename "$newfile" .md)
  assert "S5 H1 rewritten to stamp" "echo \"$h1\" | grep -qE \"^# $stamp\""
}

# ============================================================
# S6: A-status narrowing — a MODIFIED (M-status) CHANGELOG file in the PR
#     diff is NOT renamed.
# ============================================================
s6() {
  rm -rf "$WORK"; mkdir -p "$WORK"
  git init -q -b main "$WORK/remote.git" --bare
  git init -q -b main "$WORK/base"
  git -C "$WORK/base" config user.email t@t; git -C "$WORK/base" config user.name t
  echo "base" > "$WORK/base/README.md"
  mkdir -p "$WORK/base/CHANGELOG"
  printf '# 2026.232.0001 — placeholder\n\nbody\n' > "$WORK/base/CHANGELOG/2026.232.0001.md"
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm base
  git -C "$WORK/base" remote add origin "$WORK/remote.git"
  git -C "$WORK/base" push -q origin main
  # head branch MODIFIES the pre-existing placeholder (M-status in the diff)
  git -C "$WORK/base" checkout -qb feat/x
  printf '# 2026.232.0001 — placeholder\n\nbody modified\n' > "$WORK/base/CHANGELOG/2026.232.0001.md"
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm "docs: modify changelog placeholder"
  git -C "$WORK/base" push -q origin feat/x
  git clone -q "$WORK/remote.git" "$WORK/run"
  git -C "$WORK/run" config user.email t@t; git -C "$WORK/run" config user.name t
  git -C "$WORK/run" remote set-url origin "$WORK/remote.git"
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S6 exit 0" "[ $rc -eq 0 ]"
  assert "S6 no finalize (M-status excluded)" "grep -q 'No CHANGELOG placeholder in the PR diff' $WORK/out.log"
  assert "S6 no rename commit" "! grep -q 'Finalizing' $WORK/out.log"
  assert "S6 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  # the head branch must still carry the modified file unchanged
  local still=$(git -C "$WORK/remote.git" ls-tree -r --name-only feat/x | grep -cE '^CHANGELOG/2026\.232\.0001\.md$')
  assert "S6 modified file intact" "[ \"$still\" = '1' ]"
}

# ============================================================
# S7: placeholder already at the merge stamp → skip finalize, merge
# ============================================================
s7() {
  setup_git
  # rename the placeholder to the current merge stamp so basename == stamp
  local stamp=$(date -u +%Y.%j.%H%M)
  git -C "$WORK/base" mv CHANGELOG/2026.232.0001.md "CHANGELOG/$stamp.md"
  git -C "$WORK/base" commit -qm "docs: placeholder at stamp"
  git -C "$WORK/base" push -q origin feat/x
  git -C "$WORK/run" fetch -q origin
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S7 exit 0" "[ $rc -eq 0 ]"
  assert "S7 already-at-stamp message" "grep -q 'already at the merge stamp' $WORK/out.log"
  assert "S7 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
}

# ============================================================
# S8: auto-merge enable FAILS → run fails loudly, NO direct-squash
#     fallback
# ============================================================
s8() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=fail; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S8 exit 1" "[ $rc -eq 1 ]"
  assert "S8 no direct-squash fallback" "! grep -q 'MERGE_CALL pr merge 20 --squash --delete-branch=false' $WORK/gh.log"
  assert "S8 only one merge attempt" "[ $(grep -c 'MERGE_CALL' $WORK/gh.log) -eq 1 ]"
}

# ============================================================
# S9: fork PR → skip finalize, merge (head repo != REPO)
# ============================================================
s9() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="someone/fedora"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S9 exit 0" "[ $rc -eq 0 ]"
  assert "S9 fork skip message" "grep -q 'Fork PR' $WORK/out.log"
  assert "S9 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
}

# ============================================================
# S10: NO commit statuses at all — combined state is the empty-default
#      "pending" (statuses=[]). verify_all_green() must ACCEPT this and merge.
# ============================================================
s10() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=pending; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S10 exit 0" "[ $rc -eq 0 ]"
  assert "S10 empty-default pending accepted" "grep -q 'All status checks green' $WORK/out.log"
  assert "S10 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
}

# ============================================================
# S10b: non-empty statuses with combined state pending (a real pending that
#       wait_all_settled failed to drain) → still rejected, no merge
# ============================================================
s10b() {
  setup_git noplaceholder
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=pending; STATUS_COUNT=1; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S10b exit 1" "[ $rc -eq 1 ]"
  assert "S10b no merge" "! grep -q 'MERGE_CALL' $WORK/gh.log"
  assert "S10b pending state reported" "grep -q 'commit-status contexts (state=pending)' $WORK/out.log"
}

# ============================================================
# S11: finalization → validator run ends action_required (GITHUB_TOKEN
#      dispatch) → re-run via API → check-run appears → revalidate → merge
# ============================================================
s11() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=action_required; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S11 exit 0" "[ $rc -eq 0 ]"
  assert "S11 finalize message" "grep -q 'Finalizing CHANGELOG/2026.232.0001.md' $WORK/out.log"
  assert "S11 action_required reported" "grep -q 'ended action_required' $WORK/out.log"
  assert "S11 validator re-run called" "grep -q 'VALIDATOR_RERUN' $WORK/gh.log"
  assert "S11 merged after revalidation" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S11 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
}

# ============================================================
# S12: idempotent finalization — head history already contains a
#      finalization commit → skip the rename, reuse the changelog stamp for
#      the tag, merge
# ============================================================
s12() {
  setup_git
  # simulate a previous auto-merge finalization: rename the placeholder to a
  # merge stamp, add the finalization commit to the head history, AND write the
  # `auto-merge-finalized` marker the corrected workflow's state-based
  # idempotency check reads (the marker is the state, not the commit subject)
  git -C "$WORK/base" mv CHANGELOG/2026.232.0001.md CHANGELOG/2026.232.1304.md
  sed -i "1s/^# [0-9]\{4\}\.[0-9]\{3\}\.[0-9]\{4\}/# 2026.232.1304/" "$WORK/base/CHANGELOG/2026.232.1304.md"
  sed -i "1a\\<!-- auto-merge-finalized: 2026.232.1304 -->" "$WORK/base/CHANGELOG/2026.232.1304.md"
  git -C "$WORK/base" add -A
  git -C "$WORK/base" -c user.name='opencharly auto-merge' -c user.email='actions@github.com' \
    commit -q -m "docs: finalize CHANGELOG placeholder to 2026.232.1304 (merge-time CalVer)"
  git -C "$WORK/base" push -q origin feat/x
  git -C "$WORK/run" fetch -q origin
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S12 exit 0" "[ $rc -eq 0 ]"
  assert "S12 skip finalization message" "grep -q 'already finalized — skipping finalization' $WORK/out.log"
  assert "S12 no rename commit" "! grep -q 'Finalizing' $WORK/out.log"
  assert "S12 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S12 tag uses changelog stamp" "grep -q 'CREATED_TAG refs/tags/v2026.232.1304' $WORK/gh.log"
}

# ============================================================
# S13: finalization → validator run dispatches normally (in_progress, no
#      action_required) → no re-run → check-run appears → revalidate → merge
# ============================================================
s13() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=in_progress; VALIDATOR_CHECKRUN=present
  run_block; local rc=$?
  assert "S13 exit 0" "[ $rc -eq 0 ]"
  assert "S13 no re-run (normal dispatch)" "! grep -q 'VALIDATOR_RERUN' $WORK/gh.log"
  assert "S13 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S13 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
}

# ============================================================
# S14: finalization → no validator run appears on the new head → wait_for_validator
#      fails loudly, exit 1, NO merge
# ============================================================
s14() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S14 exit 1" "[ $rc -eq 1 ]"
  assert "S14 no validator run reported" "grep -q 'No validator run found' $WORK/out.log"
  assert "S14 no merge" "! grep -q 'MERGE_CALL' $WORK/gh.log"
}

# ============================================================
# S15: finalization → the re-run validator would BLOCK on the body↔diff
#      mismatch (A1, live-observed) unless the run block reconciles the PR
#      body to the finalized file first. The body-append (gh pr edit) adds
#      the marker → the re-run concludes success → revalidate → merge → tag.
# ============================================================
s15() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=action_required; VALIDATOR_CHECKRUN=absent
  A1_BLOCK=true
  PR_BODY="docs: change\n\nChange class: docs-only — a single new CHANGELOG/2026.232.0001.md markdown file."
  run_block; local rc=$?
  assert "S15 exit 0" "[ $rc -eq 0 ]"
  assert "S15 finalize message" "grep -q 'Finalizing CHANGELOG/2026.232.0001.md' $WORK/out.log"
  assert "S15 body reconciled before re-run" "grep -q 'BODY_EDIT' $WORK/gh.log"
  assert "S15 revalidation green (A1 resolved by the body-append)" "grep -q 'All status checks green' $WORK/out.log"
  assert "S15 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S15 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
}

# ============================================================
# S16: finalization → the body-append FAILS → the run continues (best-effort)
#      but the re-run validator blocks on the unresolved mismatch → the run
#      fails LOUDLY, exit 1, NO merge (the PR stays open for investigation).
# ============================================================
s16() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=action_required; VALIDATOR_CHECKRUN=absent
  A1_BLOCK=true; BODY_EDIT_FAIL=1
  PR_BODY="docs change\n\nChange class: docs-only — a single new CHANGELOG/2026.232.0001.md markdown file."
  run_block; local rc=$?
  assert "S16 exit 1" "[ $rc -eq 1 ]"
  assert "S16 body-append failure reported" "grep -q 'PR body finalization-note update failed' $WORK/out.log"
  assert "S16 re-validation blocked (A1)" "grep -q 'Not all checks pass' $WORK/out.log"
  assert "S16 no merge" "! grep -q 'MERGE_CALL' $WORK/gh.log"
}

# ============================================================
# S17: the marker-collision live defect (PR #26, run 32392375687). The
#      author's body ITSELF contains the literal `## Auto-merge finalization`
#      heading (describing the workflow). The OLD guard grepped for that
#      heading, so the append was silently skipped and the re-run validator
#      blocked on the body↔diff mismatch. The corrected guard greps for the
#      note's unique `<!-- auto-merge-finalization-note` HTML-comment marker —
#      author prose cannot contain it — so the append MUST still land.
# ============================================================
s17() {
  setup_git
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=action_required; VALIDATOR_CHECKRUN=absent
  A1_BLOCK=true
  BODY_EDIT_FAIL=0   # reset — s16 leaves it 1 and it is exported
  # author prose names the note heading AND pins the placeholder filename
  PR_BODY="docs change\n\nStep 3 appends the \`## Auto-merge finalization\` note naming the\nfinalized file. Change class names CHANGELOG/2026.232.0001.md."
  run_block; local rc=$?
  assert "S17 exit 0" "[ $rc -eq 0 ]"
  assert "S17 finalize message" "grep -q 'Finalizing CHANGELOG/2026.232.0001.md' $WORK/out.log"
  assert "S17 body-append executed despite heading in author prose" "grep -q 'BODY_EDIT' $WORK/gh.log"
  assert "S17 revalidation green (A1 resolved by the body-append)" "grep -q 'All status checks green' $WORK/out.log"
  assert "S17 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S17 tag created" "grep -q 'CREATED_TAG refs/tags/v' $WORK/gh.log"
}

# ============================================================
# S18: idempotency skip WITHOUT a finalize commit — a head whose file carries
#      the marker (e.g. a re-run after a finalized-then-blocked cycle, or the
#      healed canary PRs whose finalized files get the marker backfilled) is
#      recognized by the state-based check and merged with no re-finalization.
# ============================================================
s18() {
  setup_git
  # head file already finalized + marker present, but NO finalize commit in
  # the branch history (the marker is the state, not the history)
  git -C "$WORK/base" mv CHANGELOG/2026.232.0001.md CHANGELOG/2026.232.1406.md
  sed -i "1s/^# [0-9]\{4\}\.[0-9]\{3\}\.[0-9]\{4\}/# 2026.232.1406/" "$WORK/base/CHANGELOG/2026.232.1406.md"
  sed -i "1a\\<!-- auto-merge-finalized: 2026.232.1406 -->" "$WORK/base/CHANGELOG/2026.232.1406.md"
  git -C "$WORK/base" add -A
  git -C "$WORK/base" -c user.name=t -c user.email=t@t commit -qm "chore: changelog at merge stamp"
  git -C "$WORK/base" push -q origin feat/x
  git -C "$WORK/run" fetch -q origin
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; STATUS_COUNT=0; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S18 exit 0" "[ $rc -eq 0 ]"
  assert "S18 skip finalization message" "grep -q 'already finalized — skipping finalization' $WORK/out.log"
  assert "S18 no rename commit" "! grep -q 'Finalizing' $WORK/out.log"
  assert "S18 merged" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  assert "S18 tag uses changelog stamp" "grep -q 'CREATED_TAG refs/tags/v2026.232.1406' $WORK/gh.log"
}

# ============================================================
# S19: the tag-mint fix's happy path (the exact PR #27 live case). Tag ABSENT
#      (no local ref) → the fixed ensure_tag mints via POST → the "Tagged"
#      echo fires and CREATED_TAG lands in the log. The OLD guard read gh's
#      404 error body as non-empty and printed "already exists" without
#      minting — PR #27 merged but was never tagged.
# ============================================================
s19() {
  setup_git noplaceholder
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S19 exit 0" "[ $rc -eq 0 ]"
  assert "S19 mint POST executed" "grep -q 'CREATED_TAG refs/tags/v2026.231.1234' $WORK/gh.log"
  assert "S19 Tagged message" "grep -q 'Tagged v2026.231.1234' $WORK/out.log"
  assert "S19 no already-exists echo" "! grep -q 'already exists' $WORK/out.log"
}

# ============================================================
# S20: the POST fails (a concurrent run minted the tag first → 422
#      "Reference already exists"). The fixed ensure_tag treats a failed POST
#      as "already exists (concurrent mint)" and does NOT exit non-zero — the
#      tag is present, the run is still a success.
# ============================================================
s20() {
  setup_git noplaceholder
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; POST_FAIL=1; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S20 exit 0" "[ $rc -eq 0 ]"
  assert "S20 POST failure logged" "grep -q 'POST_FAILED' $WORK/gh.log"
  assert "S20 no mint recorded" "! grep -q 'CREATED_TAG' $WORK/gh.log"
  assert "S20 concurrent-mint message" "grep -q 'already exists (concurrent mint)' $WORK/out.log"
}

# ============================================================
# S21: the sdk tag-scheme guard — sdk tags under its Go-module v0.<YYYYDDD>.<HHMM
#      with all leading zeros stripped> form. Stamp 2026.232.0733 (07:33 UTC)
#      → v0.2026232.733, NOT the invalid plain v2026.232.0733 (semver forbids
#      the leading-zero segment). Tag ABSENT → the POST fires with the v0. form.
# ============================================================
s21() {
  setup_git noplaceholder
  REPO=opencharly/sdk; COMMIT_DATE=2026-08-20T07:33:00Z
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; POST_FAIL=0; HEAD_REPO=opencharly/sdk
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S21 exit 0" "[ $rc -eq 0 ]"
  assert "S21 sdk mint POST fires with the v0. form" "grep -q 'CREATED_TAG refs/tags/v0.2026232.733' $WORK/gh.log"
  assert "S21 sdk Tagged message" "grep -q 'Tagged v0.2026232.733' $WORK/out.log"
  assert "S21 no plain-v tag minted" "! grep -q 'refs/tags/v2026' $WORK/gh.log"
}

# ============================================================
# S22: sdk post-merge path (open PR, all green → native merge). The merged-HEAD
#      tag uses the v0. scheme for a non-zero HHMM (12:34 → 1234): v0.2026231.1234.
# ============================================================
s22() {
  setup_git noplaceholder
  REPO=opencharly/sdk; COMMIT_DATE=2026-08-19T12:34:56Z
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; POST_FAIL=0; HEAD_REPO=opencharly/sdk
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S22 exit 0" "[ $rc -eq 0 ]"
  assert "S22 sdk merged-HEAD tag is the v0. form" "grep -q 'CREATED_TAG refs/tags/v0.2026231.1234' $WORK/gh.log"
  assert "S22 no plain-v tag minted" "! grep -q 'refs/tags/v2026' $WORK/gh.log"
}

# ============================================================
# S23: sdk tag already present locally → the local-ref existence check skips
#      the mint under the v0. scheme (the already-exists path is unchanged).
# ============================================================
s23() {
  setup_git noplaceholder
  REPO=opencharly/sdk; COMMIT_DATE=2026-08-19T12:34:56Z
  git -C "$WORK/run" tag v0.2026231.1234 HEAD
  STATE=closed; MERGED=true; MS=deadbeef; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=true; POST_FAIL=0; HEAD_REPO=opencharly/sdk
  VALIDATOR_RUN_ID=none; VALIDATOR_CONCLUSION=""; VALIDATOR_CHECKRUN=absent
  run_block; local rc=$?
  assert "S23 exit 0" "[ $rc -eq 0 ]"
  assert "S23 no sdk tag POST" "! grep -q 'CREATED_TAG' $WORK/gh.log"
  assert "S23 sdk already-exists message" "grep -q 'Tag v0.2026231.1234 already exists' $WORK/out.log"
}

# ============================================================
# S24: placeholder PROSE naming the finalize marker must NOT be read as
#      already-finalized. The already-finalized guard greps for the marker
#      WITH the file's own basename stamp (`auto-merge-finalized: <basename>`),
#      which prose cannot contain. Regression for the live distro-arch #16
#      skip: prose "H1 rewrite + `auto-merge-finalized` marker" matched the
#      old naive substring grep, so the finalize was skipped and the
#      placeholder stamp (2026.232.0001) was minted as the release tag
#      instead of the merge stamp.
# ============================================================
s24() {
  rm -rf "$WORK"; mkdir -p "$WORK"
  git init -q -b main "$WORK/remote.git" --bare
  git init -q -b main "$WORK/base"
  git -C "$WORK/base" config user.email t@t; git -C "$WORK/base" config user.name t
  echo "base" > "$WORK/base/README.md"
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm base
  git -C "$WORK/base" remote add origin "$WORK/remote.git"
  git -C "$WORK/base" push -q origin main
  git -C "$WORK/base" checkout -qb feat/x
  mkdir -p "$WORK/base/CHANGELOG"
  printf '# 2026.232.0001 — placeholder\n\nThe placeholder is renamed to the merge-time CalVer (H1 rewrite + `auto-merge-finalized` marker) at merge.\n' > "$WORK/base/CHANGELOG/2026.232.0001.md"
  git -C "$WORK/base" add -A && git -C "$WORK/base" commit -qm "feat: add changelog placeholder"
  git -C "$WORK/base" push -q origin feat/x
  git clone -q "$WORK/remote.git" "$WORK/run"
  git -C "$WORK/run" config user.email t@t; git -C "$WORK/run" config user.name t
  git -C "$WORK/run" remote set-url origin "$WORK/remote.git"
  REPO=opencharly/fedora
  STATE=open; MERGED=false; MS=""; CHECK_PENDING=0; CHECK_CONCLUSION=success; STATUS_STATE=success; MERGE_RESULT=ok; TAG_EXISTS=false; HEAD_REPO="$REPO"
  VALIDATOR_RUN_ID=555; VALIDATOR_CONCLUSION=in_progress; VALIDATOR_CHECKRUN=present
  run_block; local rc=$?
  assert "S24 exit 0" "[ $rc -eq 0 ]"
  assert "S24 finalize fires (prose not read as marker)" "grep -q 'Finalizing CHANGELOG/2026.232.0001.md' $WORK/out.log"
  assert "S24 merged after finalize" "grep -q 'MERGE_CALL pr merge 20 --squash --auto' $WORK/gh.log"
  local newfile=$(git -C "$WORK/remote.git" ls-tree -r --name-only feat/x | grep -E '^CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$' | head -1)
  assert "S24 placeholder renamed" "[ \"$newfile\" != 'CHANGELOG/2026.232.0001.md' ]"
  local h1=$(git -C "$WORK/remote.git" show "feat/x:$newfile" | head -1)
  local stamp=$(basename "$newfile" .md)
  assert "S24 H1 rewritten to stamp" "echo \"$h1\" | grep -qE \"^# $stamp\""
  assert "S24 tag is the finalize stamp, not the placeholder" "grep -q 'CREATED_TAG refs/tags/v$stamp' $WORK/gh.log && ! grep -q 'CREATED_TAG refs/tags/v2026.232.0001' $WORK/gh.log"
}

# ---- run all scenarios ----
s1; s2; s3; s3b; s4; s4b; s5; s6; s7; s8; s9; s10; s10b; s11; s12; s13; s14; s15; s16; s17; s18; s19; s20; s21; s22; s23; s24
echo "============================"
echo "TOTAL: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
