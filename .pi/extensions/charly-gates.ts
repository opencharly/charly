/**
 * charly-gates.ts — Pi extension that enforces OpenCharly's git-workflow
 * command mechanics AND injects engineering rules into the system prompt.
 *
 * ## Mechanical gates (tool_call interception)
 *
 * Pi has no hooks system (unlike Claude Code / reasonix / kimi). This
 * extension is the Pi equivalent of the `.reasonix`/kimi `PreToolUse(Bash)`
 * wiring of `.claude/hooks/pre-commit-gate.sh` and `pre-push-gate.sh`. It
 * intercepts every `bash` tool call and runs both gate scripts against the
 * command, blocking the call when a gate exits 2.
 *
 * The gates guard ONLY deterministic command mechanics (per the project
 * rulebook "Hooks" doctrine):
 *   - `git commit --no-verify` / `-n` / `core.hooksPath` bypass
 *   - untokenizable commit commands
 *   - configured Go lint failures for staged Go modules
 *   - new-or-grown `charly/*_aliases.go` files (ZERO-ALIASES gate)
 *   - `git push --force` / `--force-with-lease` / `-f`
 *   - a direct push to `main`
 *
 * Attribution identity/confidence, change class, CHANGELOG coverage,
 * architecture, and R0–R10 proof are judged once by the fresh pr-validator,
 * never here. Hooks guard mechanics; agents judge policy and evidence.
 *
 * ## System prompt injection (before_agent_start)
 *
 * Injects condensed R0–R10 rules, PR body requirements, and attribution
 * tiers into the system prompt every turn. This survives compaction because
 * it is re-injected before every LLM call.
 *
 * ## Custom tools
 *
 * - charly_load_skills: reads SKILL.md files matching trigger keywords
 * - charly_worktree_create: creates a linked worktree with submodules + binary
 * - charly_worktree_remove: removes a worktree and its branch
 *
 * The gate scripts self-gate on a fast path (they exit 0 for commands that
 * do not mention `git commit` / `git push`), so running them for every bash
 * call is cheap and matches the doctrine exactly.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { writeFile, unlink, access, readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Type } from "typebox";

/** Gate scripts, relative to the project root (ctx.cwd). */
const GATE_SCRIPTS = [
  ".claude/hooks/pre-commit-gate.sh",
  ".claude/hooks/pre-push-gate.sh",
];

/** The condensed engineering rules injected every turn. */
function buildRulesBlock(): string {
  return `## Charly Engineering Rules

### R0 — Skills First
Before the first tool call of every task, use \`charly_load_skills\` to load the
SKILL.md files whose trigger column matches the task. The dispatcher table is in
AGENTS.md. Load ALL matching skills before acting.

### R1 — RCA Every Anomaly
Every failure, warning, or doc-vs-reality divergence triggers the
root-cause-analyzer process before any remediation. Skills are living
documents: any code change that affects a skill, doc, comment, or memory
claim updates that document in the SAME change, and sweeps every sibling
carrying the same false claim.

### R2 — Finish the Cutover
Every issue surfaced during a task is fixed — pre-existing or not. No
"pre-existing", "unrelated", "out of scope", or "follow-up PR"
classifications. A blocking issue is fixed in the same commit; a genuinely
separable non-blocking issue joins its next thematic batch cutover
immediately — never parked.

### R2a — Use Subagents and Fabric Whenever Possible
Delegate heavy exploration, log archaeology, repo-wide greps, and
long-running verification to a subagent that returns only a concise verdict
+ evidence paths. Use fabric_exec to batch independent tool calls into one
program. The main agent plans, decides, and lands; the subagent digs. Never
burn the main context on raw logs or repeated diagnostics.

### R3 — No Duplication
One canonical implementation owns each behavior. Extract shared mechanisms on
the second occurrence.

### R4 — No Workarounds
No sleeps, blind retries, magic numbers, manual infrastructure commands, or
fallback branches. Use \`charly\` or fix the missing capability.

### R4a — Fix the Product First
Documentation never routes around a defect. Fix the code before the prose.

### R5 — Delete Legacy Completely
Hard cutover removes the old path and every stale reference, shim, alias, and
TODO in one commit.

### R6 — Git Safety
Check \`git status\` and stashes before destructive actions. No force-push,
pushed-history rewrite, hook bypass, or direct push to \`main\`.

### R7 — Prove Behavior, Not Compilation
A green compile proves nothing. Run the changed path live and retain output.

### R8 — Preserve Emitted Artifacts
Validate labels, plans, configs, schemas, and generated files at their actual
boundary.

### R9 — Binary Equals Source
Build with \`task build:binary\`, invoke through \`bin/\`, verify version.

### R10 — Fresh Disposable Proof
Verify only on targets explicitly marked \`disposable: true\`. Fresh rebuild
from the final committed tree. Pasted output, zero warnings.

### PR Body Requirements
Every PR body must contain:
1. **## Summary** — what changed and why
2. **## How tested** — pasted command + output for every verification step
3. **## Project-rulebook rule-compliance** — table with every applicable rule
4. **## Change Classification** — change class, R10 gate, attribution tier
5. ***Assisted-by: ...*** — italicized footer at the end

### Attribution Tiers
| Confidence | Required proof |
|---|---|
| \`fully tested and validated\` | Every runtime standard + fresh-rebuild R10 on every affected disposable target; changed paths executed live |
| \`analysed on a live system\` | Changed runtime path ran live with retained output; full R10 did not pass |
| \`documentation reviewed\` | Docs-only change class (forbidden if code/config changed) |
| \`syntax check only\` | Compile/unit/dry-run only — R10 incomplete, do not commit |
| \`theoretical suggestion\` | No validation — never ship |

### Validator Verdict Discipline
Every validator BLOCK must be read in full and ALL listed issues fixed before
the next push. A partial fix that addresses only one of several findings is a
defective cycle.`;
}

