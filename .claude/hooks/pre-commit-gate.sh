#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git commit` that:
#   - bypasses the project's git hooks (--no-verify, or its short alias -n
#     incl. bundled forms like -an, as a flag BEFORE the message — so a
#     "--no-verify" mention INSIDE a commit message never false-triggers; or
#     a core.hooksPath override in git's global options, the config spelling
#     of the same bypass), or
#   - carries an AI-attribution tier the CLAUDE.md table forbids on a commit
#     (`theoretical suggestion`, and `syntax check only` — the table pairs it
#     with "do NOT commit"), or any unknown tier (legal-on-commit set:
#     `fully tested and validated`, `analysed on a live system`,
#     `documentation reviewed`), or
#   - carries the `documentation reviewed` tier with a staged diff that is NOT
#     all-documentation (the tier is only honest for `*.md`/CHANGELOG/README/
#     LICENSE/VISION/`*.txt` files, comment-only code edits, or a submodule
#     pointer bump whose own old..new diff is itself all-documentation), or
#   - uses an inline -m message with NO `Assisted-by: Claude (<tier>)` trailer
#     (every commit Claude is involved in — in ANY way — must attribute; a
#     pure-human hand-commit does not pass through this PreToolUse gate), or
#   - carries a RUNTIME tier (`fully tested and validated` / `analysed on a live
#     system`) in a repo that TRACKS a CHANGELOG/ directory yet stages no
#     CHANGELOG/<YYYY.DDD.HHMM>.md entry (history lives in each repo's per-repo
#     per-CalVer CHANGELOG/; a runtime-tier cutover must record it). Exempt: a repo with no
#     CHANGELOG/ (hasn't adopted the structure), and a commit whose staged diff
#     is EXCLUSIVELY submodule pointer bumps (the entry lives in the submodule).
#     Fires only when a tier is parsed from the command string (inline -m or a
#     heredoc body — both ARE scanned), NOT for a tier delivered via an external
#     -F/--file message; and skipped for --amend (the amended commit already
#     recorded its entry).
# It does NOT judge whether the tier is JUSTIFIED by the proof — that is the
# AI's job (testing-validator + the pasted-proof rule). Hooks gate mechanical
# invariants; agents judge proof. See CLAUDE.md "Agents, Workflows & Teams"
# (Hooks doctrine) + /charly-internals:agents.
#
# Fast path: only a git-commit-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*commit*) ;;
  *) exit 0 ;;
esac

python3 - "$INPUT" <<'PY'
import json, os, re, shlex, subprocess, sys
try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

LEGAL = {"fully tested and validated", "analysed on a live system", "documentation reviewed"}
RUNTIME_TIERS = {"fully tested and validated", "analysed on a live system"}
# A per-repo per-CalVer changelog entry file: top-level CHANGELOG/<YYYY.DDD.HHMM>.md
# (NOT the README, and NOT a nested sub/CHANGELOG/... path — anchored to repo root
# to match the top-level `git ls-files CHANGELOG/` adoption check). The CalVer is
# computed once and shared by the changelog filename and the release git tag.
CHANGELOG_ENTRY = re.compile(r'^CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$')

def block(msg):
    sys.stderr.write("pre-commit-gate BLOCKED: " + msg + "\n")
    sys.exit(2)

class _UnparseableCommit(ValueError):
    """A commit the structured parser cannot cleanly resolve (e.g. a `git commit`
    hidden behind an unmodeled command wrapper). A ValueError subclass so the
    caller's fail-closed handler catches it alongside unbalanced-quote errors."""

# --- strict gate for the `documentation reviewed` tier ---------------------
# That tier is only honest when the staged diff is all-documentation: every
# staged file is a doc path OR a code file whose staged hunks are full-line
# comments / blanks only OR a submodule pointer bump whose own old..new diff is
# itself all-documentation (recursed one level — a bump that integrates submodule
# code is rejected). Conservative-safe: it may reject a trailing/block-comment-
# only edit (harmless — use a runtime tier there), but it never lets a behavioral
# change pass as docs. The gate is a discipline backstop, not a security boundary
# (a compound `git add ... && git commit` inspects the CURRENT index, like the
# rest of this gate's command-span scoping).
DOC_PATH = re.compile(r'(?:^|/)(?:CHANGELOG|README|LICENSE|VISION)[^/]*$|\.(?:md|txt)$',
                      re.IGNORECASE)
