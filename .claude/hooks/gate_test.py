#!/usr/bin/env python3
"""Behavioural test for the pre-commit / pre-push PreToolUse gates + gitcmd.py.

Run: python3 .claude/hooks/gate_test.py   (exit 0 = all pass, 1 = a failure)

It drives the REAL gate scripts over throwaway git repos (the same path a live
`git commit` / `git push` takes), and asserts gitcmd's tokenization directly.
This pins the gate invariants in-tree: restoring a fail-open would fail a case
here. No dependency beyond the stdlib + git.
"""
import json
import os
import subprocess
import sys
import tempfile
import shutil

sys.dont_write_bytecode = True  # importing gitcmd must not leave a __pycache__
HERE = os.path.dirname(os.path.abspath(__file__))
CGATE = os.path.join(HERE, "pre-commit-gate.sh")
PGATE = os.path.join(HERE, "pre-push-gate.sh")
sys.path.insert(0, HERE)
import gitcmd  # noqa: E402

DOCS = "Assisted-by: Codex (OpenAI GPT-5.6 Sol; documentation reviewed)"
RUN = "Assisted-by: Codex (OpenAI GPT-5.6 Sol; fully tested and validated)"
CLAUDE_DOCS = "Assisted-by: Claude Code (Anthropic Claude Opus 4.8; documentation reviewed)"

fails = []
ran = 0


def gate(script, cmd, cwd=None):
    p = subprocess.run(["bash", script], input=json.dumps({"tool_input": {"command": cmd}}),
                       capture_output=True, text=True, cwd=cwd)
    return "BLOCK" if p.returncode == 2 else "ALLOW"


def expect(label, got, want):
    global ran
    ran += 1
    ok = got == want
    if not ok:
        fails.append(label)
    print("[%s] want=%-5s got=%-5s %s" % ("PASS" if ok else "FAIL", want, got, label))


