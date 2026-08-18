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

export default async function charlyGates({ directory }) {
  const hooksDir = join(directory, ".claude", "hooks");
  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool !== "bash") return;
      const command = output.args?.command;
      if (typeof command !== "string") return;
      for (const gate of GATES) {
        if (gate.match.test(command)) {
          await runGate(hooksDir, gate.script, command);
        }
      }
    },
  };
}
