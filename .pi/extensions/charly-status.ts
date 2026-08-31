/**
 * charly-status.ts — the umbrella's OpenCharly bed / image / VM status tool for pi agents.
 *
 * Why it exists: every core activity of using, developing and validating OpenCharly
 * runs a disposable check bed (`charly check run <bed>`: image build, pod/VM start,
 * live probes) and needs to answer "is it still building? has it started? what are
 * the latest step results?". Without this tool an agent answers those questions with
 * ad-hoc `ps`, `tail .check/...`, `podman images` and `virsh domstate` shell games —
 * arcane, error-prone, and the exact failure class the org's own rules forbid (R4).
 *
 * This tool closes it the same way gh_pr_status does for the PR validator:
 *   1. `check` — one-shot structured status of a bed/image/vm from charly's OWN
 *      artifacts (the .check run tree + summary.yml + the bed lock + podman/virsh
 *      read-only queries).
 *   2. `watch` — polls until a bed run concludes (or a timeout elapses), then
 *      returns the full step matrix. This is the real "wake me with the result"
 *      primitive: run `charly_status watch` inside a background subagent and its
 *      completion IS the wake.
 *
 * The tool only READS: .check summary/log files under the project cwd and the
 * read-only `podman images` / `virsh domstate` queries. Nothing is mutated.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { readFile, readdir, stat } from "node:fs/promises";
import { join } from "node:path";
import { Type } from "typebox";

const execFileP = promisify(execFile);

/** Read the newest run dir under .check/<bed>/ (lexical calver dirs sort newest-last). */
async function newestRunDir(checkDir: string): Promise<string | null> {
  try {
    const entries = (await readdir(checkDir, { withFileTypes: true }))
      .filter((e) => e.isDirectory() && /^\d{4}\.\d{3}\.\d{4}$/.test(e.name))
      .map((e) => e.name)
      .sort();
    return entries.length ? join(checkDir, entries[entries.length - 1]) : null;
  } catch {
    return null;
  }
}

/** True when the bed lock exists (a run is in flight), like gh_pr_status uses run status. */
async function bedLocked(checkDir: string): Promise<boolean> {
  try {
    await stat(join(checkDir, ".lock"));
    return true;
  } catch {
    return false;
  }
}

/** Parse phase lines from a run log: [<phase>] PASS|FAIL after <dur> ... */
function phaseSummary(logText: string): { phase: string; verdict: string; detail: string }[] {
  const out: { phase: string; verdict: string; detail: string }[] = [];
  for (const line of logText.split("\n")) {
    const m = line.match(/\[(image-build|check-image|start|check-live|bring-up-members)\]\s+(PASS|FAIL|SKIP)(.*)$/);
    if (m) out.push({ phase: m[1], verdict: m[2], detail: m[3].trim() });
    const s = line.match(/(\d+) steps: (\d+) passed, (\d+) failed, (\d+) skipped/);
    if (s) out.push({ phase: "steps", verdict: s[3] === "0" ? "PASS" : "FAIL", detail: `${s[1]} steps: ${s[2]} passed, ${s[3]} failed, ${s[4]} skipped` });
  }
  return out;
}


/** Locate the .check/<bed> tree for a bed: explicit projectDir > cwd > the
 * umbrella's known box-project roots (charly/box/*). The bed runs under its
 * owning project dir, so the DEFAULT cwd almost never holds it — the search is
 * what makes the tool usable without the caller knowing the layout (R4). */
async function locateBedCheck(projectDir: string | undefined, bed: string): Promise<string | null> {
  const cwd = projectDir && projectDir !== "" ? projectDir : process.cwd();
  const candidates: string[] = [join(cwd, ".check", bed)];
  try {
    const boxRoots = await readdir(join(cwd, "charly", "box"));
    for (const d of boxRoots) candidates.push(join(cwd, "charly", "box", d, ".check", bed));
  } catch {}
  for (const c of candidates) {
    try {
      await stat(c);
      return c;
    } catch {}
  }
  return null;
}

/** The bed report: state + phases + step matrix, from charly's OWN .check tree. */
async function bedStatus(projectDir: string, bed: string): Promise<string> {
  const checkDir = (await locateBedCheck(projectDir || undefined, bed)) || join(process.cwd(), ".check", bed);
  const run = await newestRunDir(checkDir);
  const locked = await bedLocked(checkDir);
  if (!run) {
    return `bed ${bed}: no .check runs found under ${checkDir} (nothing has run here).`;
  }
  let logText = "";
  try {
    logText = (await readFile(join(run, "run.log"), "utf8")).slice(-32000);
  } catch {
    try {
      // fall back to a concatenation of the per-phase logs present
      const names = (await readdir(run)).filter((n) => n.endsWith(".log"));
      for (const n of names) {
        try {
          logText += (await readFile(join(run, n), "utf8")).slice(-4000) + "\n";
        } catch {}
      }
    } catch {}
  }
  const phases = phaseSummary(logText);
  const steps = phases.find((p) => p.phase === "steps");
  const lines: string[] = [
    `bed: ${bed}`,
    `state: ${locked ? "RUNNING" : "IDLE"}`,
    `latest run dir: ${run}`,
  ];
  if (steps) lines.push(`steps: ${steps.detail}`);
  for (const p of phases.filter((p) => p.phase !== "steps")) {
    lines.push(`phase ${p.phase}: ${p.verdict} ${p.detail}`);
  }
  lines.push(`verdict: ${locked ? "in-progress" : steps ? (steps.verdict === "PASS" ? "PASS" : "FAIL") : "unknown"}`);
  lines.push(`logs: ${run}/`);
  return lines.join("\n");
}

