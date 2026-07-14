# OpenCharly guidance for Codex

This is a thin Codex adapter. `CLAUDE.md` is the canonical rulebook and skill
dispatcher; do not mirror its rules here.

## Startup

1. Start at the superproject root. Before reading `CLAUDE.md`, load every
   matching skill already identifiable from the request or assigned role. A PR
   validator first reads `plugins/internals/agents/pr-validator.md`,
   `plugins/internals/skills/git-workflow/SKILL.md`, and
   `plugins/internals/skills/strict-policy/SKILL.md`; an RCA role first reads
   `plugins/internals/agents/root-cause-analyzer.md` and
   `plugins/internals/skills/strict-policy/SKILL.md`.
2. Read the root `CLAUDE.md` completely, then load every additional skill its
   dispatcher selects before exploration or mutation. Run submodule Git
   operations from the superproject with literal absolute `git -C <path>`
   commands; never root a worker inside a submodule.
3. Read the nearest per-directory `CLAUDE.md` signpost for every touched area.
4. If a dispatched skill is not
   registered with Codex, read its on-disk `SKILL.md` completely; registration
   never determines whether a repository skill applies.
5. Treat Claude-specific tool names as roles and preserve their isolation,
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

## Process integrity

- Create every AI-authored commit, including every merge commit, with the exact
  `Assisted-by:` trailer. Before the first push, verify its complete message,
  tree, and ordered parents.
- A fresh validator loads protected-`main` policy and every dispatched skill
  before inspecting the candidate change. Use validation commands that are
  read-only or self-cleaning and leave the candidate worktree unchanged. Any
  validator anomaly invalidates that run and requires RCA plus a new context.
- For validation scope, follow `/charly-check:check` "R10 gate by change
  class"; Codex adds no alternate gate or bed requirement.
- For submodule-pointer conflicts, follow `/charly-internals:git-workflow`
  "Gitlink ANCESTOR bump → `gh pr update-branch` flags CONFLICTING (recover
  locally)"; Codex adds no alternate conflict-recovery procedure.
