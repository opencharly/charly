# Pi project config (`.pi/`)

Project-local configuration for [pi](https://pi.dev) agent sessions in this
repository. Pi auto-loads `AGENTS.md`/`CLAUDE.md` as context and auto-discovers
the skills under `.agents/skills/` (symlinks into `plugins/`), so no config is
needed for those. This directory exists to give pi the one thing it lacks
compared to the other harnesses: a **hooks system**.

## What's here

| Path | Purpose |
|---|---|
| `settings.json` | Registers the project extension. |
| `extensions/charly-gates.ts` | Pi's equivalent of the `.reasonix`/kimi `PreToolUse(Bash)` wiring of `.claude/hooks/pre-commit-gate.sh` + `pre-push-gate.sh`. Intercepts every `bash` tool call and blocks commands the gates reject (force-push, direct push to `main`, `--no-verify` commit bypass, untokenizable commits, new alias files). |

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
