# Program: Pi → Charly Integration

**North-star:** Pi sessions on the charly repository follow the same R0–R10
discipline as Claude Code sessions, enforced mechanically where possible and
guided by prompt injection where it is not. The agent loads the right skill
before the first tool call of every task, and produces PR bodies that pass the
`pr-validator` on the first submission.

## Current state (2026-08-20)

### What works today

| Surface | Mechanism | Detail |
|---|---|---|
| Mechanical gates (force-push, direct-main push, commit bypass, aliases) | `.pi/extensions/charly-gates.ts` intercepts `tool_call` for bash, runs `.claude/hooks/pre-commit-gate.sh` + `pre-push-gate.sh` via stdin | Exit 2 → `{ block: true }`. Guards mechanics only, per AGENTS.md "Hooks" doctrine. |
| Rulebook context | Pi auto-loads `AGENTS.md` and `CLAUDE.md` as context files (both exist at repo root) | Loaded in startup header, included in system prompt. Survives `/reload`. |
| Skill discovery | Pi auto-discovers `~/.agents/skills/` and `.agents/skills/` (project) | 150+ skills visible via `<skill>` tags in system prompt. Agent must `read` SKILL.md to load content. |
| Project packages | `settings.json` pins pi-mcp-adapter, pi-subagents, pi-plan-mode, pi-memory, pi-ollama-cloud, rpiv-todo | Installed automatically on project trust. |
| git-flow test suite | `gate_test.py` covers commit + push gate edges | Run via `python3 -B gate_test.py` from `.claude/hooks/`. |

### What Pi has that Claude Code does not

- **`before_agent_start` with `systemPromptOptions`** — Pi exposes `contextFiles`, `skills`, `selectedTools`, `promptGuidelines`, `appendSystemPrompt`, `cwd` as structured data. An extension can inject rules every turn, surviving compaction.
- **`pi.registerTool()`** — custom tools with `promptSnippet` and `promptGuidelines` metadata. Dynamic tool loading (search+activate).
- **`pi.registerCommand()`** — custom slash commands.
- **Prompt templates** — `.pi/prompts/*.md` expand on `/name`.
- **`pi-subagents`** — `runs.run()` accepts `skill:` parameter to make a skill available to a child agent. The child then sees the skill in its system prompt.
- **Dynamic tool loading** — `search_tools` pattern: register many tools, keep only a few active, activate on demand.

### What Claude Code has that Pi does not

| Surface | Claude Code | Pi | Impact |
|---|---|---|---|
| `Skill` tool | Built-in `Skill` tool loads SKILL.md content | No equivalent — agent must use `read` | Agent must manually read SKILL.md files; no structured load |
| `EnterWorktree` | Built-in `EnterWorktree` tool creates worktree + submodule init + binary build | No equivalent | Worktree management is manual |
| `PreToolUse` hooks | `.claude/hooks/*.sh` wired via `.claude/settings.json` | Replaced by `tool_call` extension (charly-gates.ts) | ✅ Equivalent |
| Agent teams | `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` has built-in orchestrator/teammate | pi-subagents package provides equivalent | ✅ Equivalent via pi-subagents |

### Root cause of the PR #359 validator churn

The validator caught 4 issues across 3+ rounds. Each round was a different failure mode:

1. **No evidence in PR body** → missing `## How tested`, no pasted output, no `*Assisted-by:*` footer
2. **Missing bed gate** → `charly check box docs-site-app` not run for the docs-site config change
3. **Wrong attribution tier** → claimed `fully tested and validated` without the bed gate
4. **R2 deferred split** → sdk+spec bumps deferred without naming a thematic batch
5. **Missing rule-compliance section** → no `## Project-rulebook rule-compliance` table

The common thread: **the agent had all the rules in context files (AGENTS.md/CLAUDE.md) but did not follow them.** The rules were in the context but not in the system prompt, and after compaction or across turns, the model lost track of specific requirements like "every PR body must have a rule-compliance section."

## Implementation plan

### Layer 1: System prompt injection (extension)

**What:** Extend `charly-gates.ts` to inject condensed R0–R10 rules, the skill
dispatcher table, and PR body requirements into the system prompt every turn
via `before_agent_start`.

