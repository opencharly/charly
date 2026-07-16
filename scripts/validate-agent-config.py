#!/usr/bin/env python3
"""Check semantic parity between the independent Claude and Codex rulebooks."""

from __future__ import annotations

import pathlib
import re
import json
import subprocess
import sys
import tempfile
import tomllib


ROOT = pathlib.Path(__file__).resolve().parents[1]
SKILL_REF = re.compile(
    r"/charly-([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)(?![A-Za-z0-9_-])"
)


def dispatcher(path: pathlib.Path) -> list[tuple[str, ...]]:
    text = path.read_text()
    section = text.split("## Skill Dispatcher", 1)[1].split("Full index:", 1)[0]
    rows: list[tuple[str, ...]] = []
    for line in section.splitlines():
        if not line.startswith("|"):
            continue
        refs = tuple(
            f"/charly-{plugin}:{name}" for plugin, name in SKILL_REF.findall(line)
        )
        if refs:
            rows.append(refs)
    return rows


def current_markdown(repository: pathlib.Path) -> list[pathlib.Path]:
    result = subprocess.run(
        [
            "git",
            "-C",
            str(repository),
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
            "--",
            "*.md",
        ],
        check=True,
        capture_output=True,
    )
    paths = {
        repository / raw.decode()
        for raw in result.stdout.split(b"\0")
        if raw
    }
    return sorted(path for path in paths if path.is_file())


def self_test() -> None:
    sample = (
        "`/charly-internals:strict-policy` "
        "/charly-ollama:not-a-real-skill "
        "http://host/charly-ollama:11434/api "
        "http://host/charly-jupyter:8888/lab"
    )
    assert SKILL_REF.findall(sample) == [
        ("internals", "strict-policy"),
        ("ollama", "not-a-real-skill"),
    ]
    assert not SKILL_REF.findall("/charly-internals:strict-policy_bad")

    with tempfile.TemporaryDirectory() as temporary:
        repository = pathlib.Path(temporary)
        subprocess.run(["git", "-C", temporary, "init", "-q"], check=True)
        (repository / "keep.md").write_text("keep\n")
        (repository / "old.md").write_text("old\n")
        subprocess.run(
            ["git", "-C", temporary, "add", "keep.md", "old.md"], check=True
        )
        (repository / "old.md").unlink()
        (repository / "new.md").write_text("new\n")
        names = {path.name for path in current_markdown(repository)}
        assert names == {"keep.md", "new.md"}


def validate_validator_bootstrap(root: pathlib.Path, errors: list[str]) -> None:
    """Fail before a validator spawn when tracked policy gitlinks are absent."""
    validator = root / "plugins/internals/agents/pr-validator.md"
    if not validator.is_file():
        errors.append(
            "validator bootstrap is unready: initialize recursive submodules with "
            "task agent:prepare-validator-worktree before spawning a validator"
        )


def validate_codex_r0_hooks(root: pathlib.Path, errors: list[str]) -> None:
    """Keep the repository-level Codex R0 guardrail present and executable."""
    hook_path = root / ".codex/hooks.json"
    hook_script = root / "scripts/codex-r0-hook.py"
    if not hook_path.is_file():
        errors.append("Codex R0 hook configuration is missing")
        return
    if not hook_script.is_file():
        errors.append("Codex R0 hook script is missing")
        return
    try:
        hooks = json.loads(hook_path.read_text()).get("hooks", {})
    except json.JSONDecodeError as error:
        errors.append(f"Codex R0 hook configuration is invalid JSON: {error}")
        return
    for event in ("SessionStart", "PreToolUse"):
        entries = hooks.get(event, [])
        if not any(
            "scripts/codex-r0-hook.py" in hook.get("command", "")
            for entry in entries
            for hook in entry.get("hooks", [])
        ):
            errors.append(f"Codex R0 hook configuration lacks {event} coverage")

    def invoke(event: str) -> dict[str, object]:
        result = subprocess.run(
            [sys.executable, str(hook_script)],
            input=json.dumps({"hook_event_name": event}),
            text=True,
            capture_output=True,
            check=True,
        )
        return json.loads(result.stdout)

    try:
        started = invoke("SessionStart")
        context = started["hookSpecificOutput"]["additionalContext"]
        if "R0" not in context:
            errors.append("Codex SessionStart hook does not provide R0 admission context")
        pre_tool = invoke("PreToolUse")
        if "R0" not in pre_tool.get("systemMessage", ""):
            errors.append("Codex PreToolUse hook does not provide an R0 warning")
    except (subprocess.CalledProcessError, json.JSONDecodeError, KeyError, TypeError) as error:
        errors.append(f"Codex R0 hook is not executable: {error}")


