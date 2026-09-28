#!/usr/bin/env python3
"""Check the single canonical agent rulebook and the Codex project
configuration."""

from __future__ import annotations

from collections.abc import Callable
import os
import pathlib
import re
import subprocess
import sys
import tomllib


ROOT = pathlib.Path(__file__).resolve().parents[1]
BARE_ROOT_GO_GATE = re.compile(r"(?m)^\s*(?:`)?go (?:test|vet|build) \./\.\.\.(?:`)?\s*$")
NONCANONICAL_ATTRIBUTION_TEMPLATE = re.compile(
    r"(?:Assisted-by: <Harness>\s+\(<Provider Full Model Name>;\s+<confidence>\)"
    r"|Assisted-by: <Harness>\s+<Full Model Name>\s+\(<confidence>\)"
    r"|Assisted-by: <Harness>\s+<Provider Full Model Name>\s+\(<tier>\))"
)
ATTRIBUTION_TEMPLATE = (
    "Assisted-by: <Harness> <Provider Full Model Name> (<confidence>)"
)
SHARED_POLICY_MARKERS = (
    "Candyboxing",
    "Risk Driven Development (RDD)",
    "Memory Hygiene",
    "Agent Driven Evaluation (ADE)",
    "Schema Driven Design (SDD)",
    "Prioritize Clean Architecture Above All Else",
    "The kernel/plugin boundary law",
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
    "Disposable-Only Autonomy",
    "Hard Cutover by Default",
    "Post-Execution Policies",
    "Acceptance checklist",
    "Agents, Workflows & Teams",
    "AI Attribution",
    "Key Rules",
    "Where things are documented",
    "pr-validator",
    "root-cause-analyzer",
    "scripts/bootstrap-charly.sh",
    "disposable: true",
    "direct push to `main`",
    "one phase",
    "Any rule violation forbids commit",
)
CONFIDENCE_TIERS = (
    "fully tested and validated",
    "analysed on a live system",
    "documentation reviewed",
    "syntax check only",
    "theoretical suggestion",
)
# Harness names and config identifiers a neutral rulebook must never embed.
# AGENTS.md is the single harness-neutral policy surface: per-harness
# primitives (plugin markets/packages/catalogs, config paths, sub-agent
# registries, TODO ledgers) belong to the harness-specific skills — the
# per-harness install table lives in the marketplace README, the primitives in
# `/charly-internals:agents`. Every marker here is an unambiguous substring:
# `Claude` covers `Claude Code`/`CLAUDE.md`, `Kimi` covers `Kimi Code`, `Codex`
# covers the Codex harness and its config. `pi` alone is a bare two-letter word
# (it would match `api`, `pipeline`, `capabilities`, …) so it is matched with
# word boundaries via a pattern.
FORBIDDEN_GENERIC_RULEBOOK_MARKERS = (
    "Claude",
    "Codex",
    "Kimi",
    "opencode",
    "reasonix",
    "cursor",
    "CODEX_HOME",
    ".codex",
    "~/.kimi-code",
)
FORBIDDEN_GENERIC_RULEBOOK_PATTERNS = (re.compile(r"\bpi\b"),)


