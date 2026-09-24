#!/usr/bin/env node
// check-opencode-plugin.mjs — regression gate for the opencode gate plugin(s).
//
// No stubs or mocks: every assertion runs the REAL artifact — the plugin module,
// the REAL gate scripts, and (opt-in) the REAL `opencode` binary. A fake of the
// opencode boundary certifies behaviour the author IMAGINED rather than what
// opencode actually does, and cannot see a real integration break.
//
// Layers:
//   A (always)  — the plugin is a real ES module whose default export is a V2
//                 definition: a plain object with a string `id` and a `setup`
//                 function. That is the shape opencode >= 2.0 loads; the V1
//                 default-exported function fails it (the regression this gate
//                 exists for — it loads as a WARN-only no-op under 2.0).
//   B (always)  — the REAL gate scripts, invoked with the same stdin payload the
//                 plugin sends, BLOCK a hook-bypassing commit (exit 2) and ALLOW
//                 a benign one (exit 0).
//   C (opt-in)  — LIVE_OPENCODE=1 drives the REAL `opencode` binary end to end in
//                 a throwaway project: the loader resolves the plugin id, a gated
//                 shell call is DENIED, and a benign shell call succeeds. Skipped
//                 visibly when unset (live-or-skip; never a silent pass).
//
// Repo-agnostic by construction (auto-discovers the plugin, reads ids from the
// module) so the identical file is shared, byte-for-byte, everywhere it is needed.
//
// Usage: node scripts/check-opencode-plugin.mjs [--plugin <path>]
//   --plugin  check one specific file instead of auto-discovering (used to prove
//             the gate FAILS on the old V1 form).

import { spawnSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join, relative, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

function arg(name) {
  const i = process.argv.indexOf(name);
  return i !== -1 && i + 1 < process.argv.length ? process.argv[i + 1] : undefined;
}

// V2 discovery location first; V1's singular dir is checked too so an accidental
// file left there still fails on the V1 contract.
function discover() {
  const override = arg("--plugin");
  if (override) return [resolve(root, override)];
  const found = [];
  for (const dir of [".opencode/plugins", ".opencode/plugin"]) {
    let entries;
    try {
      entries = readdirSync(join(root, dir));
    } catch {
      continue;
    }
    for (const name of entries) if (name.endsWith(".ts")) found.push(join(root, dir, name));
  }
  return found;
}

let failures = 0;
const pass = (m) => console.log(`  PASS  ${m}`);
const fail = (m) => {
  failures += 1;
  console.error(`  FAIL  ${m}`);
};
const ok = (cond, message) => (cond ? pass(message) : fail(message));

function run(cmd, args, opts = {}) {
  // Some programs (opencode among them) resolve their working location from the
  // inherited PWD env var, NOT from the process's real cwd. spawnSync's `cwd`
  // sets only the latter, so a child would otherwise operate on the parent's
  // directory (RCA, measured: `opencode run` in a temp project committed in the
  // parent repo). Keep PWD in lockstep with cwd.
  const env = { ...process.env, ...(opts.env ?? {}) };
  if (opts.cwd) env.PWD = opts.cwd;
  return spawnSync(cmd, args, { encoding: "utf8", ...opts, env });
}

// --- Layer A: the real plugin module is a V2 definition ----------------------
const plugins = discover();
if (plugins.length === 0) {
  console.error("check-opencode-plugin: FAIL — no plugin under .opencode/plugins/");
  process.exit(1);
}

for (const pluginPath of plugins) {
  console.log(`check-opencode-plugin: ${pluginPath}`);
  let plugin;
  try {
    ({ default: plugin } = await import(pathToFileURL(pluginPath).href));
  } catch (err) {
    fail(`plugin did not import: ${err.message}`);
    continue;
  }
  // V2 = a definition object; V1 = a function.
  ok(
    plugin !== null && typeof plugin === "object" && !Array.isArray(plugin),
    "default export is a definition object (V2), not a V1 function",
  );
  ok(typeof plugin?.setup === "function", "definition exposes a setup() function");
  ok(
    typeof plugin?.id === "string" && plugin.id.length > 0,
    "definition has a string id",
  );
}

// --- Layer B: the REAL gate scripts, with the plugin's own stdin payload -----
const hooksDir = join(root, ".claude", "hooks");
const payload = (command) => JSON.stringify({ tool_input: { command } });
const commitGate = (command) =>
  run("bash", [join(hooksDir, "pre-commit-gate.sh")], { input: payload(command) });

if (!existsSync(join(hooksDir, "pre-commit-gate.sh"))) {
  fail("gate script .claude/hooks/pre-commit-gate.sh is missing");
} else {
  const blocked = commitGate("git commit --no-verify -m x");
  ok(
    blocked.status === 2,
    `real pre-commit-gate.sh BLOCKS \`git commit --no-verify\` (exit ${blocked.status})`,
  );
  const allowed = commitGate("git commit -m ok");
  ok(
    allowed.status === 0,
    `real pre-commit-gate.sh ALLOWS \`git commit -m ok\` (exit ${allowed.status})`,
  );
}

// --- Layer C: the REAL opencode binary, end to end (opt-in) -----------------
if (process.env.LIVE_OPENCODE === "1") {
  const which = run("sh", ["-c", "command -v opencode"]);
  if (which.status !== 0) {
    fail("LIVE_OPENCODE=1 but no `opencode` binary on PATH");
  } else {
    // Throwaway project carrying this repo's plugin + gate scripts.
    const proj = mkdtempSync(join(tmpdir(), "opencode-plugin-live."));
    try {
      cpSync(join(root, ".opencode"), join(proj, ".opencode"), { recursive: true });
      cpSync(join(root, ".claude"), join(proj, ".claude"), { recursive: true });
      run("git", ["init", "-q"], { cwd: proj });
      run("git", ["config", "user.email", "ci@example.invalid"], { cwd: proj });
      run("git", ["config", "user.name", "ci"], { cwd: proj });
      writeFileSync(join(proj, "f.txt"), "x\n");
      run("git", ["add", "f.txt"], { cwd: proj });

      const opencode = (prompt) =>
        // --standalone: a PRIVATE server for THIS throwaway project. Without it,
        // `opencode run` uses the shared background service, whose location is
        // scoped to whatever project first started it — so the model inherits
        // that project's skills and may refuse to call the tool at all, instead
        // of the gate doing the blocking (measured: the deny path never fired).
        run("opencode", ["run", "--standalone", "--auto", prompt], {
          cwd: proj,
          timeout: 240000,
        });
      const listPlugins = () => run("opencode", ["plugin", "list"], { cwd: proj });

      // The loader builds its location/plugin cache on the FIRST `plugin list`
      // call in a directory: call 1 reports "No plugins found", call 2 resolves
      // (RCA, measured — a `run` does NOT prime this). Call it twice, assert on
      // the second; deterministic, not a retry-on-failure.
      listPlugins();
      const list = listPlugins();
      for (const abs of plugins) {
        const rel = relative(root, abs); // e.g. .opencode/plugins/umbrella-gates.ts
        const line = list.stdout.split("\n").find((l) => l.includes(rel));
        ok(
          line !== undefined && !line.trim().startsWith("-"),
          `real \`opencode plugin list\` resolves ${rel}`,
        );
        if (line) console.log(`        ${line.trim()}`);
      }

      // 2. A gated shell call is DENIED by the real gate through real opencode.
      // The block comes from REPO STATE, not a "dangerous-looking" flag: a staged
      // `charly/*_aliases.go` trips the gate's ZERO-ALIASES rule on an innocent
      // `git commit -m x`. That matters because a recognisable bypass flag
      // (`--no-verify`, `core.hooksPath=…`) makes the model self-censor and never
      // call the tool at all (measured), which would prove nothing about the
      // gate; an innocent command is reliably run, and only the GATE refuses it.
      mkdirSync(join(proj, "charly"), { recursive: true });
      writeFileSync(join(proj, "charly", "foo_aliases.go"), "package charly\n\nvar x = kit.Y\n");
      run("git", ["add", "charly/foo_aliases.go"], { cwd: proj });
      const denied = opencode(
        "Run exactly this shell command and report what happens: git commit -m x",
      );
      ok(
        /BLOCKED/.test(denied.stdout) || /BLOCKED/.test(denied.stderr),
        "real opencode: a gate-blocked commit (staged alias file) is DENIED",
      );
      const afterBlock = run("git", ["log", "--oneline"], { cwd: proj });
      ok(
        afterBlock.stdout.trim() === "",
        "real opencode: the denied commit created nothing",
      );

      // 3. A benign shell call proceeds — same command, no blocked state staged.
      //    Unstage only the alias file and ensure f.txt is staged again; do NOT
      //    `git reset` (which would unstage everything and leave nothing to commit).
      run("git", ["rm", "-q", "--cached", "--ignore-unmatch", "charly/foo_aliases.go"], {
        cwd: proj,
      });
      run("git", ["add", "f.txt"], { cwd: proj });
      const allowed = opencode(
        "Run exactly this shell command and report the result: git commit -m live-allow",
      );
      const afterAllow = run("git", ["log", "--oneline"], { cwd: proj });
      ok(
        afterAllow.stdout.trim() !== "" && /live-allow/.test(afterAllow.stdout),
        "real opencode: a benign `git commit` succeeds",
      );
    } finally {
      rmSync(proj, { recursive: true, force: true });
    }
  }
} else {
  console.log(
    "  SKIP  live opencode layer (set LIVE_OPENCODE=1 to run the real binary end to end)",
  );
}

if (failures > 0) {
  console.error(`check-opencode-plugin: FAIL — ${failures} assertion(s)`);
  process.exit(1);
}
console.log("check-opencode-plugin: OK");