def main() -> int:
    # The canonical validation command owns its meta-test. Keeping this inside the validator avoids
    # ad-hoc gate assemblers guessing a separate test filename and silently omitting parser coverage.
    self_test()
    if sys.argv[1:] == ["--self-test"]:
        print("agent configuration validator self-test passed")
        return 0
    claude_path = ROOT / "CLAUDE.md"
    codex_path = ROOT / "AGENTS.md"
    claude = claude_path.read_text()
    codex = codex_path.read_text()
    errors: list[str] = []
    validate_validator_bootstrap(ROOT, errors)
    validate_codex_r0_hooks(ROOT, errors)

    claude_rows = dispatcher(claude_path)
    codex_rows = dispatcher(codex_path)
    if claude_rows != codex_rows:
        limit = max(len(claude_rows), len(codex_rows))
        for index in range(limit):
            left = claude_rows[index] if index < len(claude_rows) else None
            right = codex_rows[index] if index < len(codex_rows) else None
            if left != right:
                errors.append(f"dispatcher row {index + 1}: Claude={left} Codex={right}")

    mandatory = (
        "Risk Driven Development (RDD)",
        "Agent Driven Evaluation (ADE)",
        "Schema Driven Design (SDD)",
        "R1",
        "R2",
        "R3",
        "R4",
        "R5",
        "R6",
        "R7",
        "R8",
        "R9",
        "R10",
        "Acceptance checklist",
        "AI Attribution",
        "pr-validator",
        "root-cause-analyzer",
    )
    for term in mandatory:
        if term.lower() not in claude.lower():
            errors.append(f"CLAUDE.md is missing mandatory policy marker {term!r}")
        if term.lower() not in codex.lower():
            errors.append(f"AGENTS.md is missing mandatory policy marker {term!r}")

    forbidden = (
        "Codex does not read `AGENTS.md`",
        "Codex adapter",
        "canonical project rulebook is `CLAUDE.md`",
    )
    for phrase in forbidden:
        if phrase.lower() in codex.lower():
            errors.append(f"AGENTS.md contains obsolete delegation text: {phrase!r}")

    plugins_root = ROOT / "plugins"
    known = {
        (path.parts[-4], path.parts[-2])
        for path in plugins_root.glob("*/skills/*/SKILL.md")
    }
    known.update(
        (path.parts[-3], path.stem)
        for path in plugins_root.glob("*/agents/*.md")
    )
    known_plugins = {plugin for plugin, _ in known}
    repositories = [ROOT, plugins_root]
    repositories.extend(
        sorted(path for path in (ROOT / "box").glob("*") if (path / ".git").exists())
    )
    for repository in repositories:
        for path in current_markdown(repository):
            relative = path.relative_to(repository)
            if "CHANGELOG" in relative.parts:
                continue
            for plugin, name in SKILL_REF.findall(path.read_text()):
                if plugin in known_plugins and (plugin, name) not in known:
                    errors.append(
                        f"{path.relative_to(ROOT)} references missing /charly-{plugin}:{name}"
                    )

    for harness in ("claude", "codex"):
        result = subprocess.run(
            [str(ROOT / "plugins/setup"), harness, "--check", "developer"],
            cwd=ROOT,
            capture_output=True,
            text=True,
        )
        if result.returncode:
            detail = (result.stderr or result.stdout).strip()
            errors.append(f"{harness} project is not in full developer mode: {detail}")

    codex_config = tomllib.loads((ROOT / ".codex/config.toml").read_text())
    if "sandbox_mode" in codex_config or "sandbox_workspace_write" in codex_config:
        errors.append("Codex project config must not use legacy sandbox settings")
    if codex_config.get("default_permissions") != ":danger-full-access":
        errors.append("Codex project permissions must request full Charly developer access")
    if codex_config.get("approval_policy") != "on-request":
        errors.append("Codex project sandbox must retain on-request approvals")

    if errors:
        print("agent configuration validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"validated {len(claude_rows)} equivalent R0 dispatcher rows and mandatory policy markers")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
