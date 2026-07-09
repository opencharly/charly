#!/usr/bin/env bash
# apply-branch-protection.sh — enforce the PR-only, agent-validated landing
# policy on every OpenCharly repo's `main` branch. Idempotent. Requires `gh`
# authenticated with admin on each repo.
#
#   scripts/apply-branch-protection.sh apply    # PATCH settings + PUT protection
#   scripts/apply-branch-protection.sh verify    # GET and report drift (default)
#   scripts/apply-branch-protection.sh apply opencharly/sdk   # a subset
#
# After `apply`, the ONLY way `main` advances in a repo is a PR carrying a green
# `charly/claude-validation` status (posted by the fresh pr-validator agent),
# merged by that agent via `gh pr merge --squash`. `enforce_admins=true` makes
# the block real for everyone, admins included; `required_linear_history` +
# squash-only keep `main` linear AND make every cutover exactly ONE commit on
# `main`, no matter how many fix commits its `feat/` branch accumulated across
# review rounds. That is what lets a PR be UPDATED IN PLACE (append + push) on a
# CHANGES-REQUESTED review instead of being closed and recreated — with the
# force-push ban left absolute. See CLAUDE.md "Post-Execution Policies" and
# /charly-internals:git-workflow.
#
# The protection body below is the exact shape proven live during the policy's
# RDD spike (required classic status context satisfies the gate; enforce_admins
# blocks direct pushes; strict serializes concurrent merges).
set -euo pipefail

# Every repo in the project (superproject + sdk + plugins + box/* + pkg/*).
REPOS=(
  opencharly/charly
  opencharly/sdk
  opencharly/plugins
  opencharly/distro-arch
  opencharly/distro-cachyos
  opencharly/distro-debian
  opencharly/distro-fedora
  opencharly/distro-ubuntu
  opencharly/pkg-arch
  opencharly/pkg-debian
  opencharly/pkg-fedora
)
CONTEXT="charly/claude-validation"
BRANCH="main"

mode="${1:-verify}"
shift || true
[ "$#" -gt 0 ] && REPOS=("$@")

protection_body() {
  cat <<JSON
{
  "required_status_checks": { "strict": true, "contexts": ["$CONTEXT"] },
  "enforce_admins": true,
  "required_pull_request_reviews": { "required_approving_review_count": 0 },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
}

apply_one() {
  local repo="$1"
  echo "→ $repo: merge settings (squash-only, delete-branch-on-merge)"
  gh api -X PATCH "repos/$repo" \
    -F allow_squash_merge=true -F allow_rebase_merge=false \
    -F allow_merge_commit=false -F delete_branch_on_merge=true \
    -F allow_auto_merge=false >/dev/null
  echo "→ $repo: branch protection on $BRANCH"
  protection_body | gh api -X PUT "repos/$repo/branches/$BRANCH/protection" --input - >/dev/null
}

verify_one() {
  local repo="$1" ok=1
  local s rb sq mc db
  s=$(gh api "repos/$repo" --jq '"\(.allow_rebase_merge) \(.allow_squash_merge) \(.allow_merge_commit) \(.delete_branch_on_merge)"')
  read -r rb sq mc db <<<"$s"
  local p pr strict ctx admins linear force del
  if ! p=$(gh api "repos/$repo/branches/$BRANCH/protection" 2>/dev/null \
        --jq '"\(.required_pull_request_reviews!=null) \(.required_status_checks.strict // false) \((.required_status_checks.contexts // [])|join(",")) \(.enforce_admins.enabled // false) \(.required_linear_history.enabled // false) \(.allow_force_pushes.enabled // false) \(.allow_deletions.enabled // false)"'); then
    echo "✗ $repo: NO branch protection on $BRANCH"
    return 1
  fi
  read -r pr strict ctx admins linear force del <<<"$p"
  if ! { [ "$sq" = true ] && [ "$rb" = false ] && [ "$mc" = false ] && [ "$db" = true ]; }; then
    echo "✗ $repo: merge-settings drift (squash=$sq rebase=$rb mergeCommit=$mc delBranch=$db)"; ok=0
  fi
  if ! { [ "$pr" = true ] && [ "$strict" = true ] && [ "$ctx" = "$CONTEXT" ] && [ "$admins" = true ] && [ "$linear" = true ] && [ "$force" = false ] && [ "$del" = false ]; }; then
    echo "✗ $repo: protection drift (pr=$pr strict=$strict ctx=$ctx admins=$admins linear=$linear force=$force del=$del)"; ok=0
  fi
  [ "$ok" = 1 ] && echo "✓ $repo: compliant"
  return $((1 - ok))
}

rc=0
for r in "${REPOS[@]}"; do
  case "$mode" in
    apply)  apply_one "$r"; verify_one "$r" || rc=1 ;;
    verify) verify_one "$r" || rc=1 ;;
    *) echo "usage: $0 {apply|verify} [repo ...]" >&2; exit 2 ;;
  esac
done
exit "$rc"
