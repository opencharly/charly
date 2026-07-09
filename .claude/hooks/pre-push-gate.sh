#!/usr/bin/env bash
# PreToolUse(Bash) gate for `git push` — a DISCIPLINE BACKSTOP, not a security
# boundary (GitHub branch protection is the authority). It blocks (exit 2) a
# push that:
#   - force-pushes (--force / --force-with-lease / -f) — forbidden on EVERY
#     branch in EVERY repo (main only fast-forwards, tags are add-only),
#   - bypasses hooks (--no-verify, or a core.hooksPath override), or
#   - targets `main` directly (main advances ONLY via an agent-validated PR
#     merge). A bare `git push` with no refspec is left to the server-side
#     branch protection, the authoritative block.
# Shares git-command parsing with pre-commit-gate via gitcmd.py; obfuscation is
# out of scope by construction (see gitcmd.py / /charly-internals:agents).
#
# Fast path: only a git-push-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*push*) ;;
  *) exit 0 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 - "$INPUT" "$HERE" <<'PY'
import json, re, sys
sys.path.insert(0, sys.argv[2])
from gitcmd import git_invocations, hooks_path_override

try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)


def block(msg):
    sys.stderr.write("pre-push-gate BLOCKED: " + msg + "\n")
    sys.exit(2)


for globs, args in git_invocations(cmd, "push"):
    if hooks_path_override(globs):
        block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config "
              "spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
    for t in args:
        if t in ("--force", "--force-with-lease") or t.startswith("--force-with-lease=") \
                or re.match(r'^-[a-z]*f[a-z]*$', t):
            block("force-push is forbidden on every branch in every repo (CLAUDE.md: main only "
                  "fast-forwards, tags are add-only). Remove --force / --force-with-lease / -f.")
        if t == "--no-verify":
            block("`git push --no-verify` bypasses hooks — forbidden.")
    # The first non-flag arg is the remote; each later one is a refspec whose
    # destination is the part after the last ':' (a leading '+' force marker
    # stripped). `main` / `refs/heads/main` are forbidden destinations.
    non_flags = [t for t in args if not t.startswith("-")]
    for spec in non_flags[1:]:
        dst = spec.split(":")[-1].lstrip("+")
        if dst in ("main", "refs/heads/main"):
            block("direct push to `main` is forbidden — `main` advances ONLY via an "
                  "agent-validated PR merge (CLAUDE.md / git-workflow). Push a `feat/` branch "
                  "and open a PR.")

sys.exit(0)
PY
