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
python3 -B - "$INPUT" "$HERE" <<'PY'
import json, re, sys
sys.path.insert(0, sys.argv[2])
from gitcmd import (git_invocations, hooks_path_override, is_force_refspec,
                    mentions_subcommand, refspec_dst)

try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)


def block(msg):
    sys.stderr.write("pre-push-gate BLOCKED: " + msg + "\n")
    sys.exit(2)


FORCE = "force-push is forbidden on every branch in every repo (project rulebook: main only " \
        "fast-forwards, tags are add-only). Remove --force / --force-with-lease / -f, " \
        "and any leading `+` on a refspec (`+feat/x` forces feat/x)."
NOVERIFY = "`git push --no-verify` bypasses hooks — forbidden."
HOOKSPATH = "`git -c core.hooksPath=...` bypasses the project's git hooks — the config " \
            "spelling of --no-verify; forbidden (project rulebook: never bypass hooks)."
TOMAIN = "direct push to `main` is forbidden — `main` advances ONLY via an agent-validated " \
         "PR merge (project rulebook / git-workflow). Push a `feat/` branch and open a PR."

try:
    invocations = git_invocations(cmd, "push")
except ValueError:
    # An UNBALANCED or UNQUOTED quote (shlex cannot tokenize it) — never treat as
    # "no push". FAIL CLOSED: if a `git … push` is plausibly present, block; no
    # fallback re-parse. (Returning "no command" here is how a gate stops gating.)
    if mentions_subcommand(cmd, "push"):
        block("cannot parse this command — an unbalanced or unquoted quote — so the gate "
              "cannot verify the push. Balance the quotes and run the push as its own command.")
    sys.exit(0)

for globs, args in invocations:
    if hooks_path_override(globs):
        block(HOOKSPATH)
    for t in args:
        if t in ("--force", "--force-with-lease") or t.startswith("--force-with-lease=") \
                or re.match(r'^-[a-z]*f[a-z]*$', t):
            block(FORCE)
        if t == "--no-verify":
            block(NOVERIFY)
    # The first non-flag arg is the remote; each later one is a refspec. A leading
    # `+` on the refspec is a FORCE update (`git push origin +feat/x`) — forbidden
    # on every branch, exactly like --force. Otherwise its destination is the part
    # after the last ':'; `main` / `refs/heads/main` are forbidden destinations.
    # Bounded to THIS invocation's segment, so `git push feat/x && git log main`
    # never trips.
    non_flags = [t for t in args if not t.startswith("-")]
    for spec in non_flags[1:]:
        if is_force_refspec(spec):
            block(FORCE)
        if refspec_dst(spec) in ("main", "refs/heads/main"):
            block(TOMAIN)

sys.exit(0)
PY