/** Image presence + newest build verdict, via read-only podman query + build log tail. */
async function imageStatus(image: string): Promise<string> {
  const lines: string[] = [`image: ${image}`];
  try {
    const { stdout } = await execFileP("podman", ["images", "--format", "{{.Repository}}:{{.Tag}}"], {
      maxBuffer: 8 * 1024 * 1024,
    });
    const found = stdout.split("\n").map((s) => s.trim()).filter((s) => s.includes(image));
    lines.push(found.length ? `present locally: ${found.length} tag(s) — ${found.slice(0, 3).join(", ")}${found.length > 3 ? ` +${found.length - 3} more` : ""}` : "present locally: NO");
  } catch (e: any) {
    lines.push(`podman query failed: ${String(e?.message || e).slice(0, 160)}`);
  }
  return lines.join("\n");
}

/** VM domain state via read-only virsh query. */
async function vmStatus(domain: string): Promise<string> {
  try {
    const { stdout } = await execFileP("virsh", ["domstate", domain], { maxBuffer: 4 * 1024 * 1024 });
    return `vm ${domain}: ${stdout.trim().split("\n")[0] || "unknown"}`;
  } catch (e: any) {
    return `vm ${domain}: virsh domstate failed — ${String(e?.message || e).slice(0, 160)}`;
  }
}


export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "charly_status",
    label: "Charly Bed / Image / VM Status",
    description:
      "Check the status of a charly check bed run, a built box image, or a VM domain " +
      "from charly's OWN artifacts (.check run tree + summary + the bed lock) and read-only " +
      "podman/virsh queries. Use `mode: check` for a one-shot structured status (bed state, " +
      "phases with PASS/FAIL, the step matrix, verdict; image local presence; VM domain state). " +
      "Use `mode: watch` with a bed to poll until the run concludes and return the step matrix — " +
      "run `watch` inside a background subagent so its completion wakes the main agent. Never " +
      "parse .check logs, ps, podman, or virsh ad hoc — this tool is the sanctioned surface.",
    promptSnippet: "Check the status of a charly check bed / image build / VM start",
    promptGuidelines: [
      "Use charly_status check for ANY bed/image/vm status question — an agent must never parse .check logs, ps, podman images, or virsh domstate by ad-hoc shell (R4); this tool reads charly's own artifacts behind the scenes.",
      "Use charly_status watch <bed> inside a background subagent (check-bed-runner / deploy-verifier / worker) when you need to wait for a bed run to finish — its completion is the wake.",
      "A bed verdict of FAIL with phases or steps failing means the run did not pass — read the failing phase log under the reported run dir (via a subagent with read access) and fix before re-running.",
      "The tool is read-only: it never starts, stops, or modifies any bed/image/VM; running a bed is `charly check run <bed>` via a subagent, and stopping is `charly check stop <bed>`.",
    ],
    parameters: Type.Object({
      target: StringEnum(["bed", "image", "vm"] as const, { description: "What to check: a check bed run, a built box image, or a VM domain" }),
      name: Type.String({ description: "Bed entity name (e.g. check-sway-browser-vnc-pod), box image name (e.g. sway-browser-vnc), or VM domain (e.g. check-charly-vm)" }),
      mode: Type.Optional(StringEnum(["check", "watch"] as const), {
        description: "check = one-shot status (default); watch = poll until the bed run concludes",
      }),
      projectDir: Type.Optional(Type.String({ description: "Project dir containing .check/ (default: the process cwd)" })),
      timeoutSeconds: Type.Optional(Type.Number({ description: "Watch timeout in seconds (default 1800, max 3600)" })),
      intervalSeconds: Type.Optional(Type.Number({ description: "Watch poll interval in seconds (default 30)" })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, _ctx) {
      const target = params.target as string;
      const name = params.name as string;
      const mode = (params.mode as string | undefined) ?? "check";
      const projectDir = (params.projectDir as string | undefined) ?? process.cwd();

      const poll = async (): Promise<{ text: string; concluded: boolean }> => {
        if (target === "bed") {
          const t = await bedStatus(projectDir, name);
          const concluded = !t.includes("state: RUNNING") && t.includes("steps:");
          return { text: t, concluded };
        }
        if (target === "image") return { text: await imageStatus(name), concluded: true };
        return { text: await vmStatus(name), concluded: true };
      };

      const first = await poll();
      if (mode === "check" || target !== "bed" || first.concluded) {
        return { content: [{ type: "text", text: first.text }], details: {} };
      }

      // watch: poll until the bed run concludes or timeout
      const interval = (params.intervalSeconds as number | undefined) ?? 30;
      const timeout = Math.min((params.timeoutSeconds as number | undefined) ?? 1800, 3600);
      const deadline = Date.now() + timeout * 1000;
      let last = first.text;
      while (Date.now() < deadline) {
        if (signal?.aborted) {
          return { content: [{ type: "text", text: last + "\n\n(watch aborted)" }], details: {} };
        }
        await new Promise((r) => setTimeout(r, Math.min(interval, Math.max(1, deadline - Date.now())) * 1000));
        const snap = await poll();
        last = snap.text;
        if (snap.concluded) {
          return { content: [{ type: "text", text: last + "\n\n(concluded)" }], details: {} };
        }
      }
      return {
        content: [{ type: "text", text: last + `\n\n(watch timed out after ${timeout}s — run again or check manually)` }],
        details: {},
      };
    },
  });
}
