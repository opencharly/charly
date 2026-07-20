#!/usr/bin/env python3
"""Check semantic parity between the independent Claude and Codex rulebooks and
the generated per-harness developer profiles (Claude, Codex, Kimi)."""

from __future__ import annotations

from collections.abc import Callable
import json
import os
import pathlib
import re
import subprocess
import sys
import tomllib


ROOT = pathlib.Path(__file__).resolve().parents[1]
SKILL_REF = re.compile(
    r"/charly-([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)(?![A-Za-z0-9_-])"
)
GITLINK_COMMIT = re.compile(r"^[0-9a-f]{40,64}$")
BARE_ROOT_GO_GATE = re.compile(r"(?m)^\s*(?:`)?go (?:test|vet|build) \./\.\.\.(?:`)?\s*$")
GitRunner = Callable[..., subprocess.CompletedProcess[str]]


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


def current_markdown_names(
    records: bytes, path_is_file: Callable[[str], bool]
) -> list[str]:
    """Decode `git ls-files -z` output without creating a fixture repository."""
    names = {record.decode() for record in records.split(b"\0") if record}
    return sorted(name for name in names if path_is_file(name))


def task_dry_plan_contains(stdout: str, stderr: str, command: str) -> bool:
    """Check Task's compiled plan regardless of its documented output stream."""
    return command in f"{stdout}\n{stderr}"


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
    names = current_markdown_names(
        result.stdout, lambda name: (repository / name).is_file()
    )
    return [repository / name for name in names]


def plugins_gitlink_commit(output: str) -> str | None:
    """Return the tracked plugins gitlink commit from `git ls-files --stage`."""
    entry, separator, path = output.strip().partition("\t")
    fields = entry.split()
    if (
        not separator
        or path != "plugins"
        or len(fields) != 3
        or fields[0] != "160000"
        or not GITLINK_COMMIT.fullmatch(fields[1])
    ):
        return None
    return fields[1]