def mkrepo(with_changelog=False):
    d = tempfile.mkdtemp(prefix="gate-test-")
    for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
        subprocess.run(["git", "-C", d, *a], capture_output=True)
    if with_changelog:
        os.makedirs(os.path.join(d, "CHANGELOG"))
        open(os.path.join(d, "CHANGELOG", "2026.001.0000.md"), "w").write("# old\n")
    open(os.path.join(d, "main.go"), "w").write("package main\n")
    open(os.path.join(d, "README.md"), "w").write("# d\n")
    subprocess.run(["git", "-C", d, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", d, "commit", "-qm", "i"], capture_output=True)
    return d


# The scripts are AI-harness PreToolUse gates, not repository Git hooks. A
# genuinely human commit made through ordinary Git remains trailer-free.
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("human edit\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
p = subprocess.run(["git", "-C", r, "commit", "-qm", "docs: human change"], capture_output=True)
expect("human git commit: no AI trailer accepted", p.returncode == 0, True)
shutil.rmtree(r, ignore_errors=True)


# --- gitcmd tokenization (the B1 surface: a clean heredoc TOKENIZES) ---------
def tok_ok(cmd):
    try:
        gitcmd.git_invocations(cmd, "commit")
        return True
    except ValueError:
        return False


expect("tokenize: clean heredoc -F - tokenizes", tok_ok("git commit -F - <<'EOF'\nclean body\nEOF"), True)
expect("tokenize: quoted-delim heredoc tokenizes", tok_ok("git commit -F - <<\"EOF\"\nclean\nEOF"), True)
expect("tokenize: -m command-substitution tokenizes", tok_ok('git commit -m "$(printf ok)"'), True)
expect("tokenize: apostrophe in heredoc body -> ValueError", tok_ok("git commit -F - <<'EOF'\nit's broken\nEOF"), False)
expect("tokenize: unterminated quote -> ValueError", tok_ok('git commit -m "unterminated'), False)
expect("tokenize: quoted spaced -C is one token",
       gitcmd.dash_c_dir(gitcmd.git_invocations('git -C "/a b/x" commit -m y', "commit")[0][0]) == "/a b/x", True)

# --- commit gate: bypass / trailer / tier (no repo needed) -------------------
for label, cmd, want in [
    ("commit: --no-verify blocked", f"git commit --no-verify -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: -n alias blocked", f"git commit -n -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: bundled -an blocked", f"git commit -an -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: core.hooksPath blocked", f"git -c core.hooksPath=/x commit -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: --no-verify AFTER message blocked", f"git commit -m 'x\n\n{DOCS}' --no-verify", "BLOCK"),
    ("commit: -n AFTER message blocked", f"git commit -m 'x\n\n{DOCS}' -n", "BLOCK"),
    ("commit: absent trailer blocked", "git commit -m 'x'", "BLOCK"),
    ("commit: Claude Code model-aware trailer allowed", f"git commit -m 'x\n\n{CLAUDE_DOCS}'", "ALLOW"),
    ("commit: legacy harness-only trailer blocked", "git commit -m 'x\n\nAssisted-by: Claude (documentation reviewed)'", "BLOCK"),
    ("commit: blank harness blocked", "git commit -m 'x\n\nAssisted-by: (OpenAI GPT-5.6 Sol; documentation reviewed)'", "BLOCK"),
    ("commit: blank model blocked", "git commit -m 'x\n\nAssisted-by: Codex (; documentation reviewed)'", "BLOCK"),
    ("commit: illegal tier blocked", "git commit -m 'x\n\nAssisted-by: Codex (OpenAI GPT-5.6 Sol; theoretical suggestion)'", "BLOCK"),
    ("commit: syntax-check-only tier blocked", "git commit -m 'x\n\nAssisted-by: Codex (OpenAI GPT-5.6 Sol; syntax check only)'", "BLOCK"),
    ("commit: multiple models same tier allowed", f"git commit -m 'x\n\n{DOCS}\n{CLAUDE_DOCS}'", "ALLOW"),
    ("commit: multiple models mixed tiers blocked", f"git commit -m 'x\n\n{DOCS}\n{RUN}'", "BLOCK"),
    ("commit: --no-verify INSIDE message allowed", f"git commit -m 'do not --no-verify\n\n{DOCS}'", "ALLOW"),
    ("commit: attached -m with 'a'-trailer not a bypass", f"git commit -m'x\n\n{DOCS}' --amend", "ALLOW"),
    ("commit: -c HEAD~1 (commit's own) allowed", f"git commit -c HEAD~1 -m 'x\n\n{DOCS}'", "ALLOW"),
    ("commit: echo mentioning git commit allowed", 'echo "git commit -m x --no-verify"', "ALLOW"),
    ("commit: unresolvable -C var at docs tier -> BLOCK", f'git -C "$P" commit -m \'x\n\n{DOCS}\'', "BLOCK"),
    ("commit: compound && echo done -> ALLOW", f"git commit -m 'x\n\n{DOCS}' && echo done", "ALLOW"),
    ("commit: apostrophe-in-heredoc + illegal tier -> BLOCK (fail closed)",
     "git commit -F - <<'EOF'\nit's x\n\nAssisted-by: Codex (OpenAI GPT-5.6 Sol; theoretical suggestion)\nEOF", "BLOCK"),
    # Late staging: the hook fires BEFORE the command runs, so anything that stages
    # during the command leaves the gate judging a stale/empty diff. Fail closed.
    ("commit: compound `add && commit` at docs tier -> BLOCK (late staging)",
     f"git add main.go && git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: compound `add && commit` at runtime tier -> BLOCK (late staging)",
     f"git add main.go && git commit -m 'x\n\n{RUN}'", "BLOCK"),
    ("commit: -a at docs tier -> BLOCK (late staging)", f"git commit -a -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: --all at runtime tier -> BLOCK (late staging)", f"git commit --all -m 'x\n\n{RUN}'", "BLOCK"),
    ("commit: bundled -am at docs tier -> BLOCK (late staging)", f"git commit -am 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: -o pathspec at docs tier -> BLOCK (late staging)",
     f"git commit -o main.go -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: `git stage` in same command -> BLOCK (late staging)",
     f"git stage main.go; git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    # Every other index-mutating verb: staging MORE than the gate saw...
    ("commit: `git rm` in same command -> BLOCK", f"git rm old.go && git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: `git mv` in same command -> BLOCK", f"git mv a.go b.go && git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: `git apply --cached` -> BLOCK", f"git apply --cached p.patch && git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    ("commit: `git update-index` -> BLOCK", f"git update-index --add m.go && git commit -m 'x\n\n{DOCS}'", "BLOCK"),
    # ...and staging LESS (unstaging a CHANGELOG entry the gate already approved).
    ("commit: `git reset` then runtime tier -> BLOCK",
     f"git reset CHANGELOG/2026.001.0000.md && git commit -m 'x\n\n{RUN}'", "BLOCK"),
    ("commit: `git restore --staged` then runtime tier -> BLOCK",
     f"git restore --staged CHANGELOG/x.md && git commit -m 'x\n\n{RUN}'", "BLOCK"),
    # checkout/switch carry the index across unchanged — must NOT block.
    ("commit: `git switch -c` then commit -> ALLOW", f"git switch -c feat/x && git commit -m 'x\n\n{DOCS}'", "ALLOW"),
    ("commit: `git checkout -b` then commit -> ALLOW", f"git checkout -b feat/x && git commit -m 'x\n\n{DOCS}'", "ALLOW"),
    # a verb name inside the quoted message is one token, never an invocation
    ("commit: 'git reset' inside the message -> ALLOW",
     f"git commit -m 'never git reset here\n\n{DOCS}'", "ALLOW"),
    # `-m` ATTACHED to a message starting with 'a' tokenizes as `-ma…`. The scan must
    # stop at 'm' (rest is the message value), never read that 'a' as --all.
    ("commit: attached -m'a…' is not --all -> ALLOW", f"git commit -m'a\n\n{DOCS}' --amend", "ALLOW"),
    # `git add` alone is not a commit at all.
    ("commit: bare `git add` (no commit) -> ALLOW", "git add main.go", "ALLOW"),
    # #35: an UNREADABLE -F <file> hides the tier from the cmd scan -> the gate can no
    # longer verify attribution -> fail CLOSED (this was the fail-OPEN hole; msg.txt does
    # not exist here). A readable -F file is exercised in the #35 block below.
    ("commit: -a with UNREADABLE -F file -> BLOCK (#35 fail-closed)", "git commit -a -F msg.txt", "BLOCK"),
    # A mention of "git add" inside the quoted message is one token, not an invocation.
    ("commit: 'git add' inside the message -> ALLOW",
     f"git commit -m 'do not git add here\n\n{DOCS}'", "ALLOW"),
]:
    expect(label, gate(CGATE, cmd), want)

# --- commit gate: docs-tier / changelog (need a repo via -C) -----------------
rc = mkrepo()
subprocess.run(["git", "-C", rc, "add", "main.go"], capture_output=True)  # a code change
open(os.path.join(rc, "main.go"), "a").write("func x(){}\n")
subprocess.run(["git", "-C", rc, "add", "main.go"], capture_output=True)
expect("commit: docs tier on CODE diff -> BLOCK", gate(CGATE, f"git -C {rc} commit -m 'x\n\n{DOCS}'"), "BLOCK")
shutil.rmtree(rc, ignore_errors=True)

rd = mkrepo()
open(os.path.join(rd, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", rd, "add", "README.md"], capture_output=True)
expect("commit: docs tier on DOCS diff -> ALLOW", gate(CGATE, f"git -C {rd} commit -m 'x\n\n{DOCS}'"), "ALLOW")
shutil.rmtree(rd, ignore_errors=True)

rcl = mkrepo(with_changelog=True)
open(os.path.join(rcl, "main.go"), "a").write("func y(){}\n")
subprocess.run(["git", "-C", rcl, "add", "main.go"], capture_output=True)
expect("commit: runtime tier, no CHANGELOG staged -> BLOCK", gate(CGATE, f"git -C {rcl} commit -m 'x\n\n{RUN}'"), "BLOCK")
open(os.path.join(rcl, "CHANGELOG", "2026.190.1300.md"), "w").write("# n\n")
subprocess.run(["git", "-C", rcl, "add", "CHANGELOG/2026.190.1300.md"], capture_output=True)
expect("commit: runtime tier, CHANGELOG staged -> ALLOW", gate(CGATE, f"git -C {rcl} commit -m 'x\n\n{RUN}'"), "ALLOW")
shutil.rmtree(rcl, ignore_errors=True)

# --- commit gate: #34 — a CHANGELOG entry is required at EVERY legal tier -----
# The gate historically fired the CHANGELOG check only for the two RUNTIME tiers, so a
# `documentation reviewed` skill/doc edit in a CHANGELOG-tracking repo slipped through
# with no history entry (the gap #35 hit — caught by convention, not the gate). It now
# fires for every legal tier; the pure-submodule-pointer-bump exemption survives.
rdl = mkrepo(with_changelog=True)
open(os.path.join(rdl, "README.md"), "a").write("more docs\n")
subprocess.run(["git", "-C", rdl, "add", "README.md"], capture_output=True)
expect("commit: docs tier, docs change, no CHANGELOG -> BLOCK (#34)",
       gate(CGATE, f"git -C {rdl} commit -m 'x\n\n{DOCS}'"), "BLOCK")
open(os.path.join(rdl, "CHANGELOG", "2026.190.1400.md"), "w").write("# n\n")
subprocess.run(["git", "-C", rdl, "add", "CHANGELOG/2026.190.1400.md"], capture_output=True)
expect("commit: docs tier, docs change, CHANGELOG staged -> ALLOW (#34)",
       gate(CGATE, f"git -C {rdl} commit -m 'x\n\n{DOCS}'"), "ALLOW")
shutil.rmtree(rdl, ignore_errors=True)


def mksubbump():
    """A superrepo (tracking CHANGELOG/) whose ONLY staged change is a docs-only
    submodule POINTER BUMP — the #81 scenario. The gate must ALLOW it with no
    CHANGELOG entry: assert_docs_only certifies the submodule's old..new diff is
    all-docs, and assert_changelog's only-gitlinks arm exempts the pure pointer bump
    (its narrative lives in the submodule). Returns (parent_to_rmtree, superrepo)."""
    parent = tempfile.mkdtemp(prefix="gate-subbump-")
    sub = os.path.join(parent, "sub")
    os.makedirs(sub)
    for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
        subprocess.run(["git", "-C", sub, *a], capture_output=True)
    open(os.path.join(sub, "README.md"), "w").write("# sub v1\n")
    subprocess.run(["git", "-C", sub, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", sub, "commit", "-qm", "v1"], capture_output=True)
    top = os.path.join(parent, "top")
    os.makedirs(top)
    for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
        subprocess.run(["git", "-C", top, *a], capture_output=True)
    os.makedirs(os.path.join(top, "CHANGELOG"))
    open(os.path.join(top, "CHANGELOG", "2026.001.0000.md"), "w").write("# old\n")
    subprocess.run(["git", "-C", top, "-c", "protocol.file.allow=always",
                    "submodule", "add", "-q", sub, "sub"], capture_output=True)
    subprocess.run(["git", "-C", top, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", top, "commit", "-qm", "init"], capture_output=True)
    # advance the submodule with a DOCS-ONLY commit IN PLACE, then stage just the bump
    topsub = os.path.join(top, "sub")
    open(os.path.join(topsub, "README.md"), "a").write("v2 docs\n")
    subprocess.run(["git", "-C", topsub, "commit", "-qam", "v2 docs"], capture_output=True)
    subprocess.run(["git", "-C", top, "add", "sub"], capture_output=True)
    return parent, top


sb_parent, sb_top = mksubbump()
expect("commit: docs tier, pure docs submodule pointer bump, no CHANGELOG -> ALLOW (#34 exemption)",
       gate(CGATE, f"git -C {sb_top} commit -m 'x\n\n{DOCS}'"), "ALLOW")
shutil.rmtree(sb_parent, ignore_errors=True)

# --- commit gate: #35 — tier read from -F files; unscannable forms fail CLOSED
# The tier historically came ONLY from the command string, so an external `-F <file>`
# (or a `$(...)` substitution) hid it and every tier-dependent check silently failed
# OPEN. Now a readable -F file is READ; a substitution / unreadable-F / editor message
# fails CLOSED. A heredoc body is already IN the command string — it stays scannable.


def mkmsg(with_tier=True):
    """A temp message FILE carrying (optionally) the `documentation reviewed` trailer."""
    p = tempfile.mktemp(prefix="gate35msg-", suffix=".txt")
    open(p, "w").write("subject\n" + ("\n%s\n" % DOCS if with_tier else ""))
    return p


# -F <file> with the tier + docs change + NO CHANGELOG -> BLOCK (tier now READ -> #34 fires)
r = mkrepo(with_changelog=True)
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
mf = mkmsg(True)
expect("commit: -F <file> docs tier, no CHANGELOG -> BLOCK (#35 tier read + #34)",
       gate(CGATE, "git -C %s commit -F %s" % (r, mf)), "BLOCK")
os.unlink(mf); shutil.rmtree(r, ignore_errors=True)

# -F <file> with the tier + docs change + CHANGELOG staged -> ALLOW
r = mkrepo(with_changelog=True)
open(os.path.join(r, "README.md"), "a").write("more\n")
open(os.path.join(r, "CHANGELOG", "2026.190.1500.md"), "w").write("# n\n")
subprocess.run(["git", "-C", r, "add", "README.md", "CHANGELOG/2026.190.1500.md"], capture_output=True)
mf = mkmsg(True)
expect("commit: -F <file> docs tier, CHANGELOG staged -> ALLOW (#35 tier read)",
       gate(CGATE, "git -C %s commit -F %s" % (r, mf)), "ALLOW")
os.unlink(mf); shutil.rmtree(r, ignore_errors=True)

# -F <file> docs tier on a CODE diff -> BLOCK (the docs-only check now fires for -F too)
r = mkrepo()
open(os.path.join(r, "main.go"), "a").write("func q(){}\n")
subprocess.run(["git", "-C", r, "add", "main.go"], capture_output=True)
mf = mkmsg(True)
expect("commit: -F <file> docs tier on CODE diff -> BLOCK (#35 tier read)",
       gate(CGATE, "git -C %s commit -F %s" % (r, mf)), "BLOCK")
os.unlink(mf); shutil.rmtree(r, ignore_errors=True)

# -F <file> with NO trailer -> BLOCK (readable file, attribution genuinely absent)
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
mf = mkmsg(False)
expect("commit: -F <file> no trailer -> BLOCK (#35 absent)",
       gate(CGATE, "git -C %s commit -F %s" % (r, mf)), "BLOCK")
os.unlink(mf); shutil.rmtree(r, ignore_errors=True)

# -F <nonexistent path> -> BLOCK (fail closed; the gate cannot read the tier)
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
expect("commit: -F <nonexistent file> -> BLOCK (#35 fail-closed)",
       gate(CGATE, "git -C %s commit -F /nonexistent/gate35/msg.txt" % r), "BLOCK")
shutil.rmtree(r, ignore_errors=True)

# -m "$(...)" command substitution -> BLOCK (fail closed; message not in the command)
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
mf = mkmsg(True)
expect('commit: -m "$(cat file)" substitution -> BLOCK (#35 fail-closed)',
       gate(CGATE, 'git -C %s commit -m "$(cat %s)"' % (r, mf)), "BLOCK")
os.unlink(mf); shutil.rmtree(r, ignore_errors=True)

# heredoc body carries the tier -> STILL scannable (in the command string), NOT a hole:
# docs tier + no CHANGELOG via heredoc -> BLOCK proves the heredoc tier is seen (#34 fires).
r = mkrepo(with_changelog=True)
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
expect("commit: heredoc docs tier, no CHANGELOG -> BLOCK (#35 heredoc still scannable)",
       gate(CGATE, "git -C %s commit -F - <<'EOF'\nsubject\n\n%s\nEOF" % (r, DOCS)), "BLOCK")
shutil.rmtree(r, ignore_errors=True)

# bare `git commit` (editor, no -m/-F) -> BLOCK (fail closed; message unreadable)
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
expect("commit: bare editor commit (no -m/-F) -> BLOCK (#35 fail-closed)",
       gate(CGATE, "git -C %s commit" % r), "BLOCK")
shutil.rmtree(r, ignore_errors=True)

# --amend --no-edit (no new message) -> ALLOW (inherits an already-gated message; exempt)
r = mkrepo()
open(os.path.join(r, "README.md"), "a").write("more\n")
subprocess.run(["git", "-C", r, "add", "README.md"], capture_output=True)
expect("commit: --amend --no-edit (inherited message) -> ALLOW (#35 exempt)",
       gate(CGATE, "git -C %s commit --amend --no-edit" % r), "ALLOW")
shutil.rmtree(r, ignore_errors=True)

# --- commit gate: the Go-lint criterion --------------------------------------
# The gate runs the CONFIGURED `golangci-lint run` on each touched Go MODULE. It fails
# OPEN when golangci-lint is absent (the pr-validator remains the gate), so the BLOCK
# cases only assert when the tool is installed; the docs-only ALLOW case is tool-free.
HAS_GOLANGCI = shutil.which("golangci-lint") is not None


def mkgorepo():
    """A base-committed clean git repo that IS a Go module (go.mod + main.go)."""
    d = tempfile.mkdtemp(prefix="gate-golint-")
    for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
        subprocess.run(["git", "-C", d, *a], capture_output=True)
    open(os.path.join(d, "go.mod"), "w").write("module golinttest\n\ngo 1.24\n")
    open(os.path.join(d, "main.go"), "w").write("package main\n\nfunc main() {}\n")
    subprocess.run(["git", "-C", d, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", d, "commit", "-qm", "i"], capture_output=True)
    return d


if HAS_GOLANGCI:
    rgd = mkgorepo()  # stage a change that introduces an UNUSED symbol -> BLOCK
    open(os.path.join(rgd, "main.go"), "w").write("package main\n\nfunc main() {}\n\nfunc dead() {}\n")
    subprocess.run(["git", "-C", rgd, "add", "main.go"], capture_output=True)
    expect("commit: Go module with an unused symbol -> BLOCK (golangci-lint)",
           gate(CGATE, f"git -C {rgd} commit -m 'x\n\n{RUN}'"), "BLOCK")
    shutil.rmtree(rgd, ignore_errors=True)

    rgc = mkgorepo()  # a lint-clean .go change (greet is called) -> ALLOW
    open(os.path.join(rgc, "main.go"), "w").write("package main\n\nfunc main() { greet() }\n\nfunc greet() {}\n")
    subprocess.run(["git", "-C", rgc, "add", "main.go"], capture_output=True)
    expect("commit: lint-clean Go module -> ALLOW", gate(CGATE, f"git -C {rgc} commit -m 'x\n\n{RUN}'"), "ALLOW")
    shutil.rmtree(rgc, ignore_errors=True)
else:
    print("[SKIP] Go-lint BLOCK/ALLOW cases (golangci-lint not installed; gate fails OPEN by design)")

# A docs-only change in a Go repo must NOT invoke golangci-lint (no .go staged) -> ALLOW.
# Tool-free: exercises go_modules_touched returning empty regardless of golangci-lint.
rgn = mkgorepo()
open(os.path.join(rgn, "README.md"), "w").write("# doc\n")
subprocess.run(["git", "-C", rgn, "add", "README.md"], capture_output=True)
expect("commit: docs-only change in a Go repo -> ALLOW (no .go staged, lint skipped)",
       gate(CGATE, f"git -C {rgn} commit -m 'x\n\n{DOCS}'"), "ALLOW")
shutil.rmtree(rgn, ignore_errors=True)

# quoted-space -C, CODE at docs tier -> BLOCK (the original fail-open).
# The space must be in the REPO dir name; put it inside our OWN mkdtemp parent and
# remove that parent. (Never rmtree(dirname(mkdtemp(...))) — that is the shared
# temp root, e.g. /tmp: an autonomous destroy of a non-disposable resource.)
sp_parent = tempfile.mkdtemp(prefix="gate-test-sp-")
sp = os.path.join(sp_parent, "s p")
os.makedirs(sp)
for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
    subprocess.run(["git", "-C", sp, *a], capture_output=True)
open(os.path.join(sp, "main.go"), "w").write("package main\n")
subprocess.run(["git", "-C", sp, "add", "-A"], capture_output=True)
subprocess.run(["git", "-C", sp, "commit", "-qm", "i"], capture_output=True)
open(os.path.join(sp, "main.go"), "a").write("func z(){}\n")
subprocess.run(["git", "-C", sp, "add", "main.go"], capture_output=True)
expect("commit: quoted spaced -C, CODE at docs tier -> BLOCK (original fail-open)",
       gate(CGATE, f'git -C "{sp}" commit -m \'x\n\n{DOCS}\''), "BLOCK")
shutil.rmtree(sp_parent, ignore_errors=True)

# --- commit gate: #49 v2 ZERO-ALIASES teeth (tier-independent, declaration-form only) ---
# Blocks a NEW charly/*_aliases.go file or a `type X = kit.Y` / `var x = kit.Y` alias line.
# Does NOT block a plain residual kit CALL or import (a K-wave mechanism move leaves those in
# core legitimately — the pr-validator's migration-pattern exception judges them).


def mkcharly():
    """A repo with a base-committed charly/ Go dir (no CHANGELOG/ → the changelog check no-ops,
    isolating the alias-block; charly/ is not a Go module → the go-lint check no-ops)."""
    d = tempfile.mkdtemp(prefix="gate-alias-")
    for a in (["init", "-q"], ["config", "user.email", "t@t"], ["config", "user.name", "t"]):
        subprocess.run(["git", "-C", d, *a], capture_output=True)
    os.makedirs(os.path.join(d, "charly"))
    open(os.path.join(d, "charly", "existing.go"), "w").write("package main\n\nfunc main() {}\n")
    subprocess.run(["git", "-C", d, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", d, "commit", "-qm", "i"], capture_output=True)
    return d


# NEW charly/*_aliases.go file -> BLOCK
r = mkcharly()
open(os.path.join(r, "charly", "vmshared_aliases.go"), "w").write("package main\n\ntype BoxConfig = deploykit.Box\n")
subprocess.run(["git", "-C", r, "add", "charly/vmshared_aliases.go"], capture_output=True)
expect("commit: NEW charly/*_aliases.go file -> BLOCK (#49 ZERO-ALIASES)",
       gate(CGATE, f"git -C {r} commit -m 'x\n\n{RUN}'"), "BLOCK")
shutil.rmtree(r, ignore_errors=True)

# a `type X = deploykit.Y` alias line added in a plain charly/*.go -> BLOCK
r = mkcharly()
open(os.path.join(r, "charly", "bar.go"), "w").write("package main\n\ntype BoxConfig = deploykit.Box\n")
subprocess.run(["git", "-C", r, "add", "charly/bar.go"], capture_output=True)
expect("commit: type-alias line in charly/*.go -> BLOCK (#49 NO-NEW-ALIASES)",
       gate(CGATE, f"git -C {r} commit -m 'x\n\n{RUN}'"), "BLOCK")
shutil.rmtree(r, ignore_errors=True)

# a plain residual kit CALL (not an alias) -> ALLOW (a K-wave move leaves these)
r = mkcharly()
open(os.path.join(r, "charly", "bar.go"), "w").write("package main\n\nfunc x() { _ = kit.Compute(1) }\n")
subprocess.run(["git", "-C", r, "add", "charly/bar.go"], capture_output=True)
expect("commit: residual kit CALL line in charly/*.go -> ALLOW (#49 migration exception)",
       gate(CGATE, f"git -C {r} commit -m 'x\n\n{RUN}'"), "ALLOW")
shutil.rmtree(r, ignore_errors=True)

# a plain residual kit IMPORT (not an alias) -> ALLOW (declaration-form guard skips imports)
r = mkcharly()
open(os.path.join(r, "charly", "bar.go"), "w").write('package main\n\nimport "github.com/opencharly/sdk/deploykit"\n\nvar _ = deploykit.Box{}\n')
subprocess.run(["git", "-C", r, "add", "charly/bar.go"], capture_output=True)
expect("commit: residual kit IMPORT line in charly/*.go -> ALLOW (#49 migration exception)",
       gate(CGATE, f"git -C {r} commit -m 'x\n\n{RUN}'"), "ALLOW")
shutil.rmtree(r, ignore_errors=True)

# --- push gate --------------------------------------------------------------
for label, cmd, want in [
    ("push: --force blocked", "git push --force origin feat/x", "BLOCK"),
    ("push: -f alias blocked", "git push -f origin feat/x", "BLOCK"),
    ("push: --force-with-lease blocked", "git push --force-with-lease origin feat/x", "BLOCK"),
    ("push: +refspec force to feat blocked", "git push origin +feat/x", "BLOCK"),
    ("push: +src:dst force blocked", "git push origin +feat/x:refs/heads/feat/y", "BLOCK"),
    ("push: +main force blocked", "git push origin +main", "BLOCK"),
    ("push: --no-verify blocked", "git push --no-verify origin feat/x", "BLOCK"),
    ("push: core.hooksPath blocked", "git -c core.hooksPath=/x push origin feat/x", "BLOCK"),
    ("push: direct to main blocked", "git push origin main", "BLOCK"),
    ("push: HEAD:refs/heads/main blocked", "git push origin HEAD:refs/heads/main", "BLOCK"),
    ("push: feat push allowed", "git push origin feat/x", "ALLOW"),
    ("push: main as SOURCE (main:feat/x) allowed", "git push origin main:feat/x", "ALLOW"),
    ("push: echo mentioning push allowed", 'echo "git push --force origin main"', "ALLOW"),
    ("push: compound feat/x && git log main -> ALLOW", "git push origin feat/x && git log main", "ALLOW"),
    ("push: unbalanced quote (apostrophe) + main -> BLOCK (fail closed)", "git push origin HEAD:main; echo it's", "BLOCK"),
]:
    expect(label, gate(PGATE, cmd), want)

print("\n%d case(s), %d failure(s)" % (ran, len(fails)))   # measured, never asserted
for f in fails:
    print("  FAILED:", f)
sys.exit(1 if fails else 0)
