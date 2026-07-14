#!/usr/bin/env bash
# PreToolUse(Bash) gate for `git commit` — a DISCIPLINE BACKSTOP, not a security
# boundary. Every change is independently re-validated by a fresh pr-validator
# agent and by GitHub branch protection, so this catches the common honest
# mistakes cheaply and leaves nuance (and the adversarial case) to them. It
# blocks (exit 2) a commit that:
#   - bypasses hooks: --no-verify / its -n alias / a core.hooksPath override,
#   - has a READABLE message (inline -m, a heredoc body, or a -F <file> the gate reads)
#     with no model-aware `Assisted-by: <harness> (<model>; <tier>)` trailer — OR a message the gate CANNOT read
#     to find the tier (a $(...)/backtick substitution, a piped/unreadable -F, or an
#     editor message with no -m/-F): the latter fails CLOSED (inline the tier with -m, or
#     point -F at a readable file),
#   - carries a tier illegal on a commit (`theoretical suggestion`,
#     `syntax check only`, or any unknown tier; legal: `fully tested and
#     validated`, `analysed on a live system`, `documentation reviewed`),
#   - claims `documentation reviewed` on a staged diff that is not all-docs,
#   - stages no CHANGELOG/<YYYY.DDD.HHMM>.md entry at ANY legal tier (in a repo
#     that tracks CHANGELOG/), except a pure submodule-pointer bump,
#   - stages a Go (*.go) change whose module is NOT golangci-lint-clean — the Go-lint
#     criterion, so dead/unused code cannot slip in the way the P10 VM-CLI sweep did. It
#     runs the CONFIGURED `golangci-lint run` (never --fix, never --enable-only) on each
#     touched module (GOWORK=off for candy/plugin-* candies), and fails OPEN when
#     golangci-lint is absent or times out (the pr-validator remains the real gate),
#   - changes the index LATE, after this hook has read it — `git commit -a`/`-i`/`-o`,
#     or an index-mutating git subcommand (add/stage/rm/mv/reset/restore/apply/
#     update-index) in the same command — while declaring a diff-dependent tier: the
#     gate would judge a stale (usually empty) diff, so it fails CLOSED. Run the
#     index-mutating command as its own Bash call first. Or
#   - cannot be TOKENIZED — an UNBALANCED or UNQUOTED quote (e.g. an apostrophe in
#     a heredoc body, or an unterminated `"`): the gate fails CLOSED and blocks it.
#     Balance the quotes; a clean heredoc / `-F <file>` message parses fine.
# It does NOT judge whether a tier is JUSTIFIED (that is the pr-validator's job),
# and it does not try to defeat obfuscation (eval, base64|bash, splitting the
# word `git`) — out of a local gate's reach by construction. Hooks gate
# mechanical invariants; agents judge proof. See /charly-internals:agents.
#
# Fast path: only a git-commit-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*commit*) ;;
  *) exit 0 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 -B - "$INPUT" "$HERE" <<'PY'
import json, os, re, shutil, subprocess, sys
sys.path.insert(0, sys.argv[2])
from gitcmd import git_invocations, hooks_path_override, dash_c_dir, mentions_subcommand

try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

LEGAL = {"fully tested and validated", "analysed on a live system", "documentation reviewed"}
ATTRIBUTION_RE = re.compile(
    r'Assisted-by:\s*([^\n(]+?)\s*\(\s*([^;\n()]+?)\s*;\s*([^\n)]+?)\s*\)')