LINE_COMMENT = {
    '.go': '//', '.cue': '//', '.js': '//', '.ts': '//', '.c': '//', '.h': '//', '.cc': '//',
    '.cpp': '//', '.hpp': '//', '.rs': '//', '.java': '//', '.kt': '//', '.swift': '//',
    '.sh': '#', '.bash': '#', '.zsh': '#', '.py': '#', '.rb': '#', '.pl': '#',
    '.yml': '#', '.yaml': '#', '.toml': '#', '.cfg': '#', '.ini': '#', '.mk': '#',
}

def _git(args, cwd=None):
    base = ["git"] + (["-C", cwd] if cwd else [])
    try:
        out = subprocess.run(base + args, capture_output=True, text=True, timeout=10)
    except Exception:
        return None
    if out.returncode != 0:
        return None
    return out.stdout

def resolve_literal_dir(tok):
    # `tok` is a `-C` argument, already shell-tokenized (quotes removed) but NOT
    # expanded: this gate runs BEFORE the shell does. A token still carrying an
    # expansion sigil names a directory the hook cannot know, so it fails closed.
    # Never fall back to the hook's CWD: the commit would write one repo's index
    # while the docs-tier check inspected another's.
    hit = next((c for c in '$`~*?[' if c in tok), None)
    if hit:
        block(
            'cannot verify this commit: the `-C` path `{}` contains `{}`, which the shell '
            'expands but this gate — which reads the command before expansion — cannot '
            'resolve. Re-issue the command ONCE with a literal absolute path '
            '(`git -C /abs/path/to/repo commit ...`), per git-workflow B7. Do NOT `cd` into '
            'the repo (a subdirectory project root drops .claude/settings.json), and do NOT '
            'reshape-and-retry a command that was already denied — if you have been denied, '
            'stop and surface it.'.format(tok, hit))
    if not os.path.isdir(tok):
        block(
            'cannot verify this commit: `-C {}` is not a directory on this machine, so the '
            'staged diff cannot be inspected. Re-issue ONCE with a literal absolute path to '
            'the repository root.'.format(tok))
    return tok


# Shell separators, and the keywords a command word may hide behind.
_SEP_CHARS = set('&|;<>')
_SHELL_KW = {'if', 'then', 'elif', 'else', 'do', 'while', 'until', '!', '{', '}', '(', ')'}
_ENV_ASSIGN = re.compile(r'^\w+=')
# git GLOBAL options that take their value as the NEXT token. `commit`'s own
# `-c <commit>` (reuse-message) lives AFTER the subcommand and is never global.
_VALUE_OPTS = {'-C', '-c', '--git-dir', '--work-tree', '--namespace',
               '--config-env', '--exec-path', '--super-prefix'}


# Command-modifier words that run their REMAINING args as a command, so
# `<word> git commit` still executes a commit. Consumed like a shell keyword so
# the invocation is recognized. Not exhaustive — the adjacency safety net below
# fails closed on any wrapper this misses rather than letting a commit through.
_CMD_WRAPPERS = {'command', 'exec', 'nohup', 'time', 'nice', 'ionice', 'stdbuf',
                 'setsid', 'sudo', 'doas', 'env', 'xargs', 'builtin'}