def plugins_setup_prerequisite(
    root: pathlib.Path,
    *,
    run: GitRunner = subprocess.run,
    path_is_file: Callable[[pathlib.Path], bool] = pathlib.Path.is_file,
    path_is_executable: Callable[[pathlib.Path], bool] = lambda path: os.access(
        path, os.X_OK
    ),
) -> tuple[pathlib.Path | None, str | None]:
    """Require the checked-out plugins tree before using its developer checker."""
    plugins = root / "plugins"
    try:
        recorded = run(
            ["git", "-C", str(root), "ls-files", "--stage", "--", "plugins"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
    except (OSError, subprocess.CalledProcessError) as error:
        return None, f"cannot read the recorded plugins gitlink: {error}"
    expected = plugins_gitlink_commit(recorded)
    if expected is None:
        return None, "superproject does not record a valid plugins gitlink"
    try:
        toplevel = pathlib.Path(
            run(
                ["git", "-C", str(plugins), "rev-parse", "--show-toplevel"],
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()
        )
        superproject = pathlib.Path(
            run(
                [
                    "git",
                    "-C",
                    str(plugins),
                    "rev-parse",
                    "--show-superproject-working-tree",
                ],
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()
        )
    except (OSError, subprocess.CalledProcessError) as error:
        return None, f"plugins checkout is unavailable: {error}"
    if toplevel.resolve() != plugins.resolve() or superproject.resolve() != root.resolve():
        return None, (
            "plugins submodule checkout is absent or uninitialized; run "
            "git submodule update --init --recursive"
        )
    try:
        actual = run(
            ["git", "-C", str(plugins), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
    except (OSError, subprocess.CalledProcessError) as error:
        return None, f"plugins checkout is unavailable: {error}"
    if actual != expected:
        return None, (
            "plugins checkout does not match the recorded gitlink "
            f"(expected {expected}, found {actual or 'none'})"
        )
    setup = plugins / "setup"
    if not path_is_file(setup) or not path_is_executable(setup):
        return None, f"plugins developer checker is missing or not executable: {setup}"
    return setup, None


def validate_developer_profiles(root: pathlib.Path, errors: list[str]) -> None:
    """Run committed dual-harness developer-profile checks when available."""
    setup, prerequisite_error = plugins_setup_prerequisite(root)
    if prerequisite_error:
        errors.append(f"project developer profile cannot be checked: {prerequisite_error}")
        return
    assert setup is not None
    for harness in ("claude", "codex", "kimi"):
        try:
            result = subprocess.run(
                [str(setup), harness, "--check", "developer"],
                cwd=root,
                capture_output=True,
                text=True,
            )
        except OSError as error:
            errors.append(f"{harness} developer profile check could not start: {error}")
            continue
        if result.returncode:
            detail = (result.stderr or result.stdout).strip()
            errors.append(f"{harness} project is not in full developer mode: {detail}")


def validate_core_go_gate(root: pathlib.Path, errors: list[str]) -> None:
    """Require the executable, module-aware core Go command contract."""
    buildfile = root / "taskfiles" / "Build.yml"
    try:
        build_text = buildfile.read_text()
    except OSError as error:
        errors.append(f"core Go gate build file is unreadable: {error}")
        return
    required_build_terms = (
        "binary:",
        "main.BuildCalVer",
        "-o ../bin/charly",
    )
    for term in required_build_terms:
        if term not in build_text:
            errors.append(f"core Go build gate lacks required content: {term!r}")

    try:
        task_list = subprocess.run(
            ["task", "--list", "--json"],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError as error:
        errors.append(f"core Go gate cannot resolve Task includes: {error}")
        return
    if task_list.returncode:
        detail = (task_list.stderr or task_list.stdout).strip()
        errors.append(f"core Go gate cannot resolve Task includes: {detail}")
        return
    try:
        tasks = json.loads(task_list.stdout).get("tasks", [])
    except (json.JSONDecodeError, AttributeError) as error:
        errors.append(f"core Go gate returned invalid Task task list: {error}")
        return

    binary_task = next(
        (
            task
            for task in tasks
            if isinstance(task, dict) and task.get("name") == "build:binary"
        ),
        None,
    )
    expected_taskfile = root / "taskfiles" / "Build.yml"
    taskfile_path = (
        binary_task.get("location", {}).get("taskfile")
        if isinstance(binary_task, dict)
        else None
    )
    if not binary_task:
        errors.append("core Go gate lacks resolved build:binary task")
    elif not isinstance(taskfile_path, str):
        errors.append("core Go build task has no resolved taskfile location")
    elif pathlib.Path(taskfile_path).resolve() != expected_taskfile.resolve():
        errors.append(
            "core Go build task must be owned by taskfiles/Build.yml, "
            f"not {taskfile_path!r}"
        )
    else:
        dry_plan = subprocess.run(
            ["task", "--dry", "build:binary"],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
        )
        if dry_plan.returncode:
            detail = (dry_plan.stderr or dry_plan.stdout).strip()
            errors.append(f"core Go build task cannot compile: {detail}")
        elif not task_dry_plan_contains(
            dry_plan.stdout, dry_plan.stderr, "main.BuildCalVer"
        ):
            errors.append(
                "core Go build task lacks the CalVer stamp"
            )

    for name in ("CLAUDE.md", "AGENTS.md"):
        path = root / name
        try:
            text = path.read_text()
        except OSError as error:
            errors.append(f"core Go gate policy is unreadable: {error}")
            continue
        if "task build:binary" not in text:
            errors.append(f"{name} does not name the core Go build gate task")
        if BARE_ROOT_GO_GATE.search(text):
            errors.append(f"{name} contains a bare superproject Go ./... gate")


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
    records = b"keep.md\0old.md\0new.md\0"
    names = current_markdown_names(records, {"keep.md", "new.md"}.__contains__)
    assert names == ["keep.md", "new.md"]
    assert task_dry_plan_contains("", "go build -ldflags -X main.BuildCalVer", "main.BuildCalVer")
    assert not task_dry_plan_contains("task: no-op", "", "main.BuildCalVer")
    expected = "a" * 40

    def successful_git(*args: object, **kwargs: object) -> subprocess.CompletedProcess[str]:
        command = args[0]
        assert isinstance(command, list)
        output = f"160000 {expected} 0\tplugins\n"
        if command[-1] == "--show-toplevel":
            output = "/project/plugins\n"
        elif command[-1] == "--show-superproject-working-tree":
            output = "/project\n"
        elif command[-1] == "HEAD":
            output = f"{expected}\n"
        return subprocess.CompletedProcess(command, 0, output, "")

    setup, error = plugins_setup_prerequisite(
        pathlib.Path("/project"),
        run=successful_git,
        path_is_file=lambda path: path.name == "setup",
        path_is_executable=lambda path: path.name == "setup",
    )
    assert error is None and setup == pathlib.Path("/project/plugins/setup")

    def mismatched_git(*args: object, **kwargs: object) -> subprocess.CompletedProcess[str]:
        command = args[0]
        assert isinstance(command, list)
        output = f"160000 {expected} 0\tplugins\n"
        if command[-1] == "--show-toplevel":
            output = "/project/plugins\n"
        elif command[-1] == "--show-superproject-working-tree":
            output = "/project\n"
        elif command[-1] == "HEAD":
            output = f"{'b' * 40}\n"
        return subprocess.CompletedProcess(command, 0, output, "")

    _, error = plugins_setup_prerequisite(pathlib.Path("/project"), run=mismatched_git)
    assert error and "does not match" in error

    def uninitialized_git(*args: object, **kwargs: object) -> subprocess.CompletedProcess[str]:
        command = args[0]
        assert isinstance(command, list)
        output = f"160000 {expected} 0\tplugins\n"
        if command[-1] == "--show-toplevel":
            output = "/project\n"
        elif command[-1] == "--show-superproject-working-tree":
            output = "\n"
        return subprocess.CompletedProcess(command, 0, output, "")

    _, error = plugins_setup_prerequisite(
        pathlib.Path("/project"), run=uninitialized_git
    )
    assert error and "absent or uninitialized" in error
    _, error = plugins_setup_prerequisite(
        pathlib.Path("/project"),
        run=successful_git,
        path_is_file=lambda path: False,
    )
    assert error and "missing or not executable" in error

    def unavailable_git(*args: object, **kwargs: object) -> subprocess.CompletedProcess[str]:
        raise OSError("unavailable")

    _, error = plugins_setup_prerequisite(pathlib.Path("/project"), run=unavailable_git)
    assert error and "cannot read" in error
    assert BARE_ROOT_GO_GATE.search("go test ./...\n")
    assert not BARE_ROOT_GO_GATE.search("go test ./..\n")
    assert not BARE_ROOT_GO_GATE.search("cd charly && go test ./...\n")


def validate_codex_project_agents(root: pathlib.Path, errors: list[str]) -> None:
    """Validate the project-scoped Codex configuration and validator role."""
    config_path = root / ".codex/config.toml"
    validator_path = root / ".codex/agents/pr-validator.toml"
    validator_check_path = root / "scripts/validate-agent-config.py"
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
    try:
        validator_check = validator_check_path.read_text()
    except OSError as error:
        errors.append(f"Codex validator check is unreadable: {error}")
    else:
        forbidden_validator_setup = (
            ("temp" + "file", "host temporary storage"),
            ("Temporary" + "Directory(", "a temporary workspace"),
            ("mk" + "dtemp(", "a temporary workspace"),
            ("mk" + "stemp(", "a temporary workspace"),
        )
        for marker, description in forbidden_validator_setup:
            if marker in validator_check:
                errors.append(
                    "Codex validator check creates forbidden "
                    f"{description}: {marker!r}"
                )


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

    validate_developer_profiles(ROOT, errors)
    validate_core_go_gate(ROOT, errors)

    if errors:
        print("agent configuration validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"validated {len(claude_rows)} equivalent R0 dispatcher rows and mandatory policy markers")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