CHANGELOG_ENTRY = re.compile(r'^CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$')
DOC_PATH = re.compile(r'(?:^|/)(?:CHANGELOG|README|LICENSE|VISION)[^/]*$|\.(?:md|txt)$', re.IGNORECASE)
# v2 architecture (CLAUDE.md "Core is a PLUGIN HOST" — ZERO-ALIASES / NO-NEW-ALIASES). A
# `charly/*_aliases.go` re-export keeps a capability CALL SITE in core; the fix is moving the
# call site into its owning plugin, never an alias. This blocks the DECLARATION FORM only —
# a `type X = kit.Y` / `var x = kit.Y` alias line, or a new `charly/*_aliases.go` file — NEVER
# a plain `= kit.Foo()` call or a bare import (a K-wave cutover MOVES kit-referencing call/import
# lines into core as a mechanism body leaves; those are legal residual sites, not aliases).
ALIASES_FILE = re.compile(r'(?:^|/)charly/.*_aliases\.go$')
_KITS = r'(?:kit|deploykit|buildkit|loaderkit|vmshared|enginekit|statekit)'
KIT_ALIAS_DECL = re.compile(
    r'^\+\s*type\s+\w+\s*=\s*' + _KITS + r'\.\w'          # type X = kit.Y   (type alias)
    r'|^\+\s*var\s+\w+\s*=\s*' + _KITS + r'\.\w+\s*(?://.*)?$')  # var x = kit.Y  (var alias, no call parens)


def block(msg):
    sys.stderr.write("pre-commit-gate BLOCKED: " + msg + "\n")
    sys.exit(2)


def git(args, cwd=None):
    base = ["git"] + (["-C", cwd] if cwd else [])
    try:
        out = subprocess.run(base + args, capture_output=True, text=True, timeout=10)
    except Exception:
        return None
    return out.stdout if out.returncode == 0 else None


try:
    invocations = git_invocations(cmd, "commit")
except ValueError:
    # An UNBALANCED or UNQUOTED quote (e.g. an apostrophe in a heredoc body, or an
    # unterminated `"`) — shlex cannot tokenize it. NEVER treat this as "no command"
    # (that is how a gate silently stops gating). FAIL CLOSED: if a `git … commit`
    # is plausibly present, block — no fallback re-parse. (A CLEAN heredoc tokenizes
    # fine and is judged normally.)
    if mentions_subcommand(cmd, "commit"):
        block("cannot parse this command — an unbalanced or unquoted quote (e.g. an apostrophe "
              "in a heredoc body, or an unterminated \") — so the gate cannot verify the commit. "
              "Balance the quotes; for a long message, use a file: `git commit -F msg.txt`.")
    sys.exit(0)
if not invocations:
    sys.exit(0)

# Commit flags that stage content THEMSELVES, after this hook has already read the
# index: -a/--all (all tracked modifications), -i/--include and -o/--only (the named
# pathspecs). The gate runs BEFORE the command, so it cannot see what they will add.
LATE_STAGING = {"--all", "--include", "--only"}
LATE_STAGING_SHORT = "aio"

# git subcommands that MUTATE THE INDEX. Any of them in the same command as a commit
# runs after this hook has read that index, so the diff the gate judged is not the diff
# the commit records. `add`/`stage`/`rm`/`mv`/`apply --cached`/`update-index` stage MORE
# than the gate saw; `reset`/`restore --staged` stage LESS (unstaging a CHANGELOG entry
# after the gate approved it is the same hole from the other side). Matched by VERB, not
# by flag: the remedy — run it as its own Bash call — is cheap and always available.
# `checkout`/`switch` are absent on purpose: they carry the index across unchanged.
#
# CEDED BOUNDARY — deliberate, not an oversight. The merge-family verbs (`cherry-pick -n`,
# `revert -n`, `merge --no-commit`, `stash pop`, `am`, `rebase`, `pull`) also mutate the
# index and are NOT matched here. They are rare in the honest workflow this gate serves;
# they fail OPEN into a stale-diff commit that the fresh `pr-validator` re-checks against
# the REAL diff; and widening the set trades genuine over-blocking risk (`git merge
# --ff-only origin/main && git commit` is a legitimate sequence) for a backstop the agent
# layer already provides. Hooks gate mechanical invariants; agents judge proof (CLAUDE.md
# "Agents, Workflows & Teams" / /charly-internals:agents). Do not "close" this without a
# real workflow that hits it — the gate is a discipline backstop, not a security boundary.
INDEX_MUTATING = ("add", "stage", "rm", "mv", "reset", "restore", "apply", "update-index")