def _normalize_shell(command):
    """Bring `command` into line with how bash itself would tokenize it, for the
    two things shlex gets backwards vs bash — and it gets them EXACTLY backwards:

      * a backslash-newline line-continuation: bash REMOVES it (joining the
        lines); shlex keeps it as a literal '\\n' token. Removed here.
      * a raw (unescaped) top-level newline: bash treats it as a command
        SEPARATOR; shlex swallows it as whitespace, merging two commands into
        one. Turned into a ';' here.

    Both are done with a single/double-quote-aware char walk (single quotes
    suppress every special meaning; a raw newline inside double quotes stays
    literal). Getting this wrong silently drops a real commit past the gate, so
    it is done at the source rather than patched token-by-token afterwards.
    """
    out = []
    i, n = 0, len(command)
    in_s = in_d = False
    while i < n:
        c = command[i]
        if in_s:                                   # single quotes: everything literal
            out.append(c)
            if c == "'":
                in_s = False
            i += 1
        elif c == '\\' and i + 1 < n:              # escape (also active in double quotes)
            if command[i + 1] == '\n':             # line continuation -> bash removes it
                i += 2
            else:
                out.append(c); out.append(command[i + 1]); i += 2
        elif c == '"':
            in_d = not in_d; out.append(c); i += 1
        elif c == "'" and not in_d:
            in_s = True; out.append(c); i += 1
        elif c == '\n' and not in_d:               # raw top-level newline -> separator
            out.append(' ; '); i += 1
        elif c == '`' and not in_s:                # backtick substitution RUNS its body;
            out.append(' '); i += 1                # expose it as top-level tokens
        else:
            out.append(c); i += 1
    return ''.join(out)


# Command words that RUN a following string as a shell command, so a commit can
# hide one level down. `eval` runs its concatenated args; a shell runs its `-c`
# argument. Recursed into (bounded depth) so the embedded commit is still judged.
_STR_EXEC_SHELLS = {'bash', 'sh', 'dash', 'zsh', 'ksh'}


def _embedded_command(seg):
    """If `seg` is a string-executing wrapper, return the command string it runs,
    else None. `eval a b c` runs "a b c"; `bash -c STR …` runs STR."""
    if not seg:
        return None
    w = os.path.basename(seg[0])
    if w == 'eval':
        return ' '.join(seg[1:]) or None
    if w in _STR_EXEC_SHELLS:
        for k in range(1, len(seg)):
            if seg[k] == '-c' and k + 1 < len(seg):
                return seg[k + 1]
            if seg[k].startswith('-c') and len(seg[k]) > 2:
                return seg[k][2:]
    return None


def git_commit_invocations(command, _depth=0):
    """Every `git [global-opts] commit [args]` in `command`, as (global_opts, args).

    Shell is TOKENIZED, never regex-matched: a regex cannot span a quoted argument
    containing a space, and one that tries will silently fail to match — skipping
    the entire gate and failing OPEN. Raises ValueError if `command` cannot be
    tokenized (the caller fails closed).
    """
    lex = shlex.shlex(_normalize_shell(command), posix=True, punctuation_chars=True)
    lex.whitespace_split = True
    tokens = list(lex)          # ValueError on unbalanced quotes

    segments, cur = [], []
    for t in tokens:
        if t and all(ch in _SEP_CHARS for ch in t):
            segments.append(cur)
            cur = []
        else:
            cur.append(t)
    segments.append(cur)

    out = []
    for seg in segments:
        # A `git commit` can hide inside eval / `bash -c STR`; recurse into the
        # embedded command string (bounded depth, so `bash -c "bash -c …"` can't
        # spin). A commit found down there is judged exactly as a top-level one.
        inner = _embedded_command(seg)
        if inner is not None and _depth < 3:
            out.extend(git_commit_invocations(inner, _depth + 1))
            continue
        i = 0
        while i < len(seg) and (seg[i] in _SHELL_KW or seg[i] in _CMD_WRAPPERS
                                or _ENV_ASSIGN.match(seg[i])):
            i += 1
        if i >= len(seg) or os.path.basename(seg[i]) != 'git':
            # Not in git-command position. Safety net: if `git` is nonetheless
            # immediately followed by `commit` anywhere in the segment, a wrapper
            # this code does not model is hiding a real commit — fail CLOSED
            # rather than let it through. (`echo "git commit"` tokenizes the
            # quoted text as ONE token, so it never trips this.)
            for j in range(len(seg) - 1):
                if os.path.basename(seg[j]) == 'git' and seg[j + 1] == 'commit':
                    raise _UnparseableCommit()
            continue
        i += 1
        glob_opts = []
        while i < len(seg) and seg[i].startswith('-'):
            opt = seg[i]
            glob_opts.append(opt)
            i += 1
            if opt in _VALUE_OPTS and i < len(seg) and not seg[i].startswith('-'):
                glob_opts.append(seg[i])
                i += 1
        if i < len(seg) and seg[i] == 'commit':
            out.append((glob_opts, seg[i + 1:]))
    return out


