#!/usr/bin/env python3
"""Check semantic parity between the independent Claude and Codex rulebooks."""

from __future__ import annotations

import pathlib
import re
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


def validate_codex_project_agents(root: pathlib.Path, errors: list[str]) -> None:
    """Validate the project-scoped Codex configuration and validator role."""
    config_path = root / ".codex/config.toml"
    validator_path = root / ".codex/agents/pr-validator.toml"
    try:
        config = tomllib.loads(config_path.read_text())
    except (OSError, tomllib.TOMLDecodeError) as error:
        errors.append(f"Codex project configuration is unreadable: {error}")
        return
    # Sandbox and approval posture is an operator choice. This checker rejects
    # only configurations that cannot provide the project validator role, not
    # configurations that are merely broad or risky.
    if not isinstance(config, dict):
        errors.append("Codex project configuration must decode to a TOML table")
    workspace = config.get("sandbox_workspace_write")
    if workspace is not None and not isinstance(workspace, dict):
        errors.append("Codex workspace-write configuration must be a TOML table")
    try:
        validator = tomllib.loads(validator_path.read_text())
    except (OSError, tomllib.TOMLDecodeError) as error:
        errors.append(f"Codex pr-validator role is unreadable: {error}")
        return
    for key in ("name", "description", "developer_instructions"):
        if not validator.get(key):
            errors.append(f"Codex pr-validator role lacks {key}")
    if validator.get("name") != "pr-validator":
        errors.append("Codex pr-validator role has the wrong name")
    instructions = validator.get("developer_instructions", "")
    required = (
        "full R10 gate",
        "independently decide whether the merge-time CalVer final-tree delta requires a",
        "denial is BLOCKED",
    )
    for phrase in required:
        if phrase not in instructions:
            errors.append(f"Codex pr-validator role lacks required instruction: {phrase}")


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
    validate_codex_project_agents(ROOT, errors)

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

    if errors:
        print("agent configuration validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"validated {len(claude_rows)} equivalent R0 dispatcher rows and mandatory policy markers")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