# git-commit flags that REUSE an already-authored (already-gated) message instead of
# taking a NEW one: -c/-C <commit>, --reuse-message/--reedit-message, --fixup/--squash.
# Like --amend, they inherit a message the gate cannot (and need not) re-read, so a
# missing inline tier on them is NOT an unattributed new commit — they are exempt from
# the fail-closed below. Each consumes a ref value, mirroring -m/-F.
REUSE_OPTS = {"-c", "-C", "--reuse-message", "--reedit-message", "--fixup", "--squash"}


def scan_commit_args(args):
    """Walk a commit arg span POSITIONALLY, returning (has_no_verify, is_amend,
    has_inline_msg, stages_late, msg_files, reuses_msg). The value of
    -m/--message/-F/--file is CONSUMED, never scanned as a flag — so message text
    (which always contains 'a' via the mandatory model-aware `Assisted-by` trailer) is
    never mistaken for a flag, and a flag AFTER the message (`git commit -m x
    --no-verify`) is still seen. A -F/--file VALUE is CAPTURED into msg_files so the
    tier parser can READ the message file (the #35 fix: an external -F <file> hid the
    trailer from the cmd-only scan). reuses_msg flags the REUSE_OPTS (an inherited,
    already-gated message). In a short bundle the first m/F starts the message VALUE
    (`-am x` stages; `-ma` does not — its 'a' is the message)."""
    has_nv = is_amend = has_msg = late = reuses = False
    msg_files = []
    i = 0
    while i < len(args):
        t = args[i]
        if t in ("-m", "-F", "--message", "--file"):
            if t in ("-F", "--file") and i + 1 < len(args):
                msg_files.append(args[i + 1])       # capture the message-file path
            has_msg = has_msg or t in ("-m", "--message")
            i += 2                                  # consume the value token
            continue
        if t.startswith("--message="):
            has_msg = True; i += 1; continue
        if t.startswith("--file="):
            msg_files.append(t[len("--file="):]); i += 1; continue
        if t == "--no-verify":
            has_nv = True; i += 1; continue
        if t == "--amend":
            is_amend = True; i += 1; continue       # before the generic `--` arm
        if t in REUSE_OPTS:
            reuses = True
            i += 2 if (i + 1 < len(args) and not args[i + 1].startswith("-")) else 1
            continue
        if t.startswith(("--reuse-message=", "--reedit-message=", "--fixup=", "--squash=")):
            reuses = True; i += 1; continue
        if t in LATE_STAGING:
            late = True; i += 1; continue
        if t.startswith("--"):
            i += 1; continue                        # other long option
        if t.startswith("-") and len(t) > 1:        # short bundle
            for c in t[1:]:
                if c in ("m", "F"):
                    has_msg = has_msg or c == "m"
                    break                           # rest of the token is the message value
                if c == "n":
                    has_nv = True                   # -n is git-commit's --no-verify alias
                if c in LATE_STAGING_SHORT:
                    late = True
            i += 1; continue
        i += 1                                      # non-flag token (pathspec / stray)
    return has_nv, is_amend, has_msg, late, msg_files, reuses


def read_file_attributions(msg_files, cmd):
    """Read model-aware attribution from -F/--file message files (the #35 fix). Returns
    (file_attributions, unreadable). A '-' value is stdin: readable ONLY via a heredoc, whose
    body already lives in `cmd` (scanned there) — a PIPED '-' (no `<<`) is unreadable.
    A relative path is resolved against the gate's cwd (best effort; an unresolved or
    non-regular path -> unreadable -> fail CLOSED). Only a regular file is read (capped),
    never a device/FIFO that could block."""
    file_attributions, unreadable = [], False
    for mf in msg_files:
        if mf == "-":
            if "<<" not in cmd:
                unreadable = True            # piped stdin: content is not in the command
            continue                         # a heredoc body is already scanned from cmd
        path = mf if os.path.isabs(mf) else os.path.join(os.getcwd(), mf)
        if not os.path.isfile(path):
            unreadable = True
            continue
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                body = f.read(1 << 20)
        except OSError:
            unreadable = True
            continue
        file_attributions += ATTRIBUTION_RE.findall(body)
    return file_attributions, unreadable


