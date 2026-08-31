/**
 * github-pr-status.ts — the umbrella's GitHub PR + org-wide validation status
 * tool for pi agents.
 *
 * Why it exists: the org-wide `charly/pr-validator` GitHub Actions workflow
 * validates every PR, but its verdicts land on GitHub Actions, which has NO
 * path into a pi session. An agent that opens or fixes PRs therefore cannot
 * know whether its PR passed/failed without manually polling `gh` — the gap
 * that produced "I'll be woken as it concludes" being wrong.
 *
 * This tool closes it two ways:
 *   1. `check`  — one-shot status of a PR: state, head SHA, mergeable state,
 *                 the latest `charly/pr-validator` run ON THAT HEAD (not the
 *                 stale one), its conclusion, the failing step (Gate (BLOCK)
 *                 vs wait-for-ci), and the latest validator verdict comment.
 *   2. `watch`  — polls until the validator concludes on the CURRENT head (or
 *                 the PR merges/closes, or a timeout elapses), then returns
 *                 the full matrix. This is the real "wake me with the result"
 *                 primitive: run `gh_pr_status watch` as a background subagent
 *                 and its completion IS the wake.
 *
 * The tool wraps `gh` only (read-only queries; nothing is mutated). It
 * distinguishes a BLOCK verdict (the Gate step failed) from a CI-wait timeout
 * (wait-for-ci failed) from a build failure (the `go` check failed), because
 * each demands a different agent response.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { Type } from "typebox";

const execFileP = promisify(execFile);

/** Run gh with a JSON output and parse it; throw a readable error on failure. */
async function ghJson(args: string[]): Promise<any> {
  try {
    const { stdout } = await execFileP("gh", args, { maxBuffer: 16 * 1024 * 1024 });
    return JSON.parse(stdout);
  } catch (e: any) {
    const msg = e?.stderr?.toString?.() || e?.message || String(e);
    throw new Error(`gh ${args.join(" ")}: ${msg.slice(0, 300)}`);
  }
}

interface PRInfo {
  repo: string;
  number: number;
  state: string;
  headSha: string;
  mergeableState: string;
  title: string;
}

async function getPR(repo: string, number: number): Promise<PRInfo> {
  const p = await ghJson(["pr", "view", String(number), "--repo", repo, "--json",
    "state,headRefOid,mergeStateStatus,title"]);
  return {
    repo,
    number,
    state: p.state,
    headSha: p.headRefOid,
    mergeableState: p.mergeStateStatus ?? "unknown",
    title: p.title ?? "",
  };
}

/** The latest pr-validator run on a specific head SHA, or null. */
async function validatorRunOnHead(repo: string, headSha: string): Promise<any | null> {
  const runs = await ghJson(["run", "list", "--repo", repo, "--workflow", "pr-validator.yml",
    "--limit", "10", "--json", "databaseId,conclusion,headSha,status,createdAt"]);
  if (!Array.isArray(runs)) return null;
  const match = runs.find((r: any) => r.headSha === headSha);
  return match || null;
}

/** The failing step of a validator run, or "" if none. */
async function failingStep(repo: string, runId: string): Promise<string> {
  try {
    const j = await ghJson(["run", "view", String(runId), "--repo", repo, "--json", "jobs"]);
    const names = new Set<string>();
    for (const job of j?.jobs ?? []) {
      for (const step of job?.steps ?? []) {
        if (step.conclusion === "failure") names.add(step.name);
      }
    }
    return [...names].join(", ");
  } catch {
    return "";
  }
}

/** The latest validator verdict comment (PASS/BLOCK/unknown). */
async function latestVerdict(repo: string, number: number): Promise<string> {
  try {
    // NO --jq here: ghJson does JSON.parse on stdout, and `--jq ".[].body"`
    // emits raw unquoted markdown (not valid JSON), which would always throw
    // and leave the verdict permanently "unknown". Fetch the JSON array and
    // scan the bodies in JS instead.
    // The issue-comments API returns ascending by default and IGNORES
    // direction=desc, so per_page=5 alone would fetch the OLDEST 5 comments
    // and scan backward through them — reporting a stale BLOCK verdict even
    // when the latest verdict is PASS. Fetch a full page and scan the LAST 5.
    const comments = await ghJson(["api", `repos/${repo}/issues/${number}/comments?per_page=100`]);
    const texts: string[] = Array.isArray(comments) ? comments.map((c: any) => c.body ?? "") : [];
    // Scan the NEWEST 5 comments (the tail of the ascending page) backward.
    const tail = texts.slice(-5);
    for (let i = tail.length - 1; i >= 0; i--) {
      const m = tail[i].match(/Verdict:\s*(PASS|BLOCK)/);
      if (m) return m[1];
    }
    return "no-verdict-yet";
  } catch {
    return "unknown";
  }
}

