/**
 * charly-gates.ts — Pi extension that enforces OpenCharly's git-workflow
 * command mechanics.
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
 * The gate scripts self-gate on a fast path (they exit 0 for commands that
 * do not mention `git commit` / `git push`), so running them for every bash
 * call is cheap and matches the doctrine exactly.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { writeFile, unlink, access } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

/** Gate scripts, relative to the project root (ctx.cwd). */
const GATE_SCRIPTS = [
  ".claude/hooks/pre-commit-gate.sh",
  ".claude/hooks/pre-push-gate.sh",
];

export default function (pi: ExtensionAPI) {
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