def resolve_dir(d):
    # Classify a `-C` value: an absolute-or-relative real directory, or an
    # unresolvable one (a $VAR / `sub`/glob the shell expands but this gate,
    # running first, cannot). Returns (commit_cwd, cwd_unresolvable).
    if os.path.isdir(d):
        return d, False
    return None, True


# --- flag + repo checks (the command tokenized; no fallback path) -----------
commit_cwd = None
cwd_unresolvable = False
is_amend = False
has_inline_msg = False
stages_late = False
msg_files = []
reuses_msg = False
for globs, args in invocations:
    if hooks_path_override(globs):
        block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config "
              "spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
    # A global `-C <dir>` retargets the repo this commit writes; scope the
    # diff-dependent checks there. An unresolvable value fails CLOSED below.
    d = dash_c_dir(globs)
    if d is not None:
        commit_cwd, cwd_unresolvable = resolve_dir(d)
    nv, amend, msg, late, files, reuses = scan_commit_args(args)
    if nv:
        block("`git commit --no-verify` (or its -n alias) bypasses the project hooks — "
              "forbidden (CLAUDE.md: never bypass hooks).")
    is_amend = is_amend or amend
    has_inline_msg = has_inline_msg or msg
    stages_late = stages_late or late
    msg_files += files
    reuses_msg = reuses_msg or reuses

# An index-mutating git subcommand in the SAME command runs AFTER this hook read the
# index (the hook fires once, BEFORE the command runs) — the compound-command half of
# the same hole as `commit -a`. A mention inside a quoted message is one token, never
# an invocation, so it cannot false-trigger.
if any(git_invocations(cmd, verb) for verb in INDEX_MUTATING):
    stages_late = True

# --- attribution tier -------------------------------------------------------
# The tier is read from the COMMAND STRING (an inline -m value OR a heredoc body, both
# of which live in `cmd`) AND from any readable -F/--file message file (the #35 fix: an
# external -F <file> previously hid the trailer, so every tier-dependent check silently
# failed OPEN). What stays UNREADABLE — a `$(...)`/backtick substitution, a piped `-F -`,
# an unreadable -F path, or an editor message (no -m/-F/heredoc) — fails CLOSED below.
file_attributions, msg_file_unreadable = read_file_attributions(msg_files, cmd)
attributions = ATTRIBUTION_RE.findall(cmd) + file_attributions
tiers = [tier.strip() for _harness, _model, tier in attributions]
for harness, model, _tier in attributions:
    if not harness.strip() or not model.strip():
        block("AI attribution requires both a harness and a verified model name.")
for tier in tiers:
    if tier == "syntax check only":
        block('committing at tier "syntax check only" is a CLAUDE.md violation (AI Attribution: '
              'this tier pairs with "do NOT commit" — R10 has not run; STOP and ask).')
    if tier not in LEGAL:
        block('illegal AI-attribution tier "%s". Legal on a commit: %s.' % (tier, sorted(LEGAL)))
if len(set(tiers)) > 1:
    block("all AI-attribution lines on one commit must use the same confidence tier.")
# No tier found and the message is NOT inherited (amend/reuse): either the gate could
# not READ the message (fail CLOSED) or it read it and the trailer is genuinely ABSENT.
if not tiers and not is_amend and not reuses_msg:
    msg_unreadable = ("$(" in cmd) or ("`" in cmd) or msg_file_unreadable
    readable_msg = has_inline_msg or ("<<" in cmd) or (bool(msg_files) and not msg_file_unreadable)
    if msg_unreadable or not readable_msg:
        block("the gate cannot READ this commit's message to verify its attribution tier — a "
              "`$(...)`/backtick substitution, a piped or unreadable `-F` file, or an editor "
              "message (no -m/-F). Inline the trailer with `-m`, or point `-F <path>` at a "
              "readable file, so the tier is scannable (the gate fails CLOSED here, not open).")
    block("commit message has no model-aware `Assisted-by: <harness> (<model>; <tier>)` trailer "
          "(commits initiated by an AI harness must disclose authorship; docs-only commits use "
          "`documentation reviewed`). A 100% human-authored commit made outside this AI "
          "PreToolUse gate remains trailer-free.")


