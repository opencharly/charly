#!/usr/bin/env bash
# PreToolUse(Bash) discipline backstop for `git commit`.
#
# This hook enforces only immediate local mechanics:
#   - no `--no-verify` / `-n` bypass;
#   - no `core.hooksPath` override;
#   - commit commands must tokenize cleanly; and
#   - staged Go modules must be golangci-lint clean when the tool is available.
#
# Attribution identity/confidence, change class, CHANGELOG coverage,
# architecture, and all R0-R10 proof are judged once by the fresh pr-validator.
# They are deliberately absent here so two policy implementations cannot drift.

INPUT=$(cat)
case "$INPUT" in
  *git*commit*) ;;
  *) exit 0 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 -B - "$INPUT" "$HERE" <<'PY'
import json
import os
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, sys.argv[2])
from gitcmd import dash_c_dir, git_invocations, hooks_path_override, mentions_subcommand


def block(message):
    sys.stderr.write("pre-commit-gate BLOCKED: " + message + "\n")
    sys.exit(2)


try:
    command = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

try:
    invocations = git_invocations(command, "commit")
except ValueError:
    if mentions_subcommand(command, "commit"):
        block("cannot parse this commit command; balance its quotes or use `git commit -F <file>`.")
    sys.exit(0)


def has_no_verify(args):
    """Inspect flags only; consume message values so their text is never a flag."""
    i = 0
    while i < len(args):
        token = args[i]
        if token in ("-m", "-F", "--message", "--file"):
            i += 2
            continue
        if token.startswith(("--message=", "--file=")):
            i += 1
            continue
        if token == "--no-verify":
            return True
        if token.startswith("-") and not token.startswith("--"):
            for flag in token[1:]:
                if flag in ("m", "F"):
                    break
                if flag == "n":
                    return True
        i += 1
    return False


def git(args, cwd=None):
    base = ["git"] + (["-C", cwd] if cwd else [])
    try:
        result = subprocess.run(base + args, capture_output=True, text=True, timeout=10)
    except Exception:
        return None
    return result.stdout if result.returncode == 0 else None


def touched_go_modules(repo):
    names = git(["diff", "--cached", "--name-only", "--diff-filter=ACMR", "--", "*.go"], repo)
    if not names:
        return set()
    base = os.path.abspath(repo or os.getcwd())
    roots = set()
    for name in names.splitlines():
        directory = os.path.dirname(os.path.join(base, name))
        while directory.startswith(base):
            if os.path.isfile(os.path.join(directory, "go.mod")):
                roots.add(directory)
                break
            parent = os.path.dirname(directory)
            if parent == directory:
                break
            directory = parent
    return roots


def lint_staged_go(repo):
    if shutil.which("golangci-lint") is None:
        return
    for root in sorted(touched_go_modules(repo)):
        env = dict(os.environ)
        if os.sep + "candy" + os.sep in root + os.sep:
            env["GOWORK"] = "off"
        try:
            with tempfile.TemporaryDirectory(prefix="charly-gate-lint-") as cache:
                env["GOCACHE"] = os.path.join(cache, "go-build")
                env["GOLANGCI_LINT_CACHE"] = os.path.join(cache, "golangci-lint")
                result = subprocess.run(
                    ["golangci-lint", "run"], cwd=root, env=env,
                    capture_output=True, text=True, timeout=180,
                )
        except subprocess.TimeoutExpired:
            sys.stderr.write(
                "pre-commit-gate NOTE: golangci-lint timed out in %s; "
                "the pr-validator remains the gate.\n" % root
            )
            continue
        except Exception:
            continue
        if result.returncode != 0:
            detail = ((result.stdout or "") + (result.stderr or "")).strip()
            block("golangci-lint reports issues in %s:\n%s" % (root, detail[:4000]))


for global_args, commit_args in invocations:
    if hooks_path_override(global_args):
        block("a `core.hooksPath` override bypasses project hooks.")
    if has_no_verify(commit_args):
        block("`git commit --no-verify` (or `-n`) bypasses project hooks.")
    repo = dash_c_dir(global_args)
    if repo is not None and not os.path.isdir(repo):
        continue
    lint_staged_go(repo)

sys.exit(0)
PY
