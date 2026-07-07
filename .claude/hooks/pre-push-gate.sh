#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git push` that:
#   - targets `main` (a direct push to main is forbidden — main advances ONLY
#     via an agent-validated PR merge; CLAUDE.md / git-workflow),
#   - force-pushes (forbidden on EVERY branch in EVERY repo; tags are add-only), or
#   - bypasses hooks (--no-verify, or a core.hooksPath override in git's global
#     options).
# It checks ONLY the push invocation's OWN argument span (up to the next shell
# separator), so a `git branch -f` or other `-f` elsewhere in the same command
# line never false-triggers. A bare `git push` with no refspec is not statically
# resolvable and is left to the server-side branch protection. Recognizes git in
# command position at start, after a separator, or after a shell keyword.
#
# Fast path: only a git-push-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*push*) ;;
  *) exit 0 ;;
esac

python3 - "$INPUT" <<'PY'
import json, re, sys
try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

def block(msg):
    sys.stderr.write("pre-push-gate BLOCKED: " + msg + "\n")
    sys.exit(2)

# git in command position (start / after ;&| / after a shell keyword),
# optional global opts (`-C path`, `-c k=v`, ...), then `push`, then capture
# the push invocation's arg span up to the next shell separator.
INVOKE = re.compile(
    r'(?:^\s*|[\n;&|]\s*|(?:^|\s)(?:if|then|elif|else|do|while|until)\s+)'
    r'git(?:\s+-{1,2}[A-Za-z][^\s]*(?:\s+[^\s-][^\s]*)?)*\s+push((?:\s+[^\s;&|]+)*)')

for m in INVOKE.finditer(cmd):
    args = m.group(1) or ''
    # A core.hooksPath override is the config spelling of --no-verify; the
    # `-c key=value` form lives in git's GLOBAL options (between `git` and
    # `push`), so scan only that span. Env-var config injection is out of
    # scope: the gate is a discipline backstop, not a security boundary.
    glob_opts = cmd[m.start(0):m.start(1)]
    if re.search(r'core\.hookspath', glob_opts, re.IGNORECASE):
        block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
    # -f matches bundled too (-fu, -uf, ...); the bundle charset is git-push's
    # value-less short options, mirroring the commit gate's -n bundle handling.
    if re.search(r'(?:^|\s)(?:--force|--force-with-lease|-[uqvnd46]*f[uqvnd46]*)(?:\s|=|$)', args):
        block("force-push is forbidden on every branch in every repo (CLAUDE.md: main only fast-forwards, tags are add-only). Remove --force / --force-with-lease / -f.")
    if re.search(r'(?:^|\s)--no-verify(?:\s|$)', args):
        block("`git push --no-verify` bypasses hooks — forbidden.")
    # Block any push whose refspec DESTINATION is `main`. The first non-flag
    # token in the push arg span is the remote; each later token is a refspec
    # whose destination is the part after the last ':' (a leading '+' force
    # marker stripped). `main` / `refs/heads/main` are forbidden destinations —
    # main advances ONLY via an agent-validated PR merge. A bare `git push` with
    # no refspec is not statically resolvable and is left to the server-side
    # branch protection (the authoritative block).
    non_flags = [t for t in args.split() if not t.startswith('-')]
    for spec in non_flags[1:]:
        dst = spec.split(':')[-1].lstrip('+')
        if dst in ('main', 'refs/heads/main'):
            block("direct push to `main` is forbidden — `main` advances ONLY via an agent-validated PR merge (CLAUDE.md / git-workflow). Push a `feat/` branch and open a PR; the pr-validator evaluates and merges it.")

sys.exit(0)
PY