**Why this works:** Pi's `before_agent_start` fires every turn, including after
compaction. The injected rules survive compaction because they are re-injected
before the next LLM call. The `claude-rules.ts` example in pi's example
extensions shows this exact pattern for `.claude/rules/*.md` files.

**How:**

```typescript
// Inside charly-gates.ts, add to the default export function:

pi.on("before_agent_start", async (event) => {
  const { systemPrompt, systemPromptOptions } = event;

  // systemPromptOptions.contextFiles contains the loaded AGENTS.md/CLAUDE.md paths
  // systemPromptOptions.skills contains the loaded skill descriptions
  // These are already in the system prompt via pi's built-in loading.
  // We add the condensed rules that the model tends to forget.

  const rules = `## Charly Engineering Rules

### R0 — Skills First
Before the first tool call of every task, read the SKILL.md files whose
trigger column matches the task. The dispatcher table is in AGENTS.md.

### R1 — RCA Every Anomaly
Every failure, warning, or doc-vs-reality divergence triggers
/charly-internals:root-cause-analyzer before any remediation.

### R2 — Finish the Cutover
Every issue surfaced during an active cutover is fixed. No "pre-existing",
"out of scope", "follow-up PR" classifications.

### R3 — No Duplication
One canonical implementation owns each behavior.

### R4 — No Workarounds
No sleeps, blind retries, manual infrastructure commands.

### R4a — Fix the Product First
Documentation never routes around a defect. Fix the code before the prose.

### R5 — Delete Legacy Completely
Hard cutover removes the old path and every stale reference in one commit.

### R6 — Git Safety
Check git status before destructive actions. No force-push, pushed-history
rewrite, hook bypass, or direct push to main.

### R7 — Prove Behavior, Not Compilation
A green compile proves nothing. Run the changed path live.

### R8 — Preserve Emitted Artifacts
Validate labels, plans, configs, schemas at their actual boundary.

### R9 — Binary Equals Source
Build with task build:binary, invoke through bin/, verify version.

### R10 — Fresh Disposable Proof
Verify on targets explicitly marked disposable: true only. Fresh rebuild
from the final committed tree.

### PR Body Requirements
Every PR body must contain:
1. ## Summary — what changed and why
2. ## How tested — pasted command + output for every verification step
3. ## Project-rulebook rule-compliance — table with every applicable rule
4. ## Change Classification — change class, R10 gate, attribution tier
5. *Assisted-by: ...* — italicized footer at the end

### Attribution Tiers
- fully tested and validated: every runtime standard + fresh-rebuild R10
- analysed on a live system: changed path ran live, but full R10 incomplete
- documentation reviewed: docs-only (forbidden if code/config changed)
- syntax check only: compile/unit/dry-run only — do not commit
- theoretical suggestion: no validation — never ship

### Validator Verdict Discipline
Every validator BLOCK must be read in full and ALL listed issues fixed
before the next push. A partial fix that addresses only one of several
findings is a defective cycle.`;

  return {
    systemPrompt: systemPrompt + "\n\n" + rules,
  };
});
```

