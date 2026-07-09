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

DOCS = "Assisted-by: Claude (documentation reviewed)"
RUN = "Assisted-by: Claude (fully tested and validated)"

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
    ("commit: illegal tier blocked", "git commit -m 'x\n\nAssisted-by: Claude (theoretical suggestion)'", "BLOCK"),
    ("commit: syntax-check-only tier blocked", "git commit -m 'x\n\nAssisted-by: Claude (syntax check only)'", "BLOCK"),
    ("commit: --no-verify INSIDE message allowed", f"git commit -m 'do not --no-verify\n\n{DOCS}'", "ALLOW"),
    ("commit: attached -m with 'a'-trailer not a bypass", f"git commit -m'x\n\n{DOCS}' --amend", "ALLOW"),
    ("commit: -c HEAD~1 (commit's own) allowed", f"git commit -c HEAD~1 -m 'x\n\n{DOCS}'", "ALLOW"),
    ("commit: echo mentioning git commit allowed", 'echo "git commit -m x --no-verify"', "ALLOW"),
    ("commit: unresolvable -C var at docs tier -> BLOCK", f'git -C "$P" commit -m \'x\n\n{DOCS}\'', "BLOCK"),
    ("commit: compound && echo done -> ALLOW", f"git commit -m 'x\n\n{DOCS}' && echo done", "ALLOW"),
    ("commit: apostrophe-in-heredoc + illegal tier -> BLOCK (fail closed)",
     "git commit -F - <<'EOF'\nit's x\n\nAssisted-by: Claude (theoretical suggestion)\nEOF", "BLOCK"),
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
    # `-m` ATTACHED to a message starting with 'a' tokenizes as `-ma…`. The scan must
    # stop at 'm' (rest is the message value), never read that 'a' as --all.
    ("commit: attached -m'a…' is not --all -> ALLOW", f"git commit -m'a\n\n{DOCS}' --amend", "ALLOW"),
    # `git add` alone is not a commit at all.
    ("commit: bare `git add` (no commit) -> ALLOW", "git add main.go", "ALLOW"),
    # No diff-dependent tier parsed (-F file) -> the diff checks never run, so -a is moot.
    ("commit: -a with -F file (no inline tier) -> ALLOW", "git commit -a -F msg.txt", "ALLOW"),
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
