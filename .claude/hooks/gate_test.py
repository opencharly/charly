#!/usr/bin/env python3
"""Behavioral tests for the narrow commit gate and push safety gate."""

import json
import os
import shutil
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
COMMIT_GATE = os.path.join(HERE, "pre-commit-gate.sh")
PUSH_GATE = os.path.join(HERE, "pre-push-gate.sh")

failures = []
ran = 0


def gate(script, command, cwd=None):
    result = subprocess.run(
        ["bash", script],
        input=json.dumps({"tool_input": {"command": command}}),
        capture_output=True,
        text=True,
        cwd=cwd,
    )
    return "BLOCK" if result.returncode == 2 else "ALLOW"


def expect(label, actual, expected):
    global ran
    ran += 1
    passed = actual == expected
    if not passed:
        failures.append(label)
    print("[%s] want=%-5s got=%-5s %s" % (
        "PASS" if passed else "FAIL", expected, actual, label,
    ))


def repo(go_module=False):
    path = tempfile.mkdtemp(prefix="gate-test-")
    for args in (("init", "-q"), ("config", "user.email", "t@t"),
                 ("config", "user.name", "t")):
        subprocess.run(["git", "-C", path, *args], capture_output=True)
    if go_module:
        with open(os.path.join(path, "go.mod"), "w") as stream:
            stream.write("module gatetest\n\ngo 1.24\n")
        with open(os.path.join(path, "main.go"), "w") as stream:
            stream.write("package main\n\nfunc main() {}\n")
    else:
        with open(os.path.join(path, "README.md"), "w") as stream:
            stream.write("# fixture\n")
    subprocess.run(["git", "-C", path, "add", "-A"], capture_output=True)
    subprocess.run(["git", "-C", path, "commit", "-qm", "initial"], capture_output=True)
    return path


clean = repo()
for label, command, expected in (
    ("commit: --no-verify blocked", "git commit --no-verify -m x", "BLOCK"),
    ("commit: -n blocked", "git commit -n -m x", "BLOCK"),
    ("commit: bundled -an blocked", "git commit -an -m x", "BLOCK"),
    ("commit: hooksPath override blocked", "git -c core.hooksPath=/x commit -m x", "BLOCK"),
    ("commit: bypass text inside message allowed", "git commit -m 'do not use --no-verify'", "ALLOW"),
    ("commit: attribution text is validator-owned", "git commit -m 'Assisted-by: arbitrary text'", "ALLOW"),
    ("commit: no attribution is not locally blocked", "git commit -m human", "ALLOW"),
    ("commit: clean heredoc allowed", "git commit -F - <<'EOF'\nbody\nEOF", "ALLOW"),
    ("commit: unparseable command blocked", 'git commit -m "unterminated', "BLOCK"),
    ("non-commit command allowed", "git add README.md", "ALLOW"),
):
    expect(label, gate(COMMIT_GATE, command, clean), expected)
shutil.rmtree(clean, ignore_errors=True)


if shutil.which("golangci-lint") is not None:
    bad = repo(go_module=True)
    with open(os.path.join(bad, "main.go"), "a") as stream:
        stream.write("\nfunc unused() {}\n")
    subprocess.run(["git", "-C", bad, "add", "main.go"], capture_output=True)
    expect("commit: lint failure blocked", gate(COMMIT_GATE, f"git -C {bad} commit -m x"), "BLOCK")
    shutil.rmtree(bad, ignore_errors=True)

    good = repo(go_module=True)
    with open(os.path.join(good, "main.go"), "w") as stream:
        stream.write("package main\n\nfunc main() { helper() }\nfunc helper() {}\n")
    subprocess.run(["git", "-C", good, "add", "main.go"], capture_output=True)
    expect("commit: lint-clean Go allowed", gate(COMMIT_GATE, f"git -C {good} commit -m x"), "ALLOW")
    shutil.rmtree(good, ignore_errors=True)


for label, command, expected in (
    ("push: --force blocked", "git push --force origin feat/x", "BLOCK"),
    ("push: -f blocked", "git push -f origin feat/x", "BLOCK"),
    ("push: force-with-lease blocked", "git push --force-with-lease origin feat/x", "BLOCK"),
    ("push: forced refspec blocked", "git push origin +feat/x", "BLOCK"),
    ("push: --no-verify blocked", "git push --no-verify origin feat/x", "BLOCK"),
    ("push: hooksPath override blocked", "git -c core.hooksPath=/x push origin feat/x", "BLOCK"),
    ("push: direct main blocked", "git push origin main", "BLOCK"),
    ("push: explicit main destination blocked", "git push origin HEAD:refs/heads/main", "BLOCK"),
    ("push: feature branch allowed", "git push origin feat/x", "ALLOW"),
    ("push: main as source allowed", "git push origin main:feat/x", "ALLOW"),
):
    expect(label, gate(PUSH_GATE, command), expected)

print("\n%d case(s), %d failure(s)" % (ran, len(failures)))
for failure in failures:
    print("  FAILED:", failure)
sys.exit(1 if failures else 0)
