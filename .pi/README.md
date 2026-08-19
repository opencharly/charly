# Pi project config (`.pi/`)

Project-local configuration for [pi](https://pi.dev) agent sessions in this
repository. Pi auto-loads `AGENTS.md`/`CLAUDE.md` as context and auto-discovers
the skills under `.agents/skills/` (symlinks into `plugins/`), so no config is
needed for those. This directory exists to give pi the one thing it lacks
compared to the other harnesses: a **hooks system**.

## What's here

| Path | Purpose |
|---|---|
| `settings.json` | Registers the project extension and the project pi packages. |
| `extensions/charly-gates.ts` | Pi's equivalent of the `.reasonix`/kimi `PreToolUse(Bash)` wiring of `.claude/hooks/pre-commit-gate.sh` + `pre-push-gate.sh`. Intercepts every `bash` tool call and blocks commands the gates reject (force-push, direct push to `main`, `--no-verify` commit bypass, untokenizable commits, new alias files). |

## Project packages

`settings.json` pins the following pi packages (installed automatically on
startup after the project is trusted):

| Package | Version | Purpose |
|---|---|---|
| `pi-mcp-adapter` | 2.26.1 | MCP (Model Context Protocol) adapter extension for Pi. |
| `pi-subagents` | 0.51.0 | Single-agent delegation and scripted multi-agent workflows. |
| `@narumitw/pi-plan-mode` | 0.49.3 | Codex-like read-only `/plan` collaboration mode. |
| `pi-lens` | 4.0.1 | Real-time code feedback — LSP, linters, formatters, type-checking, structural analysis. |
| `@juicesharp/rpiv-todo` | 2.6.2 | A todo list for the model, rendered as a live overlay that survives `/reload` and compaction. |
| `pi-memory` | 0.4.2 | Memory with qmd-powered semantic search across daily logs, long-term memory, and scratchpad. |
| `pi-ollama-cloud` | 0.9.0 | Ollama Cloud provider plugin (also installed at the user level). |

Versions are pinned for reproducibility; `pi update --extensions` skips pinned
packages. Move a package to a newer ref with `pi install npm:<pkg>@<new-ver>`.

> **Security:** pi packages run with full system access and their extensions
execute arbitrary code. Review a package's source before adding it to a shared
project config.

## Why this is needed

The project rulebook (`AGENTS.md` "Hooks") mandates that deterministic
git-workflow command mechanics — bypass flags, force-push, direct-main push,
untokenizable commit commands, forbidden alias forms — are enforced by hooks.
Claude Code, reasonix, and kimi wire `.claude/hooks/*.sh` for this. Pi has no
built-in hooks system, so this extension reproduces that wiring through pi's
`tool_call` event, running the exact same gate scripts.

The gates guard mechanics only. Attribution, change class, CHANGELOG coverage,
architecture, and R0–R10 proof are judged by the fresh `pr-validator` at merge,
never by the extension.

## Trust

Pi asks before trusting a project that contains project-local resources (like
this `.pi/`). Trust it once with `/trust` (interactive) or `--approve`/`-a`
(non-interactive) so the extension loads.