def dispatcher(path: pathlib.Path) -> list[tuple[str, ...]]:
    skill_ref = re.compile(
        r"/charly-([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)(?![A-Za-z0-9_-])"
    )
    text = path.read_text()
    section = text.split("## Skill Dispatcher", 1)[1].split("Full index:", 1)[0]
    rows: list[tuple[str, ...]] = []
    for line in section.splitlines():
        if not line.startswith("|"):
            continue
        refs = tuple(
            f"/charly-{plugin}:{name}" for plugin, name in skill_ref.findall(line)
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


def rulebook_contract_errors(text: str) -> list[str]:
    """Return semantic contract drift in the single canonical rulebook."""
    errors: list[str] = []
    for marker in SHARED_POLICY_MARKERS:
        if marker.lower() not in text.lower():
            errors.append(f"AGENTS.md is missing shared policy marker {marker!r}")
    if ATTRIBUTION_TEMPLATE not in text:
        errors.append("AGENTS.md is missing the canonical attribution template")
    for tier in CONFIDENCE_TIERS:
        if tier not in text:
            errors.append(f"AGENTS.md is missing confidence tier {tier!r}")
    for marker in FORBIDDEN_GENERIC_RULEBOOK_MARKERS:
        if marker in text:
            errors.append(
                f"AGENTS.md contains harness-specific policy marker {marker!r}"
            )
    for pattern in FORBIDDEN_GENERIC_RULEBOOK_PATTERNS:
        match = pattern.search(text)
        if match:
            errors.append(
                "AGENTS.md contains harness-specific policy marker "
                f"{match.group(0)!r}"
            )
    return errors


def attribution_contract_errors(
    label: str, text: str, *, require_canonical: bool = False
) -> list[str]:
    """Return current-policy attribution-shape errors for one document."""
    errors: list[str] = []
    if NONCANONICAL_ATTRIBUTION_TEMPLATE.search(text):
        errors.append(f"{label} contains a noncanonical attribution placeholder")
    if require_canonical and ATTRIBUTION_TEMPLATE not in text:
        errors.append(f"{label} is missing the canonical attribution template")
    return errors


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


def validate_core_go_gate(root: pathlib.Path, errors: list[str]) -> None:
    """Require the executable, module-aware core Go command contract.

    The build gate is `scripts/bootstrap-charly.sh` — the ONE non-charly
    entrypoint (the build that PRODUCES the binary cannot itself be a charly
    task). Repository maintenance is the `kind:task` surface in charly.yml, run
    via `charly task <name>`. There is no Taskfile any more.
    """
    buildfile = root / "scripts" / "bootstrap-charly.sh"
    try:
        build_text = buildfile.read_text()
    except OSError as error:
        errors.append(f"core Go gate build script is unreadable: {error}")
        return
    required_build_terms = (
        "main.BuildCalVer",
        "-o ../bin/.charly.next",
        "mv bin/.charly.next bin/charly",
        "pluginsgen",
    )
    for term in required_build_terms:
        if term not in build_text:
            errors.append(f"core Go build gate lacks required content: {term!r}")
    if not os.access(buildfile, os.X_OK):
        errors.append("core Go build script scripts/bootstrap-charly.sh is not executable")

    # The task surface: charly.yml must declare the maintenance tasks as
    # kind:task entities (replacing the deleted taskfiles/).
    manifest = root / "charly.yml"
    try:
        manifest_text = manifest.read_text()
    except OSError as error:
        errors.append(f"core Go gate charly.yml is unreadable: {error}")
        return
    if "task:" not in manifest_text:
        errors.append("core Go gate charly.yml declares no kind:task entities")

    # The build gate the rulebook names must be the bootstrap script.
    name = "AGENTS.md"
    try:
        policy_text = (root / name).read_text()
    except OSError as error:
        errors.append(f"core Go gate policy is unreadable: {error}")
    else:
        if "scripts/bootstrap-charly.sh" not in policy_text:
            errors.append(f"{name} does not name the core Go build gate script")
        if BARE_ROOT_GO_GATE.search(policy_text):
            errors.append(f"{name} contains a bare superproject Go ./... gate")


def self_test() -> None:
    records = b"keep.md\0old.md\0new.md\0"
    names = current_markdown_names(records, {"keep.md", "new.md"}.__contains__)
    assert names == ["keep.md", "new.md"]
    assert BARE_ROOT_GO_GATE.search("go test ./...\n")
    assert not BARE_ROOT_GO_GATE.search("go test ./..\n")
    assert not BARE_ROOT_GO_GATE.search("cd charly && go test ./...\n")

    contract_fixture = "\n".join(
        (*SHARED_POLICY_MARKERS, ATTRIBUTION_TEMPLATE, *CONFIDENCE_TIERS)
    )
    assert not rulebook_contract_errors(contract_fixture)

    wrong_template = contract_fixture.replace(
        ATTRIBUTION_TEMPLATE,
        "Assisted-by: <Harness> (<Provider Full Model Name>; <confidence>)",
    )
    template_errors = rulebook_contract_errors(wrong_template)
    assert any("canonical attribution template" in error for error in template_errors)

    missing_tier = contract_fixture.replace("theoretical suggestion", "")
    tier_errors = rulebook_contract_errors(missing_tier)
    assert any("confidence tier" in error for error in tier_errors)

    missing_policy = contract_fixture.replace("Disposable-Only Autonomy", "")
    policy_errors = rulebook_contract_errors(missing_policy)
    assert any("shared policy marker" in error for error in policy_errors)

    branded_markers = (
        "Claude Code",
        "Codex",
        "Kimi Code",
        "opencode",
        "reasonix",
        "cursor",
        "CODEX_HOME=/tmp/x",
        ".codex/config.toml",
        "~/.kimi-code/config.toml",
    )
    for branded_marker in branded_markers:
        branded_errors = rulebook_contract_errors(
            f"{contract_fixture}\n{branded_marker}"
        )
        assert any(
            "harness-specific policy marker" in error for error in branded_errors
        ), branded_marker
    # `pi` is word-boundary matched, so substrings of ordinary words are safe.
    assert any(
        "harness-specific policy marker" in error
        for error in rulebook_contract_errors(f"{contract_fixture}\nthe pi package")
    )
    assert not any(
        "harness-specific policy marker" in error
        for error in rulebook_contract_errors(
            f"{contract_fixture}\napi pipeline capabilities"
        )
    )

    assert attribution_contract_errors(
        "fixture",
        "Assisted-by: <Harness> <Full Model Name> (<confidence>)",
    )
    assert attribution_contract_errors(
        "fixture",
        "Assisted-by: <Harness> <Provider Full Model Name> (<tier>)",
    )
    assert attribution_contract_errors(
        "fixture",
        "Assisted-by: <Harness> (<Provider Full Model Name>; <confidence>)",
    )
    assert not attribution_contract_errors(
        "fixture", ATTRIBUTION_TEMPLATE, require_canonical=True
    )


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
    # ad-hoc gate assemblers guessing a separate test filename and silently omitting validator coverage.
    self_test()
    if sys.argv[1:] == ["--self-test"]:
        print("agent configuration validator self-test passed")
        return 0
    rulebook_path = ROOT / "AGENTS.md"
    rulebook = rulebook_path.read_text()
    errors: list[str] = []
    validate_codex_project_agents(ROOT, errors)

    dispatcher_rows = dispatcher(rulebook_path)
    errors.extend(rulebook_contract_errors(rulebook))

    repositories = [ROOT]
    repositories.extend(
        sorted(path for path in (ROOT / "box").glob("*") if (path / ".git").exists())
    )
    for repository in repositories:
        for path in current_markdown(repository):
            relative = path.relative_to(repository)
            if "CHANGELOG" in relative.parts:
                continue
            text = path.read_text()
            errors.extend(
                attribution_contract_errors(str(path.relative_to(ROOT)), text)
            )

    validate_core_go_gate(ROOT, errors)

    if errors:
        print("agent configuration validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(
        f"validated {len(dispatcher_rows)} R0 dispatcher rows, "
        "shared policy contract, and attribution templates"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
