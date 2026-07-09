"""Shared, deliberately simple git-command inspection for the pre-commit and
pre-push PreToolUse gates.

These gates are DISCIPLINE BACKSTOPS, not security boundaries: every change is
independently re-validated by a fresh pr-validator agent and by GitHub
server-side branch protection. So this parses the common, honest command forms
with `shlex` and does NOT try to defeat deliberate obfuscation (eval,
base64|bash, splitting the command word) — that is out of a local gate's reach
by construction, and is the server's job. Keep this small.
"""
import os
import shlex

# git GLOBAL options that consume the NEXT token as their value. A subcommand's
# own options (e.g. commit's `-c <commit>`) sit AFTER the subcommand, so the
# scan — which stops at the first non-option token — never mistakes them.
_VALUE_OPTS = {"-C", "-c", "--git-dir", "--work-tree", "--namespace",
               "--config-env", "--exec-path", "--super-prefix"}


def git_invocations(cmd, subcommand):
    """Every `git [global-opts] <subcommand> [args]` in `cmd`, as a list of
    (global_opts, args). A simple linear scan over shlex tokens — quotes are
    handled (a quoted path with a space is one token), obfuscation is not.
    Returns [] if the command cannot be tokenized (a malformed command bash
    would not run either; the agent validator remains the real gate)."""
    try:
        toks = shlex.split(cmd)
    except ValueError:
        return []
    out, i = [], 0
    while i < len(toks):
        if os.path.basename(toks[i]) == "git":
            j = i + 1
            globs = []
            while j < len(toks) and toks[j].startswith("-"):
                globs.append(toks[j])
                j += 1
                if toks[j - 1] in _VALUE_OPTS and j < len(toks) and not toks[j].startswith("-"):
                    globs.append(toks[j])
                    j += 1
            if j < len(toks) and toks[j] == subcommand:
                out.append((globs, toks[j + 1:]))
            i = j
        else:
            i += 1
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