function formatCheck(pr: PRInfo, run: any, verdict: string, failing: string): string {
  const lines = [
    `repo:        ${pr.repo}`,
    `PR:          #${pr.number} — ${pr.title}`,
    `state:       ${pr.state}`,
    `head:        ${pr.headSha.slice(0, 10)}`,
    `mergeable:   ${pr.mergeableState}`,
    `verdict:     ${verdict}`,
  ];
  if (run) {
    lines.push(`validator run: ${run.databaseId} @ ${run.headSha?.slice?.(0, 10)}`);
    lines.push(`run status:   ${run.status}`);
    lines.push(`run concl:    ${run.conclusion ?? "in-progress"}`);
    if (run.conclusion === "failure") {
      lines.push(`failing step: ${failing || "(see run log)"}`);
      lines.push(`interpret:    ${failing.includes("Gate (BLOCK)") ? "pr-validator BLOCKED the PR — read the latest review comment and fix every finding" :
                   failing.includes("Wait for the go gate") ? "the go gate did not conclude on the head within 19m — the go suite is slow or failed; check the ci.yml 'go' check" :
                   failing.includes("gofmt") ? "the go gate failed on gofmt — run gofmt -w on the changed files" :
                   "see the run log for the failing step"}`);
    }
  } else {
    lines.push(`validator run: none found on the current head ${pr.headSha.slice(0, 10)} — either the run is still queued, or the head moved after the last review`);
  }
  return lines.join("\n");
}

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "gh_pr_status",
    label: "GitHub PR + Validation Status",
    description:
      "Check the status of a GitHub pull request and its org-wide `charly/pr-validator` run. " +
      "Use `mode: check` for a one-shot status (PR state, head SHA, mergeable state, the latest " +
      "validator run ON THE CURRENT HEAD, its conclusion, the failing step, and the latest PASS/BLOCK " +
      "verdict). Use `mode: watch` to poll until the validator concludes on the current head (or the " +
      "PR merges/closes, or a timeout elapses) and return the full matrix — run `watch` inside a " +
      "background subagent so its completion wakes the main agent with the real result.",
    promptSnippet: "Check a GitHub PR's state and its charly/pr-validator verdict",
    promptGuidelines: [
      "Use gh_pr_status check after opening or fixing a PR to verify the validator verdict and the failing step — the GitHub Actions validator does NOT wake the agent, so check explicitly.",
      "Use gh_pr_status watch in a background subagent when you need to wait for a validator conclusion — its completion is the wake.",
      "A 'Gate (BLOCK)' failing step means the pr-validator BLOCKED the PR — read the latest review comment and fix every finding before re-pushing.",
      "A 'Wait for the go gate' failing step means the validator never reviewed the head (CI wait timeout) — check the ci.yml 'go' check on the head; a gofmt failure there blocks the review.",
      "The validator verdict comment may be STALE (from an older head) — always compare the run's headSha with the PR's current headSha.",
    ],
    parameters: Type.Object({
      repo: Type.String({ description: "Repo in owner/name form, e.g. opencharly/charly" }),
      pr: Type.Number({ description: "Pull request number" }),
      mode: Type.Optional(StringEnum(["check", "watch"] as const), {
        description: "check = one-shot status (default); watch = poll until the validator concludes",
      }),
      timeoutSeconds: Type.Optional(Type.Number({ description: "Watch timeout in seconds (default 1800, max 3600)" })),
      intervalSeconds: Type.Optional(Type.Number({ description: "Watch poll interval in seconds (default 60)" })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, _ctx) {
      const repo = params.repo as string;
      const number = params.pr as number;
      const mode = (params.mode as string | undefined) ?? "check";

      const poll = async (): Promise<string> => {
        const pr = await getPR(repo, number);
        const run = await validatorRunOnHead(repo, pr.headSha);
        const verdict = await latestVerdict(repo, number);
        const fail = run?.conclusion === "failure" ? await failingStep(repo, run.databaseId) : "";
        // Concluded states: the run concluded, OR the PR merged/closed, OR no PR.
        const concluded =
          pr.state !== "open" || (run?.conclusion === "success" || run?.conclusion === "failure");
        return { text: formatCheck(pr, run, verdict, fail), concluded };
      };

      const first = await poll();
      if (mode === "check" || first.concluded) {
        return { content: [{ type: "text", text: first.text }], details: {} };
      }

      // watch: poll until concluded or timeout
      const interval = (params.intervalSeconds as number | undefined) ?? 60;
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