**Key design decisions:**
- Injected every turn, not just at session start → survives compaction
- Does NOT duplicate the full dispatcher table (that's in AGENTS.md, already loaded as context) → saves tokens
- Adds only the condensed rules that the model tends to forget mid-session → targeted, not verbose
- Uses `systemPromptOptions` to inspect what's loaded (context files, skills) but does not duplicate it

**Trade-off:** System prompt grows by ~2000 chars per turn. This is the cost of
surviving compaction. The alternative (keeping rules only in context files)
lost information after compaction, causing the validator churn we saw.

### Layer 2: Custom tool for skill loading

**What:** Register a `charly_load_skills` tool that accepts trigger keywords
and returns the full content of matching SKILL.md files.

**Why this works:** Pi's `<skill>` tags in the system prompt advertise which
skills are available, but the agent must `read` the SKILL.md to get the full
content. A custom tool that reads multiple SKILL.md files in one call is more
efficient and reliable than the agent manually reading each one.

**How:**

```typescript
pi.registerTool({
  name: "charly_load_skills",
  label: "Load Charly Skills",
  description: "Load the full content of SKILL.md files matching the given trigger keywords. Call this at the start of every task after consulting the skill dispatcher table in AGENTS.md.",
  promptSnippet: "Load skill documentation matching the current task",
  promptGuidelines: [
    "Use charly_load_skills at the START of every task to load the skills the dispatcher selects.",
    "Pass the trigger keywords from the user's request or AGENTS.md dispatcher.",
    "The tool returns the full SKILL.md content for every matching skill."
  ],
  parameters: Type.Object({
    triggers: Type.Array(Type.String(), {
      description: "Keywords from the user's task that match the skill dispatcher trigger column",
    }),
  }),
  async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
    // Parse the dispatcher table from AGENTS.md
    // This is a generated table with | Trigger | Skill to load |
    // We parse the Markdown table to extract trigger→skill mappings.

    const agentsMd = join(ctx.cwd, "AGENTS.md");
    const content = await readFile(agentsMd, "utf8");
    const dispatcher = parseDispatcherTable(content);

    // Find matching skills
    const matched = new Set<string>();
    for (const trigger of params.triggers) {
      const tl = trigger.toLowerCase();
      for (const [pattern, skillPath] of dispatcher) {
        if (pattern.toLowerCase().includes(tl)) {
          matched.add(skillPath);
        }
      }
    }

    // If no match, try the full index
    if (matched.size === 0) {
      // Fall back to the marketplace README for full index
    }

    // Read each matching SKILL.md
    const results: string[] = [];
    for (const skillPath of matched) {
      const fullPath = join(ctx.cwd, skillPath);
      try {
        const content = await readFile(fullPath, "utf8");
        results.push(`=== ${skillPath} ===\n${content}`);
      } catch {
        // Skip missing skills
      }
    }

    return {
      content: [{
        type: "text",
        text: results.length > 0
          ? `Loaded ${results.length} skill(s):\n\n${results.join("\n\n")}`
          : `No skills matched triggers: ${params.triggers.join(", ")}`,
      }],
      details: { matched: Array.from(matched), count: results.length },
    };
  },
});
```

**Key design decisions:**
- The tool **parses the dispatcher table** from AGENTS.md at runtime — no
  separate mapping file to maintain. The dispatcher is the single source of truth.
- Returns full SKILL.md content so the agent has the complete procedure without
  additional `read` calls.
- Added to `promptGuidelines` so the system prompt instructs the model to use
  it at the start of every task (R0 enforcement via guidance).

**Trade-off:** Parsing the Markdown table at runtime is fragile. Alternative:
generate a JSON mapping alongside the dispatcher (see Layer 4). For now, the
Markdown table is stable and generated, so parsing is reliable.

### Layer 3: Prompt templates

**What:** Create `.pi/prompts/` with templates for common workflows.

**Why this works:** Pi prompt templates expand on `/name` in the editor. They
provide structured starting points that enforce the correct format.

**How (retired 2026-243):** the in-repo `.pi/prompts/` templates were removed in the
harness mirror cleanup (matching the umbrella's #61 slim). Structured prompting for
cutovers, PR bodies, and review/verification is now provided by the corpus agents
(`charly-internals: pr-validator` / `check-bed-runner` / `root-cause-analyzer`, loaded
per the R0 dispatcher) and the standing system-prompt + Attribution-tier discipline
(`AGENTS.md`).

### Layer 4: Machine-readable skill dispatcher

**What:** Generate a `skill_triggers.json` file alongside the existing
dispatcher table in AGENTS.md.

**Why this works:** The charly marketplace generate pipeline already produces
the dispatcher table. Extending it to emit a JSON file is the single source of
truth approach — the JSON is generated from the same source as the Markdown.

**How:** Extend the `charly marketplace generate` command (owned by
`/charly-internals:skills`) to emit:

```json
{
  "skills": [
    {
      "trigger_patterns": ["charly box build", "Containerfile", "build"],
      "skills": [
        {"name": "build", "path": "build/skills/build/SKILL.md"},
        {"name": "generate", "path": "build/skills/generate/SKILL.md"}
      ]
    },
    ...
  ]
}
```

This file lives at `.agents/skill_triggers.json` in the repo root.

**Then** the `charly_load_skills` tool (Layer 2) reads this JSON instead of
parsing the Markdown table. This is the preferred approach once the JSON is
generated.

**Then** the `before_agent_start` extension (Layer 1) can read the JSON and
inject only matching skills into the system prompt based on the user's prompt
keywords — true per-demand skill loading.

### Layer 5: Sub-agent instruction update

**What:** Update AGENTS.md to include instructions for using pi-subagents
capabilities, and add a `.pi/subagents/` configuration.

**Why this works:** The AGENTS.md "Agents, Workflows & Teams" section describes
the orchestrator/teammate model for Claude Code. Pi has equivalent capabilities
via pi-subagents but with a different API. The instructions need to cover both.

**How:**

**5a. Add to AGENTS.md** a section describing pi-specific sub-agent usage:

```markdown
## Pi Sub-Agent Integration

In pi sessions, use the `subagent` tool for delegation:

- **Single delegate:** `subagent({ agent: "reviewer", task: "Review this diff for ...", skill: "charly-check:check" })`
  - The `skill` parameter makes the named skill available to the child.
  - The child sees the skill in its system prompt and can load it.
  - Use `context: "fresh"` for independent judgment (R10 validators).
  - Use `context: "fork"` when the child needs the parent's session history.

- **Parallel fan-out:** Use `workflowScript` with `runs.all()`:
  ```typescript
  subagent({
    workflowScript: `
      const results = await runs.all([
        { key: "bed1", agent: "worker", task: "Run bed X", skill: "charly-check:check" },
        { key: "bed2", agent: "worker", task: "Run bed Y", skill: "charly-check:check" },
      ]);
      return results.map(r => r.output);
    `,
  })
  ```

- **Fresh validator:** Use a fresh-context sub-agent for `pr-validator`:
  ```typescript
  subagent({
    agent: "reviewer",
    task: "Validate PR #N against R0-R10...",
    context: "fresh",
    skill: "charly-internals:agents,charly-internals:git-workflow,charly-check:check",
  })
  ```

- **Long-running beds:** Launch as async background:
  ```typescript
  subagent({
    agent: "worker",
    task: "Run charly check run <bed> and report results",
    async: true,
  })
  ```

**5b. Create `.pi/subagents/`** with agent definitions for charly-specific
roles (reviewer, validator, bed-runner, etc.) that pin the right skills.

### Layer 6: Pre-push PR body validation

**What:** Add a pre-push gate that checks the PR body for required sections.

**Why this works:** The `pre-push-gate.sh` already intercepts git push
commands. Adding a check for the PR body catches formatting issues before the
validator runs, saving the multi-round fix cycle.

**How:** Extend `pre-push-gate.sh` to, when pushing a `feat/` branch, check
that the associated PR (if any) has a body containing:

- `## Summary`
- `## How tested`
- `## Project-rulebook rule-compliance`
- `*Assisted-by:*`
- `Verdict:` (if a validator run is referenced)

The check uses `gh pr view --json body` and greps for the required sections.
If missing, the gate exits 2 (BLOCK) with a message listing what's missing.

**Trade-off:** This adds latency to every push to a `feat/` branch. The check
is fast (one `gh` API call) but requires `gh` to be authenticated. The gate
already requires `gh` for the aliases check.

### Layer 7: Worktree management tool

**What:** Register a `charly_worktree_create` / `charly_worktree_remove` tool.

**Why this works:** Pi has no `EnterWorktree` tool. The agent must manually
manage worktrees using `git worktree add` / `git worktree remove` via bash.
A custom tool encapsulates the lifecycle (create, init submodules, build
binary, record the path for cleanup).

**How:**

```typescript
pi.registerTool({
  name: "charly_worktree_create",
  label: "Create Worktree",
  description: "Create a linked worktree for a new feat branch, initialize submodules, and build the binary.",
  parameters: Type.Object({
    slug: Type.String({ description: "URL-safe kebab-case slug for the branch and worktree directory" }),
  }),
  async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
    const slug = params.slug;
    const worktreePath = join(ctx.cwd, ".claude", "worktrees", slug);
    const branch = `feat/${slug}`;

    // Step 1: Fetch and ff main
    await pi.exec("git", ["fetch", "origin", "--prune", "--tags"], { cwd: ctx.cwd });
    await pi.exec("git", ["merge", "--ff-only", "origin/main"], { cwd: ctx.cwd });

    // Step 2: Create worktree
    await pi.exec("git", ["worktree", "add", worktreePath, "-b", branch, "origin/main"], { cwd: ctx.cwd });

    // Step 3: Init submodules
    await pi.exec("git", ["submodule", "update", "--init", "--recursive"], { cwd: worktreePath });

    // Step 4: Build binary
    await pi.exec("task", ["build:binary"], { cwd: worktreePath });

    // Record for cleanup
    this._worktrees = this._worktrees || [];
    this._worktrees.push({ slug, path: worktreePath, branch });

    return {
      content: [{
        type: "text",
        text: `Worktree created at ${worktreePath} on branch ${branch}. Binary built.`,
      }],
      details: { path: worktreePath, branch },
    };
  },
});
```

**Key design decisions:**
- Encapsulates the full lifecycle: fetch → ff → create → submodules → build
- Records worktrees for cleanup (the agent can call `charly_worktree_remove`
  after the PR lands)
- Uses `pi.exec()` for git commands (pi's built-in shell execution)

## Decision record

| Decision | Choice | Rationale |
|---|---|---|
| R0 enforcement | System prompt injection + custom tool (Layer 1 + 2) | Mechanical blocking is fragile and risks false positives. Injection every turn keeps rules in the model's active context. The custom tool provides a reliable way to load skills. |
| Dispatcher parsing | Generate JSON from the same pipeline (Layer 4) | The Markdown table is already generated. Extending the generator is the single source of truth approach. Until then, the tool parses the Markdown table at runtime. |
| PR body formatting | Pre-push gate (Layer 6) | Catches errors before the validator runs. The gate already exists and runs on every push. Adding a `gh pr view` check is cheap. |
| Attribution model | `model-agnostic` is correct for pi | Pi is model-agnostic by design. The exact provider/model name is not exposed to extensions. The `AGENTS.md` AI Attribution section already documents this. |
| Worktree lifecycle | Custom tool (Layer 7) | Pi has no `EnterWorktree` equivalent. A tool with `promptGuidelines` instructs the agent to use it at the start of every cutover. |
| Sub-agent instructions | Update AGENTS.md + `.pi/subagents/` (Layer 5) | The orchestrator/teammate model is documented for Claude Code. pi-subagents has a different API. The instructions must cover both. |

## Summary of changes

| Layer | File | What changes |
|---|---|---|
| 1 | `.pi/extensions/charly-gates.ts` | Add `before_agent_start` handler that injects condensed R0–R10 + PR body rules |
| 2 | `.pi/extensions/charly-gates.ts` | Add `charly_load_skills` custom tool with `promptGuidelines` |
| 3 | `.pi/prompts/*.md` | Create 6 prompt templates |
| 4 | the marketplace's `build` family | Extend marketplace generate to emit `skill_triggers.json` |
| 4 | `.agents/skill_triggers.json` | Generated file (consumed by Layer 2) |
| 5 | `AGENTS.md` | Add pi-specific sub-agent instructions |
| 5 | `.pi/subagents/` | Agent definitions for charly roles |
| 6 | `.claude/hooks/pre-push-gate.sh` | Add PR body validation |
| 7 | `.pi/extensions/charly-gates.ts` | Add `charly_worktree_create` / `charly_worktree_remove` tools |

## Implementation order

1. **Layers 1 + 2 + 3** (extension + tools + templates) — immediate, no charly
   repo code changes needed. These are the highest impact per change.
2. **Layer 5** (sub-agent instructions) — update AGENTS.md and create subagent
   config. This is documentation + config, no code.
3. **Layer 6** (pre-push PR body validation) — extend existing gate script.
4. **Layer 7** (worktree tool) — extension change, no charly repo code.
5. **Layer 4** (machine-readable dispatcher) — requires changes to the `charly
   marketplace generate` pipeline (Go code in the standalone `opencharly/plugin-marketplace`
   repo since the Phase-4 de-submodule cutover, consumed here at a pinned ref). This is the only
   item that touches generator source code. All others are pi config + extension
   changes only.