# --- diff-dependent checks (skipped when the target repo is unresolvable) ----
ZERO = re.compile(r'^0+$')
LINE_COMMENT = {'.go': '//', '.cue': '//', '.js': '//', '.ts': '//', '.c': '//', '.h': '//',
                '.cc': '//', '.cpp': '//', '.hpp': '//', '.rs': '//', '.java': '//', '.kt': '//',
                '.sh': '#', '.bash': '#', '.py': '#', '.rb': '#', '.pl': '#', '.yml': '#',
                '.yaml': '#', '.toml': '#', '.cfg': '#', '.ini': '#', '.mk': '#'}


def diff_all_comments(path, repo, rangespec):
    marker = LINE_COMMENT.get(os.path.splitext(path)[1].lower())
    if marker is None:
        return False
    args = (["diff", "--no-renames", "-U0", rangespec, "--", path] if rangespec
            else ["diff", "--cached", "--no-renames", "-U0", "--", path])
    diff = git(args, cwd=repo)
    if diff is None or "Binary files" in diff:
        return False
    saw = False
    for line in diff.splitlines():
        if line[:3] in ("+++", "---") or not line or line[0] not in "+-":
            continue
        content = line[1:].strip()
        if content:
            saw = True
            if not content.startswith(marker):
                return False
    return saw


def is_doc(path, repo=None, rangespec=None):
    return bool(DOC_PATH.search(path)) or diff_all_comments(path, repo, rangespec)


def submodule_nondoc(sub, old, new, repo):
    # A submodule pointer bump is documentation iff the submodule's own old..new
    # diff is all-documentation. None = cannot certify (objects absent / add / remove).
    if ZERO.match(old) or ZERO.match(new):
        return None
    subrepo = os.path.join(repo, sub) if repo else sub
    names = git(["diff", "--no-renames", "--name-only", old + ".." + new], cwd=subrepo)
    if names is None:
        return None
    return [f for f in names.splitlines() if f.strip() and not is_doc(f, subrepo, old + ".." + new)]


def raw_staged(repo):
    return git(["diff", "--cached", "--no-renames", "--raw"], cwd=repo)


def assert_docs_only(repo):
    raw = raw_staged(repo)
    if raw is None:
        block('the "documentation reviewed" tier needs to inspect the staged diff, but git could '
              'not read it here. Use a runtime tier, or fix the invocation.')
    bad = []
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        meta, _t, rest = line.partition('\t')
        f = meta[1:].split()
        path = rest.strip()
        if len(f) < 4:
            bad.append(path or meta); continue
        if f[0] == '160000' or f[1] == '160000':
            sb = submodule_nondoc(path, f[2], f[3], repo)
            if sb is None:
                block('the "documentation reviewed" tier cannot certify submodule bump "%s" as '
                      'documentation (objects absent, or an add/remove). Fetch it, or use a '
                      'runtime tier.' % path)
            bad.extend('%s -> %s' % (path, b) for b in sb)
        elif not is_doc(path, repo=repo):
            bad.append(path)
    if bad:
        block('the "documentation reviewed" tier is only legal for an all-documentation diff '
              '(*.md / CHANGELOG / README / LICENSE / VISION / *.txt, comment-only code edits, or '
              'a docs-only submodule bump). Non-documentation staged: %s. Use a runtime tier, or '
              'split the docs into their own commit.' % ', '.join(bad))


