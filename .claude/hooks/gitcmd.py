"""Shared git-command inspection for the pre-commit and pre-push PreToolUse gates.

These gates are DISCIPLINE BACKSTOPS, not security boundaries: every change is
re-validated by a fresh pr-validator agent and by GitHub branch protection. This
parses the common, honest command forms and does NOT try to defeat deliberate
obfuscation (splitting the command word, generating it at runtime) — an infinite
regress no static parser wins, and the server's job.

`git_invocations` TOKENIZES with shlex (posix, punctuation_chars) so a quoted
argument containing a space is one token (the fail-open a bare regex has), splits
on shell separators so a compound `… && …` does not bleed one command's args into
another, and raises `ValueError` when the command cannot be tokenized — an
UNBALANCED or UNQUOTED quote (e.g. an apostrophe in a heredoc body, or an
unterminated `"`). A CLEAN heredoc tokenizes fine and is judged normally; it is
the stray quote, not the heredoc, that fails. The caller MUST fail closed on that
ValueError — returning "no command" there is how a gate silently stops gating.
"""
import os
import re
import shlex

# git GLOBAL options that consume the NEXT token as their value. A subcommand's
# own options (commit's `-c <commit>`) sit AFTER the subcommand, so a scan that
# stops at the first non-option token never mistakes them for globals.
_VALUE_OPTS = {"-C", "-c", "--git-dir", "--work-tree", "--namespace",
               "--config-env", "--exec-path", "--super-prefix"}

# Shell keywords / command-modifier words a real `git` may hide behind.
_SKIP = {"if", "then", "elif", "else", "do", "while", "until", "!",
         "command", "exec", "nohup", "time", "nice", "ionice", "stdbuf",
         "setsid", "sudo", "doas", "env", "xargs", "builtin"}
_ENV_ASSIGN = re.compile(r"^\w+=")


def _tokenize(cmd):
    """shlex tokens for `cmd`; `&&`/`||`/`;`/`|`/`&`/`(`/`)` are their own
    punctuation tokens, `#` starts a comment. Raises ValueError if unparseable."""
    lex = shlex.shlex(cmd, posix=True, punctuation_chars=True)
    lex.whitespace_split = True
    return list(lex)


def _is_sep(tok):
    return tok != "" and all(ch in "&|;()<>" for ch in tok)


def git_invocations(cmd, subcommand):
    """Every `git [global-opts] <subcommand> [args]` in `cmd`, as a list of
    (global_opts, args) with args bounded at the next shell separator. Raises
    ValueError if `cmd` cannot be tokenized — the caller fails closed."""
    tokens = _tokenize(cmd)                        # ValueError propagates
    segments, cur = [], []
    for t in tokens:
        if _is_sep(t):
            segments.append(cur)
            cur = []
        else:
            cur.append(t)
    segments.append(cur)

    out = []
    for seg in segments:
        i = 0
        while i < len(seg) and (seg[i] in _SKIP or _ENV_ASSIGN.match(seg[i])):
            i += 1
        if i >= len(seg) or os.path.basename(seg[i]) != "git":
            continue
        i += 1
        globs = []
        while i < len(seg) and seg[i].startswith("-"):
            globs.append(seg[i])
            i += 1
            if globs[-1] in _VALUE_OPTS and i < len(seg) and not seg[i].startswith("-"):
                globs.append(seg[i])
                i += 1
        if i < len(seg) and seg[i] == subcommand:
            out.append((globs, seg[i + 1:]))       # bounded to THIS segment
    return out


def hooks_path_override(globs):
    """True if the global options set core.hooksPath (the config spelling of
    --no-verify)."""
    return any("core.hookspath" in g.lower() for g in globs)


def dash_c_dir(globs):
    """The directory named by a global `-C <dir>` / `-C<dir>`, else None."""
    for k, g in enumerate(globs):
        if g == "-C" and k + 1 < len(globs):
            return globs[k + 1]
        if g.startswith("-C") and len(g) > 2:
            return g[2:]
    return None
