# OpenCharly guidance for Codex

This is a thin Codex adapter. `CLAUDE.md` is the canonical rulebook and skill
dispatcher; do not mirror its rules here.

## Startup

1. Start at the superproject root and read the root `CLAUDE.md` completely. Run
   submodule Git operations from there with literal absolute `git -C <path>`
   commands; never root a worker inside a submodule.
2. Read the nearest per-directory `CLAUDE.md` signpost for every touched area.
3. Apply R0 before exploration or mutation. If a dispatched skill is not
   registered with Codex, read its on-disk `SKILL.md` completely; registration
   never determines whether a repository skill applies.
4. Treat Claude-specific tool names as roles and preserve their isolation,
   authorization, and proof requirements with the Codex equivalent. Stop if a
   mandatory capability has no equivalent; never weaken the gate.

## Codex tool mapping

- A Codex subagent in its own agent thread is the equivalent of a Claude Code
  teammate. Use a new thread when the owning skill requires fresh or independent
  context; a PR validator must not inherit the author conversation.
- Before delegation or R10, confirm the parent Codex session can pass its tools
  and approvals to child threads, write tool caches under an authorized temporary
  root, reach GitHub through the approval path, and remain rooted at the
  superproject. Repository trust alone proves none of those capabilities.
- Give validators the normal repository, shell, GitHub, build, and disposable-bed
  capabilities, but no bypass authority. They read policy and matching skills
  from protected `main`, derive the change class independently, and run the gate
  that class requires. A missing capability is a blocker, not a partial PASS.
- A fresh validator independently executes the complete gate for its derived
  change class and must have a persistent-enough thread for every required bed.
  If it cannot personally complete a long bed with the inherited tools and
  permissions, validation is blocked and restarts in a new capable thread; the
  authoring orchestrator’s proof is never a substitute.
- For concurrent cutovers, follow `/charly-internals:agents` “Per-worktree
  binaries”: build the stamped worktree-local binary with `task build:binary`
  and invoke it through the worktree’s `bin` directory. Never install a shared
  binary from a worktree.
- Before changing a cross-repository GitHub convention, inventory the
  organization `.github` repository and every protected repository. Prefer one
  organization-owned template, reusable workflow, ruleset, or reconciler over
  repository-local copies; verify the resulting live settings across the full
  repository roster.
- On any failure, warning, anomaly, documentation divergence, or rule violation,
  stop remediation until the fresh root-cause-analyzer role has completed the
  R1 analysis required by protected-main policy.

## Attribution

Preserve the repository’s existing `Assisted-by:` trailer/footer shape and use
the runtime’s full provider model name. For this session that is
`Assisted-by: Codex OpenAI GPT-5.6 Sol (<confidence>)`. A 100% human-authored
commit has no AI trailer and remains valid.

## Pull requests

Write PR bodies as readable GitHub-flavored Markdown: use headings, short
paragraphs, lists or tables for structured facts, and fenced blocks for
verbatim validation evidence. Preserve the italicized `Assisted-by:` footer and
submit multiline bodies with `gh pr create --body-file`; never encode newlines
as escape sequences or publish an unstructured wall of text.