def _is_msg_provider(t):
    # -m/--message supply a message inline; -F/--file supply one from a file.
    # A bundled short form (-am"x" tokenizes to -amx) starts its VALUE at the m.
    return (t in ('-m', '--message', '-F', '--file')
            or t.startswith(('--message=', '--file='))
            or re.match(r'^-[aiopsvqezS]*[mF]', t) is not None)


def _is_inline_msg(t):
    return (t in ('-m', '--message')
            or t.startswith('--message=')
            or re.match(r'^-[aiopsvqezS]*m', t) is not None)


def changed_lines_all_comments(path, repo=None, rangespec=None):
    ext = os.path.splitext(path)[1].lower()
    marker = LINE_COMMENT.get(ext)
    if marker is None:
        return False  # unknown / binary type — cannot certify comment-only
    diffargs = (["diff", "--no-renames", "-U0", rangespec, "--", path] if rangespec
                else ["diff", "--cached", "--no-renames", "-U0", "--", path])
    diff = _git(diffargs, cwd=repo)
    if diff is None:
        return False
    if "Binary files" in diff:
        return False
    saw_content = False
    for line in diff.splitlines():
        if line.startswith('+++') or line.startswith('---'):
            continue
        if line and line[0] in '+-':
            content = line[1:].strip()
            if content == '':
                continue
            saw_content = True
            if not content.startswith(marker):
                return False
    # An EMPTY changeset (no +/- content lines) means the path matched no staged
    # hunk — cannot certify it as comment-only, so do NOT pass it as documentation.
    return saw_content

def _is_doc(path, repo=None, rangespec=None):
    if DOC_PATH.search(path):
        return True
    return changed_lines_all_comments(path, repo=repo, rangespec=rangespec)

ZERO = re.compile(r'^0+$')

def submodule_bad_files(sub, old, new, repo=None):
    # A staged submodule pointer bump is documentation IFF the submodule's own
    # old..new diff is itself all-documentation. Returns the non-doc file list
    # (empty == all docs), or None when the bump cannot be certified — objects
    # absent locally, or a submodule add/remove (all-zero old/new sha).
    if ZERO.match(old) or ZERO.match(new):
        return None
    subrepo = os.path.join(repo, sub) if repo else sub
    rangespec = old + ".." + new
    names = _git(["diff", "--no-renames", "--name-only", rangespec], cwd=subrepo)
    if names is None:
        return None
    bad = []
    for f in (x for x in names.splitlines() if x.strip()):
        if _is_doc(f, repo=subrepo, rangespec=rangespec):
            continue
        bad.append(f)
    return bad

def assert_docs_only_diff(repo=None):
    # The `documentation reviewed` tier is honest only when EVERY staged entry is
    # documentation: a doc path, a comment-only code edit, OR a submodule pointer
    # bump whose own old..new diff is itself all-documentation (recursed one
    # level). `--raw` exposes the gitlink mode (160000) + the old/new SHAs needed
    # to inspect the bumped submodule commit.
    raw = _git(["diff", "--cached", "--no-renames", "--raw"], cwd=repo)
    if raw is None:
        block('cannot verify this commit: the "documentation reviewed" tier requires inspecting '
              'the staged diff, but `git diff --cached --raw` failed — the target is not a git '
              'repository, or git is unusable here. (A `-C` path the shell would expand is '
              'reported separately, with its own remedy.) Fix the invocation, or use a runtime tier.')
    bad = []
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        meta, _tab, rest = line.partition('\t')
        fields = meta[1:].split()
        path = rest.strip()
        if len(fields) < 4:
            bad.append(path or meta)
            continue
        modeA, modeB, shaA, shaB = fields[0], fields[1], fields[2], fields[3]
        if modeA == '160000' or modeB == '160000':
            sub_bad = submodule_bad_files(path, shaA, shaB, repo=repo)
            if sub_bad is None:
                block('the "documentation reviewed" tier cannot certify the submodule pointer bump '
                      '"%s" as documentation: its objects are not present locally, or it adds/removes '
                      'a submodule. Fetch the submodule and retry, or use a runtime tier.' % path)
            bad.extend('%s -> %s' % (path, b) for b in sub_bad)
            continue
        if _is_doc(path, repo=repo):
            continue
        bad.append(path)
    if bad:
        block('the "documentation reviewed" tier is only legal for an all-documentation diff '
              '(*.md / CHANGELOG / README / LICENSE / VISION / *.txt, comment-only code edits, or a '
              'submodule pointer bump to an all-documentation submodule commit). Non-documentation '
              'changes staged: %s. The change touches code/config — use a runtime tier, or split the '
              'docs into their own commit.' % ', '.join(bad))

