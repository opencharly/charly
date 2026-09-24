#!/usr/bin/env node
// check-opencode-plugin.mjs — regression gate for the opencode gate plugin(s).
//
// Every `.opencode/plugins/*.ts` must satisfy the opencode V2 plugin contract.
// The gates were written to the V1 contract once (a default-exported function
// returning a keyed hook map); opencode >= 2.0 rejects that at load with
// "Plugin must export a default definition with an id and an effect or setup
// function", keeps starting, and the git gates then run UNENFORCED — a silent
// failure, which is why this test asserts the behaviour, not just the shape.
//
// Layer 1 (always): load the plugin with a stub V2 context, drive a blocked and
// an allowed command through the registered hook, and require the real gate
// scripts to BLOCK and ALLOW respectively. Fails on the V1 form.
// Layer 2 (opt-in, LIVE_OPENCODE=1): run the REAL `opencode plugin list` and
// require the plugin id to resolve. Skipped, visibly, when the flag is absent
// (live-or-skip, never a fake).
//
// Repo-agnostic by construction — the script is byte-identical in every repo
// that carries the plugin (keep it so; it is parity-shared). It discovers the
// plugin(s) and reads each id from the module, so no repo name is baked in.
//
// Usage: node scripts/check-opencode-plugin.mjs [--plugin <path>]
//   --plugin  check one specific file instead of auto-discovering (used to
//             prove the test FAILS on the old V1 form).

import { spawnSync } from "node:child_process";
import { readdirSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

function arg(name) {
  const i = process.argv.indexOf(name);
  return i !== -1 && i + 1 < process.argv.length ? process.argv[i + 1] : undefined;
}

// V2 discovery location first; V1's singular dir is kept for completeness so an
// accidental file left there still gets checked (and fails on the V1 contract).
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
    for (const name of entries) {
      if (name.endsWith(".ts")) found.push(join(root, dir, name));
    }
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

const plugins = discover();
if (plugins.length === 0) {
  console.error("check-opencode-plugin: FAIL — no plugin under .opencode/plugins/");
  process.exit(1);
}

for (const pluginPath of plugins) {
  console.log(`check-opencode-plugin: ${pluginPath}`);

  // --- Layer 1: shape + behaviour, with a stub V2 context -------------------
  let plugin;
  try {
    ({ default: plugin } = await import(pathToFileURL(pluginPath).href));
  } catch (err) {
    fail(`plugin did not import: ${err.message}`);
    continue;
  }

  // The V1 form exports a function; the V2 form exports a definition object.
  ok(
    plugin !== null && typeof plugin === "object" && !Array.isArray(plugin),
    "default export is a definition object (V2), not a V1 function",
  );
  ok(typeof plugin?.setup === "function", "definition exposes a setup() function");
  ok(typeof plugin?.id === "string" && plugin.id.length > 0, "definition has a string id");
  if (typeof plugin?.setup !== "function") {
    fail("cannot drive hooks without setup()");
    continue;
  }

  // Drive the registered hook with a stub context that captures it.
  const hooks = {};
  const ctx = {
    location: { directory: root },
    tool: {
      hook: async (name, cb) => {
        hooks[name] = cb;
        return { dispose: async () => {} };
      },
    },
  };
  await plugin.setup(ctx);
  ok(
    typeof hooks["execute.before"] === "function",
    "setup registers tool execute.before",
  );

  const call = (tool, command) =>
    hooks["execute.before"]({ tool, input: { command } });

  // A blocked command must reject (the gate script exits 2 → the hook throws).
  let blocked = false;
  try {
    await call("shell", "git commit --no-verify -m x");
  } catch {
    blocked = true;
  }
  ok(blocked, "a gate-blocked command (`git commit --no-verify`) is denied");

  // A benign command must resolve (gate exits 0).
  let allowed = false;
  try {
    await call("shell", "git commit -m ok");
    allowed = true;
  } catch (err) {
    fail(`a benign command was wrongly denied: ${err.message}`);
  }
  ok(allowed, "a benign command (`git commit -m ok`) is allowed");

  // A non-shell tool must not be inspected.
  let other = true;
  try {
    await call("read", "git commit --no-verify -m x");
  } catch {
    other = false;
  }
  ok(other, "a non-shell tool call is ignored");

  // --- Layer 2: the REAL loader, opt-in -------------------------------------
  if (process.env.LIVE_OPENCODE === "1") {
    // Warm-up: opencode builds its location/plugin cache on the first
    // invocation in a directory, and a just-added local plugin is not listed
    // until the second (RCA, measured: call 1 → "No plugins found", call 2 →
    // resolved). Warm, then assert — never assert on the cold call.
    const listOnce = () =>
      spawnSync("opencode", ["plugin", "list"], { cwd: root, encoding: "utf8" });
    const findByPath = (stdout) =>
      stdout.split("\n").find((l) => l.includes(pluginPath));

    let r = listOnce();
    if (r.error) {
      fail(`opencode plugin list did not run: ${r.error.message}`);
    } else {
      if (r.status !== 0) fail(`opencode plugin list exit ${r.status}`);
      let line = findByPath(r.stdout);
      if (line === undefined) {
        r = listOnce();
        line = findByPath(r.stdout);
      }
      // A load failure shows the id as "-"; a real load shows the plugin id.
      ok(
        line !== undefined && !line.trim().startsWith("-"),
        `live loader resolves id "${plugin.id}" (opencode plugin list)`,
      );
      if (line) console.log(`        ${line.trim()}`);
    }
  } else {
    console.log(
      "  SKIP  live loader check (set LIVE_OPENCODE=1 to run against real opencode)",
    );
  }
}

if (failures > 0) {
  console.error(`check-opencode-plugin: FAIL — ${failures} assertion(s)`);
  process.exit(1);
}
console.log("check-opencode-plugin: OK");