def assert_changelog(repo):
    if not (git(["ls-files", "CHANGELOG/"], cwd=repo) or "").strip():
        return  # repo has no CHANGELOG/ -> not gated
    raw = raw_staged(repo)
    if raw is None:
        return
    any_entry = entry = False
    only_gitlinks = True
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        any_entry = True
        meta, _t, rest = line.partition('\t')
        f = meta[1:].split()
        if not (f[0] == '160000' or (len(f) > 1 and f[1] == '160000')):
            only_gitlinks = False
        if len(f) > 4 and f[4][:1] in ('A', 'M') and CHANGELOG_ENTRY.search(rest.strip()):
            entry = True
    if not any_entry or only_gitlinks:
        return  # nothing staged, or a pure pointer bump (recorded in the submodule)
    if not entry:
        block("this commit stages no CHANGELOG/<YYYY.DDD.HHMM>.md entry in this repo — every "
              "commit at a legal attribution tier records one (a pure submodule-pointer bump, "
              "whose narrative lives in the submodule, is exempt). Add the CHANGELOG entry.")


# --- Go-lint criterion (tier-independent) ------------------------------------
# The `unused` linter needs WHOLE-package analysis (charly is one big package main), so
# the gate lints each touched MODULE, not just the changed files. It runs the CONFIGURED
# `golangci-lint run` (the deduped set CI + contributors see — never --enable-only, never
# --fix, which corrupts the tree). Fails OPEN (skip) when the tool is absent or times out.
GO_LINT_TIMEOUT = 180


def go_modules_touched(repo):
    """Module-root dirs (a dir with go.mod) that have a .go file in THIS commit: staged
    .go, plus — if the command late-stages (commit -a / -i / -o) — unstaged-modified .go
    (what -a would add). Empty when no Go is involved (a docs/CHANGELOG commit is skipped)."""
    files = set()
    staged = git(["diff", "--cached", "--name-only", "--diff-filter=ACMR", "--", "*.go"], cwd=repo)
    if staged:
        files.update(x for x in staged.splitlines() if x.strip())
    if stages_late:
        un = git(["diff", "--name-only", "--diff-filter=ACMR", "--", "*.go"], cwd=repo)
        if un:
            files.update(x for x in un.splitlines() if x.strip())
    base = repo or os.getcwd()
    roots = set()
    for f in files:
        d = os.path.dirname(os.path.join(base, f))
        while d and len(d) >= len(base):
            if os.path.isfile(os.path.join(d, "go.mod")):
                roots.add(d)
                break
            nd = os.path.dirname(d)
            if nd == d:
                break
            d = nd
    return roots


def assert_go_lint(repo):
    if shutil.which("golangci-lint") is None:
        return  # tool absent -> fail OPEN (the pr-validator remains the real gate)
    for root in sorted(go_modules_touched(repo)):
        env = dict(os.environ)
        if (os.sep + "candy" + os.sep) in (root + os.sep):
            env["GOWORK"] = "off"  # a plugin candy lints standalone, exactly as it builds
        try:
            out = subprocess.run(["golangci-lint", "run"], cwd=root, env=env,
                                 capture_output=True, text=True, timeout=GO_LINT_TIMEOUT)
        except subprocess.TimeoutExpired:
            sys.stderr.write("pre-commit-gate NOTE: golangci-lint timed out in %s — skipped "
                             "(the pr-validator remains the gate).\n" % root)
            continue
        except Exception:
            continue  # fail OPEN on any exec error
        if out.returncode != 0:
            detail = ((out.stdout or "") + (out.stderr or "")).strip()
            block("golangci-lint reports issues in %s — a Go commit must be lint-clean (this "
                  "criterion exists so dead/unused code cannot slip through the way the P10 "
                  "VM-CLI sweep's 21 orphaned symbols did). Fix them, then re-commit:\n%s"
                  % (root, detail[:4000]))


