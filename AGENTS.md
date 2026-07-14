# OpenCharly guidance for Codex

This file is the Codex adapter for OpenCharly. The canonical project rulebook is
`CLAUDE.md`; do not duplicate its dispatcher or rule bodies here.

## Startup contract

1. Work from the superproject root, including for changes in `plugins`, `sdk`,
   or another submodule. Drive submodule Git commands with a literal absolute
   `git -C <path>`; do not root a worker inside a submodule.
2. Read the root `CLAUDE.md` completely before repository work. When working
   below a directory that has its own `CLAUDE.md`, read that thin signpost too.
3. Apply `CLAUDE.md`'s Skill Dispatcher before source exploration or mutation.
   If a listed OpenCharly skill is not registered in the current Codex session,
   read its `SKILL.md` from `plugins/<plugin>/skills/<name>/SKILL.md` completely.
   A missing skill registration never makes the on-disk skill optional.
4. Treat Claude-specific tool names as role descriptions. Use the equivalent
   Codex capability while preserving the same isolation, fresh-context,
   verification, and authorization requirements. If no equivalent exists for a
   mandatory gate, stop and ask rather than weakening the gate.

## Codex-specific conventions

- Use a fresh Codex context for the independent `pr-validator` role. It must not
  be the context that authored the change and must follow
  `plugins/internals/agents/pr-validator.md` from protected `main`.
- Keep durable repository rules in `CLAUDE.md` or their owning skill. Update this
  adapter only when Codex needs different discovery or tool-mapping guidance.
- AI-authored commits use the model-aware attribution syntax defined by
  `CLAUDE.md`: `Assisted-by: <Harness> (<Provider Full Model Name>; <confidence>)`.
  Review-only AI is disclosed in the PR, not added to a human-authored commit.
  A 100% human-authored commit carries no AI trailer.
