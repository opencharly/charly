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
import re
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


# --- ZERO-ALIASES gate (v2) ---------------------------------------------------
# A deterministic backstop for the project rulebook's "Core is a PLUGIN HOST" ZERO-ALIASES
# standing rule: no NEW charly/*_aliases.go file, and no declaration-form
# re-export of a mechanism-kit symbol (an alias is a mislocated call site; the
# fix is MOVING the consumer into its owning plugin, never re-exporting). Alias
# files have NO migration exception; the IMPORT-PURITY residual-call-site
# exception (a body moving OUT of core in the SAME PR, net core-LOC negative)
# covers a plain kit import/call, NEVER an alias FORM. The fresh pr-validator is
# the architecture authority; this gate is the fail-open mechanical backstop that
# catches the unambiguous #86 (new alias file) / #87 (grown alias file) class the
# validator alone missed. A plain residual `kit.Foo()` CALL or `".../kit"` IMPORT
# is ALLOWED here — only alias FORMS block.
_KITS = r'(?:kit|deploykit|buildkit|loaderkit|vmshared|enginekit|statekit)'
# explicit declaration-form alias in ANY staged charly/*.go: `type X = kit.Y` / `var x = kit.Y`
ALIAS_DECL_EXPLICIT = re.compile(r'^\+.*\b(?:type|var)\s+\w+\s*=\s*' + _KITS + r'\.\w')
# grouped-block alias, scoped to charly/*_aliases.go only (avoids false positives on legit
# in-function `x = kit.var` reads in non-alias files): an indented `    Name = kit.Y` line.
ALIAS_DECL_GROUPED = re.compile(r'^\+\s+\w+\s*=\s*' + _KITS + r'\.\w+\s*(?:$|//)')
ALIASES_FILE = re.compile(r'(?:^|/)charly/.*_aliases\.go')


def merge_parent_shas(repo):
    """The 2nd+ parent commit-ish of an IN-PROGRESS merge, else [].

    A `git commit` that completes a merge (a conflict resolution, or an explicit
    `--no-commit` merge) records MERGE_HEAD naming the incoming parent(s). The
    staged diff (`git diff --cached`) compares ONLY against HEAD — the FIRST
    parent — so every file/line the merge pulls in from an incoming parent looks
    freshly 'added'. These SHAs let the gate tell a genuine addition (present in
    NEITHER parent) from a survivor merged in from an incoming parent (present in
    one). Empty on a normal commit, so the gate's non-merge behaviour is
    byte-for-byte unchanged."""
    out = git(["rev-parse", "MERGE_HEAD"], repo)
    if not out:
        return []
    return [s for s in (line.strip() for line in out.splitlines()) if s]


def blob_lines(repo, commitish, path):
    """The set of raw lines of `path` at `commitish` (empty if the path is absent)."""
    out = git(["show", "%s:%s" % (commitish, path)], repo)
    if out is None:
        return set()
    return set(out.splitlines())


def path_present(repo, shas, path):
    """True if `path` exists at ANY of the given commit-ish (a merge survivor)."""
    for sha in shas:
        if git(["cat-file", "-e", "%s:%s" % (sha, path)], repo) is not None:
            return True
    return False


def alias_gate(repo):
    base = os.path.abspath(repo or os.getcwd())
    if not os.path.isdir(os.path.join(base, "charly")):
        return  # not a charly superproject (e.g. an sdk/plugins submodule leg) — fail open
    # Merge-commit awareness: when the commit completes a merge, a file/line the
    # merge pulled in from an incoming parent is NOT branch-introduced and must
    # not be flagged (main's gofmt-realigned survivors trip the raw staged diff).
    # Only a GENUINE addition — present in the merge result but in NEITHER parent
    # (a new alias, or one invented during conflict resolution) — is caught.
    # merge_shas is empty on a normal commit, so that path is unchanged.
    merge_shas = merge_parent_shas(repo)
    parent_lines = {}  # path -> union of the path's lines across all incoming parents

    def merged_in_line(path, content):
        if not merge_shas or path is None:
            return False
        if path not in parent_lines:
            lines = set()
            for sha in merge_shas:
                lines |= blob_lines(repo, sha, path)
            parent_lines[path] = lines
        return content in parent_lines[path]

    # 1. A NEW charly/*_aliases.go file (status A) — absolute, no exception,
    #    UNLESS the merge carried it in from an incoming parent (already vetted
    #    on that parent's own landing).
    status = git(["diff", "--cached", "--name-status", "--no-renames", "--", "charly/*.go"], repo)
    new_alias_files = []
    if status:
        for line in status.splitlines():
            parts = line.split("\t")
            st = parts[0][:1] if parts else ""
            path = parts[-1] if len(parts) > 1 else (parts[0] if parts else "")
            if st == "A" and ALIASES_FILE.search(path):
                if merge_shas and path_present(repo, merge_shas, path):
                    continue  # merged in from an incoming parent, not branch-added
                new_alias_files.append(path)
    if new_alias_files:
        block("ZERO-ALIASES: a NEW charly/*_aliases.go file is staged (%s). Alias files have "
              "NO migration exception — an alias is a mislocated call site; move the consumer "
              "into its owning plugin instead of re-exporting. See the project rulebook 'The kernel/plugin "
              "boundary law' + the ZERO-ALIASES standing rule." % ", ".join(new_alias_files))
    # 2. Declaration-form kit-alias lines in the staged charly/*.go diff — again
    #    skipping any line the merge carried in from an incoming parent.
    diff = git(["diff", "--cached", "-U0", "--no-renames", "--", "charly/*.go"], repo)
    if not diff:
        return
    cur_path = None
    for line in diff.splitlines():
        m = re.match(r'^\+\+\+ b/(.+)$', line)
        if m:
            cur_path = m.group(1)
            continue
        if not line.startswith("+") or line.startswith("+++"):
            continue
        content = line[1:]  # the raw added line, sans the diff '+' marker
        if ALIAS_DECL_EXPLICIT.search(line):
            if merged_in_line(cur_path, content):
                continue  # survivor from an incoming merge parent, not a new alias
            block("ZERO-ALIASES: a declaration-form kit-alias line is staged in charly/ (%s). "
                  "Move the consumer into its owning plugin; never re-export a mechanism-kit "
                  "symbol (an alias is a mislocated call site). See the project rulebook." % line.strip()[:120])
        if cur_path and ALIASES_FILE.search(cur_path) and ALIAS_DECL_GROUPED.search(line):
            if merged_in_line(cur_path, content):
                continue  # survivor from an incoming merge parent, not a new alias
            block("ZERO-ALIASES: a grouped kit-alias line is staged in %s (%s). Alias files have "
                  "no exception — move the consumer, do not grow the alias file. See the project rulebook."
                  % (cur_path, line.strip()[:120]))


for global_args, commit_args in invocations:
    if hooks_path_override(global_args):
        block("a `core.hooksPath` override bypasses project hooks.")
    if has_no_verify(commit_args):
        block("`git commit --no-verify` (or `-n`) bypasses project hooks.")
    repo = dash_c_dir(global_args)
    if repo is not None and not os.path.isdir(repo):
        continue
    lint_staged_go(repo)
    alias_gate(repo)

sys.exit(0)
PY
