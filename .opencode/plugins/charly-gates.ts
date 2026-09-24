/**
 * charly-gates.ts — opencode plugin running the same mechanical gates as pi and
 * Claude Code. Intercepts shell tool calls and runs
 * `.claude/hooks/pre-commit-gate.sh` / `pre-push-gate.sh`; the gates exit 2 to
 * BLOCK.
 *
 * opencode V2 plugin contract (measured against opencode 2.0.16): the default
 * export is a definition `{ id, setup }`. V1's default-exported function
 * returning a keyed hook map does NOT load — opencode logs
 * "Plugin must export a default definition with an id and an effect or setup
 * function" and keeps starting, leaving the gates silently unenforced. The shell
 * tool is `"shell"` in V2 (V1's `"bash"`), and the hook is registered with
 * `ctx.tool.hook("execute.before", …)`.
 */
import { spawn } from "node:child_process";
import { join } from "node:path";

const GATES = [
  { script: "pre-commit-gate.sh", match: /git.*commit/ },
  { script: "pre-push-gate.sh", match: /git.*push/ },
];

const GATE_TIMEOUT_MS = 30000;

function runGate(hooksDir, script, command) {
  const payload = JSON.stringify({ tool_input: { command } });
  return new Promise((resolve, reject) => {
    const child = spawn("bash", [join(hooksDir, script)], {
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stderr = "";
    child.stderr.on("data", (d) => (stderr += d.toString()));
    const timer = setTimeout(() => {
      child.kill("SIGTERM");
      reject(new Error(`[${script}] timed out after ${GATE_TIMEOUT_MS}ms`));
    }, GATE_TIMEOUT_MS);
    child.on("error", (err) => {
      clearTimeout(timer);
      reject(new Error(`[${script}] ${err.message}`));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code === 0) {
        resolve();
      } else {
        // The gate scripts exit 2 to BLOCK. Surface that as a hard error so
        // the tool call is denied rather than silently proceeding.
        reject(new Error(`[${script}] ${stderr.trim()}`));
      }
    });
    child.stdin.write(payload);
    child.stdin.end();
  });
}

export default {
  id: "charly-gates",
  async setup(ctx) {
    const hooksDir = join(ctx.location.directory, ".claude", "hooks");
    await ctx.tool.hook("execute.before", async (event) => {
      if (event.tool !== "shell") return;
      const input = event.input;
      const command =
        input && typeof input === "object" && "command" in input
          ? input.command
          : undefined;
      if (typeof command !== "string") return;
      for (const gate of GATES) {
        if (gate.match.test(command)) {
          await runGate(hooksDir, gate.script, command);
        }
      }
    });
  },
};