def assert_changelog_entry(repo=None):
    # A runtime-tier commit lands a behavioral cutover; in a repo that keeps a
    # per-repo per-CalVer CHANGELOG/ the history MUST be recorded. Require a
    # CHANGELOG/<YYYY.DDD.HHMM>.md entry in the staged diff. Exemptions (fail-open, a
    # discipline backstop not a security boundary):
    #   - the repo tracks no CHANGELOG/ (hasn't adopted the structure) -> pass;
    #   - nothing inspectable / not a repo -> pass;
    #   - the staged diff is EXCLUSIVELY submodule gitlink bumps (mode 160000) ->
    #     pass (the substance is recorded in the submodule's own CHANGELOG).
    tracked = _git(["ls-files", "CHANGELOG/"], cwd=repo)
    if not tracked or not tracked.strip():
        return  # repo has no CHANGELOG/ -> not gated
    raw = _git(["diff", "--cached", "--no-renames", "--raw"], cwd=repo)
    if raw is None:
        return  # cannot inspect the staged diff -> do not block on this check
    any_entry = entry_staged = False
    only_gitlinks = True
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        any_entry = True
        meta, _tab, rest = line.partition('\t')
        fields = meta[1:].split()
        modeA = fields[0] if fields else ''
        modeB = fields[1] if len(fields) > 1 else ''
        status = fields[4] if len(fields) > 4 else ''
        if not (modeA == '160000' or modeB == '160000'):
            only_gitlinks = False
        # Count an entry only when a TOP-LEVEL CHANGELOG/<YYYY.DDD.HHMM>.md is ADDED or
        # MODIFIED: a deletion does not "record" history, README.md is not an entry,
        # and --no-renames keeps each path on its own --raw line so the ^-anchor holds.
        if status[:1] in ('A', 'M') and CHANGELOG_ENTRY.search(rest.strip()):
            entry_staged = True
    if not any_entry:
        return  # nothing staged (--allow-empty) -> not our concern
    if only_gitlinks:
        # Pure submodule pointer bump: the substance AND its own CHANGELOG entry were
        # recorded — and independently gated — in the submodule's own commit. Fail-open
        # here by design; do not double-require a superproject entry for a bare bump.
        return
    if not entry_staged:
        block("runtime-tier commit lands a cutover but stages no CHANGELOG/<YYYY.DDD.HHMM>.md entry "
              "in this repo — record it (history -> this repo's CHANGELOG/, one file per CalVer version), "
              "or use a non-runtime tier if this is not a behavioral change.")

found = False
has_inline_msg = False
is_amend = False
commit_cwd = None

try:
    invocations = git_commit_invocations(cmd)
except _UnparseableCommit:
    # A `git commit` is present but hidden behind a command wrapper the parser
    # does not model. Fail CLOSED: an unrecognized shape must never pass unchecked.
    block('cannot verify this commit: a `git commit` is present but wrapped in a form '
          'this gate cannot analyze (an unrecognized command prefix). Re-issue it as a '
          'plain `git [-C <dir>] commit ...` so the gate can inspect it.')
