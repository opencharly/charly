#!/usr/bin/env python3
"""Provision the immutable, recursively initialized protected-main validator tree."""

from __future__ import annotations

import argparse
import pathlib
import re
import subprocess
import sys


FULL_SHA = re.compile(r"^[0-9a-f]{40}$")


def run(*args: str, cwd: pathlib.Path | None = None) -> str:
    result = subprocess.run(args, cwd=cwd, check=True, text=True, capture_output=True)
    return result.stdout.strip()


def provision(root: pathlib.Path, base: str, worktree: pathlib.Path) -> None:
    root = root.resolve()
    if not root.is_absolute() or not worktree.is_absolute():
        raise ValueError("--root and --worktree must be absolute paths")
    if not FULL_SHA.fullmatch(base):
        raise ValueError("--base must be a full 40-character SHA")
    if worktree.exists() and any(worktree.iterdir()):
        raise ValueError("--worktree must not exist or must be empty")
    protected = run("git", "-C", str(root), "rev-parse", "origin/main")
    if protected != base:
        raise ValueError(f"--base {base} is not fetched protected origin/main {protected}")

    created = False
    try:
        run("git", "-C", str(root), "worktree", "add", "--detach", str(worktree), base)
        created = True
        run("git", "-C", str(worktree), "submodule", "sync", "--recursive")
        run("git", "-C", str(worktree), "submodule", "update", "--init", "--recursive", "--checkout")

        if run("git", "-C", str(worktree), "rev-parse", "HEAD") != base:
            raise ValueError("provisioned worktree HEAD does not equal immutable protected base")
        status = run("git", "-C", str(worktree), "submodule", "status", "--recursive")
        bad = [line for line in status.splitlines() if line[:1] in {"-", "+", "U"}]
        if bad:
            raise ValueError("unready recursive submodules:\n" + "\n".join(bad))
        dirty = run(
            "git",
            "-C",
            str(worktree),
            "status",
            "--porcelain",
            "--ignore-submodules=none",
        )
        if dirty:
            raise ValueError("provisioned validator worktree is not clean:\n" + dirty)
        validator = worktree / "plugins/internals/agents/pr-validator.md"
        if not validator.is_file():
            raise ValueError(f"missing validator specification: {validator}")
        checks: list[str] = []
        for harness in ("claude", "codex"):
            command = [str(worktree / "plugins/setup"), harness, "--check", "developer"]
            run(*command, cwd=worktree)
            checks.append(" ".join(command[1:]) + "=passed")
    except Exception:
        if created:
            subprocess.run(
                ["git", "-C", str(root), "worktree", "remove", "--force", str(worktree)],
                check=False,
                capture_output=True,
            )
        raise
    print(f"validator-worktree={worktree}")
    print(f"protected-base={base}")
    print("recursive-submodules=ready")
    print("submodule-map=" + (status or "none"))
    print("checks=" + ", ".join(checks))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--worktree", type=pathlib.Path, required=True)
    args = parser.parse_args()
    try:
        provision(args.root, args.base, args.worktree)
    except (OSError, subprocess.CalledProcessError, ValueError) as error:
        print(f"validator worktree provisioning failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
