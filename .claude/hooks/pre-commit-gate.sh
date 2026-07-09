#!/usr/bin/env bash
# PreToolUse(Bash) gate for `git commit` — a DISCIPLINE BACKSTOP, not a security
# boundary. Every change is independently re-validated by a fresh pr-validator
# agent and by GitHub branch protection, so this catches the common honest
# mistakes cheaply and leaves nuance (and the adversarial case) to them. It
# blocks (exit 2) a commit that:
#   - bypasses hooks: --no-verify / its -n alias / a core.hooksPath override,
#   - has an inline -m message with no `Assisted-by: Claude (<tier>)` trailer,
#   - carries a tier illegal on a commit (`theoretical suggestion`,
#     `syntax check only`, or any unknown tier; legal: `fully tested and
#     validated`, `analysed on a live system`, `documentation reviewed`),
#   - claims `documentation reviewed` on a staged diff that is not all-docs,
#   - carries a runtime tier but stages no CHANGELOG/<YYYY.DDD.HHMM>.md entry
#     (in a repo that tracks CHANGELOG/).
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
import json, os, re, subprocess, sys
sys.path.insert(0, sys.argv[2])
from gitcmd import git_invocations, hooks_path_override, dash_c_dir

try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

LEGAL = {"fully tested and validated", "analysed on a live system", "documentation reviewed"}
RUNTIME_TIERS = {"fully tested and validated", "analysed on a live system"}
CHANGELOG_ENTRY = re.compile(r'^CHANGELOG/[0-9]{4}\.[0-9]{3}\.[0-9]{4}\.md$')
DOC_PATH = re.compile(r'(?:^|/)(?:CHANGELOG|README|LICENSE|VISION)[^/]*$|\.(?:md|txt)$', re.IGNORECASE)


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


invocations = git_invocations(cmd, "commit")
if not invocations:
    sys.exit(0)

# --- per-invocation flag checks (string-level; no repo needed) --------------
commit_cwd = None
cwd_unresolvable = False
is_amend = False
has_inline_msg = False
for globs, args in invocations:
    if hooks_path_override(globs):
        block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config "
              "spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
    # A global `-C <dir>` retargets the repo this commit writes; scope the
    # diff-dependent checks there. If it is not a resolvable directory (a $VAR,
    # a nonexistent path), skip those checks rather than inspect the wrong repo
    # — the pr-validator still covers tier-vs-diff.
    d = dash_c_dir(globs)
    if d is not None:
        if os.path.isdir(d):
            commit_cwd = d
        else:
            cwd_unresolvable = True
    if "--amend" in args:
        is_amend = True
    # --no-verify / -n count only BEFORE a message provider (so a "--no-verify"
    # mention inside the message never trips). -n is the short alias, bundled
    # forms (-an, -amn...) included; a bundled m starts the message value.
    for t in args:
        if t in ("-m", "--message", "-F", "--file") or t.startswith(("--message=", "--file=")) \
                or re.match(r'^-[a-z]*[mF]', t):
            break
        if t == "--no-verify" or re.match(r'^-[a-z]*n[a-z]*$', t):
            block("`git commit --no-verify` (or its -n alias) bypasses the project hooks — "
                  "forbidden (CLAUDE.md: never bypass hooks).")
    if any(t in ("-m", "--message") or t.startswith("--message=") or re.match(r'^-[a-z]*m', t)
           for t in args):
        has_inline_msg = True

# --- attribution tier (string-level over the whole command) -----------------
tiers = [t.strip() for t in re.findall(r'Assisted-by:\s*Claude\s*\(([^)]*)\)', cmd)]
for tier in tiers:
    if tier == "syntax check only":
        block('committing at tier "syntax check only" is a CLAUDE.md violation (AI Attribution: '
              'this tier pairs with "do NOT commit" — R10 has not run; STOP and ask).')
    if tier not in LEGAL:
        block('illegal AI-attribution tier "%s". Legal on a commit: %s.' % (tier, sorted(LEGAL)))
if has_inline_msg and not tiers and '$(' not in cmd and '<<' not in cmd:
    block("commit message has no `Assisted-by: Claude (<tier>)` trailer (every commit Claude is "
          "involved in must attribute; docs-only commits use `documentation reviewed`).")


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
        block("runtime-tier commit stages no CHANGELOG/<YYYY.DDD.HHMM>.md entry in this repo — "
              "record it, or use a non-runtime tier if this is not a behavioral change.")


# B4 — staging that happens AT/AFTER this hook fires makes the diff checks lie.
# The hook runs ONCE per Bash call, BEFORE the command, so `git diff --cached`
# reflects the index NOW — not after a compound `git add … && git commit …` or a
# `git commit -a/--all` stages more. When such inline staging co-occurs with a
# tier that needs the diff, the gate cannot inspect the real commit, so it blocks:
# split `git add` and `git commit` into separate commands.
def commit_stages_inline():
    for globs, args in invocations:
        for t in args:
            # A short bundle containing 'a' (only commit's -a/--all uses 'a') stages
            # tracked changes — even when bundled with the message, e.g. -am. Check
            # it BEFORE the message-provider break, or `-am` slips through.
            if t == "--all" or (t.startswith("-") and not t.startswith("--") and "a" in t):
                return True
            if t in ("-m", "--message", "-F", "--file") or t.startswith(("--message=", "--file=")) \
                    or re.match(r'^-[a-z]*[mF]', t):
                break
    return bool(git_invocations(cmd, "add"))


needs_diff = ("documentation reviewed" in tiers) or any(t in RUNTIME_TIERS for t in tiers)
if needs_diff and not cwd_unresolvable and commit_stages_inline():
    block("this commit stages files inline (a `git add … && git commit …` in one command, "
          "or `git commit -a`), so the gate cannot inspect what will actually be committed — "
          "the hook runs before the command does. Run `git add` in a SEPARATE command first, "
          "then `git commit`, so the staged diff can be verified against the `%s` tier."
          % (tiers[0] if tiers else "declared"))

if not cwd_unresolvable:
    if "documentation reviewed" in tiers:
        assert_docs_only(commit_cwd)
    if not is_amend and any(t in RUNTIME_TIERS for t in tiers):
        assert_changelog(commit_cwd)

sys.exit(0)
PY