except ValueError:
    # Unbalanced quotes: the command cannot be tokenized, so it cannot be judged.
    # Fail CLOSED whenever it plausibly IS a commit. Silently skipping is how a
    # gate stops gating (a quoted global-opt value containing a space used to
    # defeat the old regex outright, disabling --no-verify and tier checks too).
    if re.search(r'(?:^|[\s;&|])git\b', cmd) and re.search(r'\bcommit\b', cmd):
        block('cannot verify this commit: the command has unbalanced quotes and cannot be '
              'parsed, so the gate cannot inspect it. Re-issue it in a simple, quoted-balanced '
              'form.')
    invocations = []

for glob_opts, args in invocations:
    found = True
    # A core.hooksPath override is the config spelling of --no-verify. It lives in
    # git's GLOBAL options (before the `commit` subcommand), so only those are
    # scanned — commit's own `-c <commit>` (reuse-message) and a message merely
    # mentioning the key never false-trigger. Env-var config injection remains out
    # of scope: this gate is a discipline backstop, not a security boundary.
    for g in glob_opts:
        if 'core.hookspath' in g.lower():
            block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
    # A `-C <dir>` in the GLOBAL options retargets the repo whose index this commit
    # writes; scope the docs-tier diff inspection there so a `git -C <sub> commit`
    # is judged against the submodule's index, not the superproject's.
    for j, g in enumerate(glob_opts):
        if g == '-C' and j + 1 < len(glob_opts):
            commit_cwd = resolve_literal_dir(glob_opts[j + 1])
        elif g.startswith('-C') and len(g) > 2:          # attached form: -C/abs/path
            commit_cwd = resolve_literal_dir(g[2:])
    # --amend re-touches the commit at HEAD; its CHANGELOG entry (if runtime-tier)
    # was already recorded in that commit, so the staged delta need not re-add one.
    if '--amend' in args:
        is_amend = True
    # inline-message detection is scoped to THIS invocation's args, so a foreign -m
    # elsewhere on the line (grep -m 1 ...; git commit -F f) never triggers the
    # absent-trailer check.
    if any(_is_inline_msg(t) for t in args):
        has_inline_msg = True
    # --no-verify counts as a FLAG only BEFORE the message provider (-m/-F); a
    # "--no-verify" mention inside the message must not block. -n is git-commit's
    # short alias, matched bundled too (-an, -anm, ...); the bundle charset is
    # git-commit's value-less short options, and m may appear only AFTER the n (a
    # bundled m starts the message VALUE, so -amnope = -a -m "nope").
    for t in args:
        if _is_msg_provider(t):
            break
        if t == '--no-verify' or re.match(r'^-[aiopsvqezS]*n[aiopsvqezSm]*$', t):
            block("`git commit --no-verify` (or its -n short alias) bypasses the project hooks — forbidden (CLAUDE.md: never bypass hooks).")

if found:
    # The Assisted-by trailer is structured; scanning the whole command is correct.
    tiers = re.findall(r'Assisted-by:\s*Claude\s*\(([^)]*)\)', cmd)
    for t in tiers:
        tier = t.strip()
        if tier == "syntax check only":
            block('committing at tier "syntax check only" is a CLAUDE.md violation (AI Attribution: this tier pairs with "do NOT commit" — R10 has not run; STOP and ask).')
        if tier not in LEGAL:
            block('illegal AI-attribution tier "%s". Legal on a commit: %s. ("theoretical suggestion" is forbidden for shipped code.)' % (tier, sorted(LEGAL)))
        if tier == "documentation reviewed":
            assert_docs_only_diff(commit_cwd)
    # Runtime-tier commits land behavioral cutovers -> must record per-repo history.
    # (--amend re-touches an existing commit whose entry was already recorded -> skip.)
    if not is_amend and any(t.strip() in RUNTIME_TIERS for t in tiers):
        assert_changelog_entry(commit_cwd)
    if has_inline_msg and not tiers and '$(' not in cmd and '<<' not in cmd:
        block("commit message has no `Assisted-by: Claude (<tier>)` trailer (every commit Claude is involved in must attribute; add it inline with the tier your R10 proof supports — docs-only commits use `documentation reviewed`).")

sys.exit(0)
PY