/** Parse the skill dispatcher table from AGENTS.md content. */
function parseDispatcherTable(content: string): Array<{ triggers: string[]; path: string }> {
  const result: Array<{ triggers: string[]; path: string }> = [];
  // Find the generated dispatcher table
  const tableStart = content.indexOf("<!-- BEGIN GENERATED SKILL DISPATCHER -->");
  const tableEnd = content.indexOf("<!-- END GENERATED SKILL DISPATCHER -->");
  if (tableStart === -1 || tableEnd === -1) return result;

  const table = content.slice(tableStart, tableEnd);
  // Extract rows from the Markdown table: | Trigger | Skill to load |
  const rowRegex = /\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|/g;
  let match;
  // Skip the header and separator rows
  let rowIndex = 0;
  while ((match = rowRegex.exec(table)) !== null) {
    rowIndex++;
    if (rowIndex <= 2) continue; // Skip header and separator
    const triggers = match[1].trim();
    const path = match[2].trim().replace(/`/g, ""); // Strip Markdown backticks from code-formatting
    if (triggers && path && !triggers.startsWith("<!--")) {
      result.push({
        triggers: triggers.split("/").map((s) => s.trim()).filter(Boolean).concat(
          triggers.split(",").map((s) => s.trim()).filter(Boolean),
        ),
        path: path.replace(/^\//, ""), // Remove leading /
      });
    }
  }
  return result;
}

export default function (pi: ExtensionAPI) {
  // =========================================================================
  // Layer 1: System prompt injection — every turn
  // =========================================================================
  pi.on("before_agent_start", async (event) => {
    return {
      systemPrompt: event.systemPrompt + "\n\n" + buildRulesBlock(),
    };
  });

  // =========================================================================
  // Layer 2: Custom tool — charly_load_skills
  // =========================================================================
  pi.registerTool({
    name: "charly_load_skills",
    label: "Load Charly Skills",
    description:
      "Load the full content of SKILL.md files matching the given trigger keywords. " +
      "Call this at the start of every task after consulting the skill dispatcher " +
      "table in AGENTS.md (R0). Pass the trigger keywords from the user's request.",
    promptSnippet: "Load skill documentation matching the current task",
    promptGuidelines: [
      "Use charly_load_skills at the START of every task to load the skills the dispatcher selects (R0).",
      "Pass the trigger keywords from the user's request or AGENTS.md dispatcher: e.g. ['charly box build', 'Containerfile'].",
      "The tool returns the full SKILL.md content for every matching skill.",
      "The tool also loads the /charly-internals:git-workflow skill when the task involves git operations.",
    ],
    parameters: Type.Object({
      triggers: Type.Array(Type.String(), {
        description:
          "Keywords from the user's task that match the skill dispatcher trigger column. " +
          "One trigger per skill line. E.g. ['charly box build', 'Go source work', 'Fedora images'].",
      }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const agentsMd = join(ctx.cwd, "AGENTS.md");
      let content: string;
      try {
        content = await readFile(agentsMd, "utf8");
      } catch {
        return {
          content: [{ type: "text", text: "Error: AGENTS.md not found at project root." }],
          details: { error: "AGENTS.md not found" },
        };
      }

      const dispatcher = parseDispatcherTable(content);
      if (dispatcher.length === 0) {
        return {
          content: [{ type: "text", text: "Error: Could not parse the skill dispatcher table from AGENTS.md." }],
          details: { error: "Dispatcher table not found" },
        };
      }

      // Match trigger keywords against dispatcher
      const matchedPaths = new Set<string>();
      const lowerTriggers = params.triggers.map((t) => t.toLowerCase());

      for (const entry of dispatcher) {
        for (const trigger of entry.triggers) {
          const tl = trigger.toLowerCase();
          for (const keyword of lowerTriggers) {
            if (tl.includes(keyword) || keyword.includes(tl)) {
              matchedPaths.add(entry.path);
              break;
            }
          }
        }
      }

      // Always include git-workflow when git operations are mentioned
      const gitKeywords = ["git", "push", "commit", "branch", "pr", "pull request", "merge", "worktree"];
      const hasGit = lowerTriggers.some((t) => gitKeywords.some((g) => t.includes(g)));
      if (hasGit) {
        matchedPaths.add("charly-internals--git-workflow");
      }

      // Read each matching SKILL.md
      const results: string[] = [];
      for (const skillPath of matchedPaths) {
        // Convert dispatcher paths (e.g. /charly-internals:git-workflow) to
        // filesystem paths (e.g. .agents/skills/charly-internals--git-workflow/SKILL.md)
        const skillDir = skillPath
          .replace(/^\//, "")           // Remove leading /
          .replace(/:/g, "--");          // Convert : to -- (filesystem convention)
        const globalPath = join(ctx.cwd, ".agents", "skills", skillDir, "SKILL.md");
        try {
          const skillContent = await readFile(globalPath, "utf8");
          results.push(`=== ${skillDir}/SKILL.md ===\n${skillContent}`);
        } catch {
          results.push(`[SKILL NOT FOUND: ${skillPath} — tried ${globalPath}]`);
        }
      }

      if (results.length === 0) {
        return {
          content: [
            {
              type: "text",
              text:
                `No skills matched triggers: ${params.triggers.join(", ")}. ` +
                `Available dispatcher entries: ${dispatcher.length}. ` +
                `Try broader keywords or check AGENTS.md for the full list.`,
            },
          ],
          details: { matched: [], count: 0, dispatcherCount: dispatcher.length },
        };
      }

      return {
        content: [
          {
            type: "text",
            text:
              `Loaded ${results.length} skill(s) matching your triggers.\n\n` +
              results.join("\n\n"),
          },
        ],
        details: {
          matched: Array.from(matchedPaths),
          count: results.length,
          dispatcherCount: dispatcher.length,
        },
      };
    },
  });

  // =========================================================================
  // Layer 7: Custom tool — charly_worktree_create
  // =========================================================================
  const _worktrees: Array<{ slug: string; path: string; branch: string }> = [];

  pi.registerTool({
    name: "charly_worktree_create",
    label: "Create Worktree",
    description:
      "Create a linked worktree for a new feat branch, initialize submodules, " +
      "and build the binary. Use this at the start of every cutover session.",
    promptSnippet: "Create a worktree for a new feature branch",
    promptGuidelines: [
      "Use charly_worktree_create at the START of every cutover to create an isolated worktree.",
      "Pass a URL-safe kebab-case slug (e.g. 'bump-gitlinks' or 'fix-schema-typo').",
      "The tool fetches origin, creates a feat/<slug> branch off origin/main, inits submodules, and builds the binary.",
      "Use charly_worktree_remove when done to clean up the worktree and branch.",
    ],
    parameters: Type.Object({
      slug: Type.String({
        description: "URL-safe kebab-case slug for the branch and worktree directory. E.g. 'bump-gitlinks'.",
      }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const slug = params.slug.replace(/[^a-z0-9-]/g, "_");
      const worktreePath = join(ctx.cwd, ".claude", "worktrees", slug);
      const branch = `feat/${slug}`;

      // Check if worktree already exists
      if (existsSync(worktreePath)) {
        return {
          content: [{ type: "text", text: `Worktree already exists at ${worktreePath}.` }],
          details: { path: worktreePath, branch, exists: true },
        };
      }

      try {
        // Step 1: Fetch and ff main
        await pi.exec("git", ["fetch", "origin", "--prune", "--tags"], { cwd: ctx.cwd });

        // Step 2: Create worktree with branch
        await pi.exec("git", ["worktree", "add", worktreePath, "-b", branch, "origin/main"], { cwd: ctx.cwd });

        // Step 3: Init submodules
        await pi.exec("git", ["submodule", "update", "--init", "--recursive"], { cwd: worktreePath });

        // Step 4: Build binary
        await pi.exec("task", ["build:binary"], { cwd: worktreePath });

        // Record for cleanup
        _worktrees.push({ slug, path: worktreePath, branch });

        return {
          content: [
            {
              type: "text",
              text:
                `Worktree created at ${worktreePath} on branch ${branch}.\n` +
                `- Binary built at ${worktreePath}/bin/charly\n` +
                `- Submodules initialized\n` +
                `- Use \`cd ${worktreePath}\` to work in this branch\n` +
                `- Use charly_worktree_remove when done`,
            },
          ],
          details: { path: worktreePath, branch, slug },
        };
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        return {
          content: [{ type: "text", text: `Error creating worktree: ${msg}` }],
          details: { error: msg },
        };
      }
    },
  });

  // =========================================================================
  // Layer 7: Custom tool — charly_worktree_remove
  // =========================================================================
  pi.registerTool({
    name: "charly_worktree_remove",
    label: "Remove Worktree",
    description:
      "Remove a previously created worktree and its feat branch. " +
      "Use this after the PR lands and the branch is merged.",
    promptSnippet: "Remove a worktree and its branch",
    parameters: Type.Object({
      slug: Type.String({
        description: "The slug of the worktree to remove (same as used in charly_worktree_create).",
      }),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const slug = params.slug.replace(/[^a-z0-9-]/g, "_");
      const worktreePath = join(ctx.cwd, ".claude", "worktrees", slug);
      const branch = `feat/${slug}`;

      const errors: string[] = [];

      // Remove worktree
      try {
        await pi.exec("git", ["worktree", "remove", worktreePath], { cwd: ctx.cwd });
      } catch (err) {
        // Try force removal
        try {
          await pi.exec("git", ["worktree", "remove", "--force", worktreePath], { cwd: ctx.cwd });
        } catch (err2) {
          errors.push(`worktree remove: ${err2 instanceof Error ? err2.message : String(err2)}`);
        }
      }

      // Delete branch (only if it's fully merged)
      try {
        const merged = await pi.exec("git", ["branch", "--merged", "origin/main"], { cwd: ctx.cwd });
        if (merged.stdout.includes(branch)) {
          await pi.exec("git", ["branch", "-d", branch], { cwd: ctx.cwd });
        }
      } catch {
        // Branch may already be deleted
      }

      // Remove from tracking
      const idx = _worktrees.findIndex((w) => w.slug === slug);
      if (idx !== -1) _worktrees.splice(idx, 1);

      if (errors.length > 0) {
        return {
          content: [{ type: "text", text: `Worktree removal completed with warnings:\n${errors.join("\n")}` }],
          details: { slug, errors },
        };
      }

      return {
        content: [{ type: "text", text: `Worktree ${worktreePath} and branch ${branch} removed.` }],
        details: { slug, path: worktreePath, branch },
      };
    },
  });

  // =========================================================================
  // Mechanical gates: tool_call interception for bash
  // =========================================================================
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "bash") return undefined;

    const command = event.input?.command;
    if (typeof command !== "string" || command.length === 0) return undefined;

    // The gate scripts expect the Claude Code PreToolUse input shape on
    // stdin: { "tool_input": { "command": "<command>" } }.
    const input = JSON.stringify({ tool_input: { command } });
    const tmp = join(
      tmpdir(),
      `charly-gate-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}.json`,
    );
    await writeFile(tmp, input, "utf8");

    try {
      for (const rel of GATE_SCRIPTS) {
        const script = join(ctx.cwd, rel);
        try {
          await access(script);
        } catch {
          // Gate script absent (e.g. running pi from a subdirectory) — skip.
          continue;
        }

        let result;
        try {
          result = await pi.exec("bash", ["-c", `"${script}" < "${tmp}"`], {
            cwd: ctx.cwd,
          });
        } catch (err) {
          // Fail-open on unexpected execution errors; only the gate's own
          // exit 2 is the block signal.
          continue;
        }

        if (result.code === 2) {
          const detail = (result.stderr ?? "").trim();
          return {
            block: true,
            reason: `charly gate (${rel}) BLOCKED: ${detail || "command violates a git-workflow mechanic"}`,
          };
        }
      }
    } finally {
      await unlink(tmp).catch(() => {});
    }

    return undefined;
  });
}