# The docs-tier and CHANGELOG checks inspect the STAGED DIFF of the repo this commit
# writes. Two things can make that diff not the one the commit will actually record —
# both fail CLOSED, because a gate that judges the wrong diff is worse than no gate:
#
#   1. An unresolvable `-C <dir>` (a $VAR / nonexistent path — the shell expands it,
#      but this gate runs first): the diff cannot be read at all.
#   2. LATE STAGING. The hook fires ONCE per Bash call, BEFORE the command runs, so a
#      `git commit -a` / `-i` / `-o`, or ANY index-mutating git subcommand in the same
#      command (add/stage/rm/mv/reset/restore/apply/update-index), changes the index
#      AFTER this check has read it — the gate would judge a stale (often EMPTY) diff
#      and wave the commit through. Run the index-mutating command as its own SEPARATE
#      Bash call so the gate inspects the real diff.
needs_diff = bool(tiers)
if needs_diff and cwd_unresolvable:
    block("this commit's `-C` path is not a resolvable directory (a $VAR or a nonexistent "
          "path), so the gate cannot inspect the staged diff for the `%s` tier. Re-issue with "
          "a literal absolute `-C /path/to/repo`; never `cd` into the repo (a subdirectory "
          "project root drops .claude/settings.json)." % (tiers[0] if tiers else "declared"))
if needs_diff and stages_late:
    block("this command changes the index AFTER the gate reads it — `git commit -a`/`-i`/`-o`, "
          "or an index-mutating git subcommand (%s) in the same command. This hook fires ONCE, "
          "BEFORE the command runs, so it would judge a stale (usually EMPTY) diff and wave the "
          "`%s` tier through unchecked. Run the index-mutating command as its OWN Bash call "
          "first (e.g. `git add -u`), then `git commit` in the next one."
          % ("/".join(INDEX_MUTATING), tiers[0] if tiers else "declared"))

def assert_no_new_alias(repo):
    """v2 ZERO-ALIASES teeth (tier-independent, fail-OPEN). Blocks a commit that ADDS a new
    `charly/*_aliases.go` file OR a declaration-form kit-alias line (`type X = kit.Y` /
    `var x = kit.Y`) in any `charly/*.go`. A plain residual kit CALL or import a K-wave move
    leaves behind is NOT an alias and is deliberately NOT matched — the pr-validator ARCHITECTURE
    GATE judges those (the migration-pattern exception)."""
    raw = raw_staged(repo)
    if raw is None:
        return  # fail OPEN (the pr-validator remains the real gate)
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        meta, _t, rest = line.partition('\t')
        f = meta[1:].split()
        path = rest.strip()
        status = f[4][:1] if len(f) > 4 else ''
        if status == 'A' and ALIASES_FILE.search(path):
            block("v2 architecture (ZERO-ALIASES): this commit ADDS a new alias file `%s`. An alias "
                  "re-export keeps a capability call site in core — move the call site into its owning "
                  "plugin, never re-export. See CLAUDE.md \"Core is a PLUGIN HOST\"." % path)
    diff = git(["diff", "--cached", "--no-renames", "-U0", "--", "charly/*.go"], cwd=repo)
    if diff is None:
        return
    for line in diff.splitlines():
        if line.startswith('+') and not line.startswith('+++') and KIT_ALIAS_DECL.match(line):
            block("v2 architecture (NO-NEW-ALIASES): this commit adds a declaration-form kit alias — "
                  "`%s`. An alias is a mislocated call site; move it into its owning plugin (a plain "
                  "residual kit call/import a same-PR mechanism move leaves is fine and is not matched "
                  "here). See CLAUDE.md \"Core is a PLUGIN HOST\"." % line[1:].strip()[:120])


if not cwd_unresolvable:
    assert_go_lint(commit_cwd)       # tier-independent: any Go commit must be lint-clean
    assert_no_new_alias(commit_cwd)  # tier-independent: v2 ZERO-ALIASES teeth (fail-open)
    if "documentation reviewed" in tiers:
        assert_docs_only(commit_cwd)
    if not is_amend and tiers:
        assert_changelog(commit_cwd)

sys.exit(0)
PY
