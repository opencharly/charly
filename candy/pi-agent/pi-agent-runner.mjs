#!/usr/bin/env node
// Thin native Pi harness entrypoint. JSONL on stdin/stdout is Pi's own RPC
// protocol from runRpcMode; Charly does not define or duplicate that protocol.
import {
  createAgentSessionFromServices,
  createAgentSessionRuntime,
  createAgentSessionServices,
  getAgentDir,
  runRpcMode,
  SessionManager,
} from "@earendil-works/pi-coding-agent";
import { spawn } from "node:child_process";

// Optional compatibility adapter. It deliberately delegates to the official
// Pi orchestrator CLI and its rpc-stream mode; Charly does not reproduce the
// experimental orchestrator socket protocol. The ordinary native runtime
// below remains the default and has no daemon dependency.
if (process.env.CHARLY_PI_ORCHESTRATOR === "1") {
  const instance = process.env.CHARLY_PI_ORCHESTRATOR_INSTANCE;
  if (!instance) throw new Error("CHARLY_PI_ORCHESTRATOR_INSTANCE is required in orchestrator mode");
  const child = spawn("orchestrator", ["rpc-stream", instance], { stdio: ["pipe", "pipe", "inherit"] });
  process.stdin.pipe(child.stdin);
  child.stdout.pipe(process.stdout);
  const code = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (value, signal) => signal ? reject(new Error(`orchestrator rpc-stream exited by ${signal}`)) : resolve(value ?? 1));
  });
  process.exitCode = code;
} else {

const cwd = process.env.CHARLY_PI_CWD || process.cwd();
const sessionFile = process.env.CHARLY_PI_SESSION_FILE || "";
const sessionDir = process.env.CHARLY_PI_SESSION_DIR || undefined;
const manager = sessionFile
  ? SessionManager.open(sessionFile, sessionDir)
  : SessionManager.create(cwd, sessionDir);

const createRuntime = async ({ cwd: effectiveCwd, sessionManager, sessionStartEvent }) => {
  const services = await createAgentSessionServices({ cwd: effectiveCwd });
  return {
    ...(await createAgentSessionFromServices({ services, sessionManager, sessionStartEvent })),
    services,
    diagnostics: services.diagnostics,
  };
};

const runtime = await createAgentSessionRuntime(createRuntime, {
  cwd,
  agentDir: getAgentDir(),
  sessionManager: manager,
});

try {
  await runRpcMode(runtime);
} finally {
  await runtime.dispose();
}
}
