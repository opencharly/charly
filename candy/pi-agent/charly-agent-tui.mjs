#!/usr/bin/env node
// Pi-TUI view/controller for Charly's headless agent API. It owns no agent
// runtime or persistence and invokes only typed `charly agent` operations.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { Container, Editor, ProcessTerminal, Spacer, Text, TUI, matchesKey } from "@earendil-works/pi-tui";

const run = promisify(execFile);
const charly = process.env.CHARLY_BIN || "charly";
const terminal = new ProcessTerminal();
const tui = new TUI(terminal);
const root = new Container();
const title = new Text("Charly agents — /help for typed controls");
const body = new Text("Loading…");
const theme = {
  borderColor: (s) => s, selectList: { selectedPrefix: (s) => s, unselectedPrefix: (s) => s,
    selectedText: (s) => s, unselectedText: (s) => s, description: (s) => s, scrollInfo: (s) => s, noMatch: (s) => s },
  editor: { borderColor: (s) => s, selectList: { selectedPrefix: (s) => s, unselectedPrefix: (s) => s,
    selectedText: (s) => s, unselectedText: (s) => s, description: (s) => s, scrollInfo: (s) => s, noMatch: (s) => s } }
};
const editor = new Editor(tui, theme.editor);
root.addChild(title); root.addChild(new Spacer(1)); root.addChild(body); root.addChild(new Spacer(1)); root.addChild(editor);
tui.addChild(root); tui.setFocus(editor);

async function invoke(args) {
  const { stdout } = await run(charly, ["agent", ...args], { maxBuffer: 16 * 1024 * 1024 });
  return JSON.parse(stdout);
}
async function refresh() {
  try {
    const [runtimes, sessions, runs, teams, federation, incidents, rcas, recoveries] = await Promise.all([
      invoke(["runtime", "list"]), invoke(["session", "list"]), invoke(["run", "list"]),
      invoke(["team", "list"]), invoke(["federation", "list"]), invoke(["incident", "list"]),
      invoke(["rca", "list"]), invoke(["recover", "list"]),
    ]);
    const section = (name, value, render) => {
      const rows = Array.isArray(value) ? value : [];
      return [`${name} (${rows.length})`, ...(rows.length ? rows.map(render) : ["  —"])].join("\n");
    };
    body.setText([
      section("Runtimes", runtimes, (v) => `  ${v.class}:${v.provider}`),
      section("Sessions / targets", sessions, (v) => `  ${v.id}  ${v.runtime}  ${v.state}  ${JSON.stringify(v.target)}`),
      section("Runs", runs, (v) => `  ${v.id}  session=${v.session_id}  resume=${Boolean(v.resume)}`),
      section("Teams", teams, (v) => `  ${v.id}  coordinator=${v.team.coordinator || "—"}  members=${v.team.agents.length}`),
      section("Federated nodes", federation, (v) => `  ${v.id}  ${v.node}  ${v.state}`),
      section("Incidents", incidents, (v) => `  ${v.id}  ${v.state}  ${v.summary}`),
      section("RCA", rcas, (v) => `  ${v.id}  incident=${v.incident_id}  ${v.state}`),
      section("Recovery", recoveries, (v) => `  ${v.id}  ${v.action}  incident=${v.incident_id}`),
    ].join("\n\n"));
  } catch (error) { body.setText(`Error: ${error.message}`); }
  tui.requestRender();
}
async function terminalArgs(profile, runId) {
  const activeRun = await invoke(["run", "show", runId]);
  const session = await invoke(["session", "show", activeRun.session_id]);
  const resolvedProfile = session.terminal_profile || profile;
  if (!resolvedProfile) throw new Error(`run ${runId} has no terminal profile`);
  return {
    profile: typeof resolvedProfile === "string" ? resolvedProfile : JSON.stringify(resolvedProfile),
    target: JSON.stringify(session.target || {}),
  };
}
editor.onSubmit = async (text) => {
  const [command, id, ...rest] = text.trim().split(/\s+/);
  try {
    if (command === "/quit") { tui.stop(); return; }
    if (command === "/refresh") await refresh();
    else if (command === "/help") body.setText([
      "/refresh", "/run SESSION PROMPT", "/abort RUN", "/events RUN",
      "/snapshot PROFILE RUN", "/transcript RUN", "/input PROFILE RUN TEXT", "/paste PROFILE RUN TEXT",
      "/key PROFILE RUN KEY", "/resize PROFILE RUN COLS ROWS", "/close PROFILE RUN",
      "/delegate TEAM FROM TO PROMPT", "/quit",
    ].join("\n"));
    else if (command === "/run") body.setText(JSON.stringify(await invoke(["run", "start", id, rest.join(" ")]), null, 2));
    else if (command === "/abort") body.setText(JSON.stringify(await invoke(["run", "abort", id]), null, 2));
    else if (command === "/events") body.setText(JSON.stringify(await invoke(["run", "events", id]), null, 2));
    else if (command === "/snapshot") {
      const terminal = await terminalArgs(id, rest[0]);
      body.setText(JSON.stringify(await invoke(["terminal", "snapshot", terminal.profile, "--run-id", rest[0], "--target", terminal.target]), null, 2));
    }
    else if (command === "/transcript") body.setText(JSON.stringify(await invoke(["terminal", "transcript", id]), null, 2));
    else if (command === "/input" || command === "/paste") {
      const terminal = await terminalArgs(id, rest[0]);
      const args = ["terminal", "input", terminal.profile, rest.slice(1).join(" "), "--run-id", rest[0], "--target", terminal.target];
      if (command === "/paste") args.push("--paste");
      body.setText(JSON.stringify(await invoke(args), null, 2));
    } else if (command === "/key") {
      const terminal = await terminalArgs(id, rest[0]);
      body.setText(JSON.stringify(await invoke(["terminal", "key", terminal.profile, rest[1], "--run-id", rest[0], "--target", terminal.target]), null, 2));
    } else if (command === "/resize") {
      const terminal = await terminalArgs(id, rest[0]);
      body.setText(JSON.stringify(await invoke(["terminal", "resize", terminal.profile, rest[1], rest[2], "--run-id", rest[0], "--target", terminal.target]), null, 2));
    } else if (command === "/close") {
      const terminal = await terminalArgs(id, rest[0]);
      body.setText(JSON.stringify(await invoke(["terminal", "close", terminal.profile, "--run-id", rest[0], "--target", terminal.target]), null, 2));
    }
    else if (command === "/delegate") body.setText(JSON.stringify(await invoke(["delegate", id, rest[0], rest[1], rest.slice(2).join(" ")]), null, 2));
    else body.setText("Unknown command; use /help");
  } catch (error) { body.setText(`Error: ${error.message}`); }
  tui.requestRender();
};
tui.addInputListener((data) => { if (matchesKey(data, "ctrl+c")) tui.stop(); });
await refresh();
tui.start();
