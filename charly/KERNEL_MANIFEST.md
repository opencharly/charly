# Kernel manifest — draft (K-wave W3a unit B5)

Draft only: the W5 terminus turns this into the CI-enforced manifest (task #16). Each row below
is a file the W3 scoping map / orchestrator adjudication (`/tmp/w3-scoping-map.md`,
`/tmp/w3-adjudication.md`) classified STAY for the check-harness wire-broker M-mechanism (the
"B5 — leave alone" bucket, 10 files / ~1,317 LOC per the scoping map). Every verdict below was
RE-VERIFIED independently with this teammate's own defines-vs-calls grep — the scoping map and
orchestrator adjudication were read for orientation, never trusted as proof (north-star
heuristic: "a file's header claim is a CLAIM — verify with defines-vs-calls").

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| `check_kit_adapter.go` | 246 | M — plugin-loading mechanism | Defines `registerCompiledCheckVerb`, called 15× from the generated `plugins_generated.go` (one per compiled-in kit-shape candy: plugin-port, plugin-process, plugin-interface, …) — same call-site family as every other `RegisterBuiltinPluginUnit` registration. |
| `check_endpoint_resolve.go` | 155 | M — wire-broker reverse-channel return path (STAY pending the B7 spike — **do not move**; the resolution BODIES `resolveVerbEndpoint`/`resolveImageLabel` are flagged for a possible peer-dispatch/data-thread split, not settled) | `resolveVerbEndpoint`/`resolveImageLabel` are wired into `plugin_checkcontext_reverse.go`'s `checkContextReverseServer` via `provider_checkenv.go:187` (`resolveEp: h.resolveVerbEndpoint, resolveImgLabel: h.resolveImageLabel`) — the literal reverse-RPC service every out-of-process live-container verb (cdp/wl/vnc/dbus/mcp) dials back into. |
| `check_graphics_endpoint.go` | 141 | M/B — permanent STAY, live-handle constraint | `resolveVerbGraphics` constructs genuinely live, unmarshalable runtime handles (`sshx.NewSSHTunnel`, `checkhost.UnixToTCPBridge`) and is wired into the SAME `checkContextReverseServer` (`provider_checkenv.go:187`, `resolveGfx: h.resolveVerbGraphics`). Cleanest verdict in the set — not a "plugin can't load the project" constraint (K1/loaderkit does not dissolve it), a live-socket-lifecycle one. |
| `planrun_adapter.go` | 125 | M — wire-broker rendezvous | Defines `hostCheckCarrier`/`hostVerbResolver`, constructed at `plugin_dispatch_reverse.go:156,165` — the reverse dispatch server for out-of-process `InvokeProvider` calls. |
| `checkrun_act.go` | 110 | M — shared dispatch, fan-out beyond check | `resolveProvisionScript` called from `plugin_executor_reverse.go:197` (production, not test-only) in addition to its own `runProvisionAct`, which `planrun_adapter.go:117` calls — confirms fan-out beyond the check package into the generic act-emit path. |
| `verb_builtins.go` | 72 | M — wire-broker leg 2 | Defines `pluginVerb{}`, the generic `plugin:` verb discriminator, registered at `registry_bootstrap.go:51`. Irreducibly small. |
| `check_venue_resolve.go` | 62 | M — legs 1+2 (venue rematerialization + resolve) | `checkVenueExecFromReply`/`resolveCheckVenueReply` called from `check_graphics_endpoint.go`, `check_endpoint_resolve.go` (×2), AND `plugin_grpc.go:157,161` — confirms it is not check-exclusive (also consumed by the generic gRPC dispatch path). Textbook three-legs match. |
| `checkrun.go` | 50 | E — generic result-envelope helpers | **Scoping-map evidence CORRECTED** (R1): the map's cited evidence ("RunModeLive/RunModeBox, wide fan-in 22+ files") is STALE — W0 already deleted those in-core aliases; the file's OWN header says so. The file's ACTUAL current content is 3 tiny helpers (`passf`/`failf`/`skipf` wrapping `spec.CheckResult`); real production fan-in is 1 file (`checkrun_act.go`), not 22+. Verdict (STAY, trivial, not worth moving) is still correct — 11 LOC of E-clause formatting helpers — but the manifest records the TRUE evidence, not the stale claim. |
| `checkrun_charly_verbs.go` | 33 | M — reverse-leg glue | `resolveCheckApk` called from `provider_checkenv.go:135` (feeds host-scanned `CandyDirs` to the out-of-process adb/appium verbs' committed-APK anchoring). |
| `host_build_check_load_plugins.go` | 46 | M — plugin-loading mechanism | `hostBuildCheckLoadPlugins` (the `"check-load-plugins"` HostBuild seam) called from `candy/plugin-check/command.go:211` — confirmed production caller, not just the in-file header's say-so. |

## Non-blocking finding: `checkspec.go`'s misleading name

The brief asked to rename `checkspec.go` → `op_vocabulary.go` (or similar) "if trivial." It is
**not trivial**: a whole-tree grep found live, non-`CHANGELOG/` references to `checkspec.go` by
name across 4 different git repos beyond `charly/` itself —

- `spec/schema/_common.cue:45`, `spec/schema/candy.cue:310` (hand-written CUE comments — a
  regeneration of `cue_types_gen.go` does NOT fix these; they need manual edits)
- `spec/spec/cue_types_gen.go:9,3318` (comments carried through from the CUE source above)
- `spec/spec/union_types.go:131`, `spec/spec/verb_context.go:11,15`
- `candy/plugin-check/plan_grammar.go` (×4 references)
- `candy/plugin-bundle/bundle_test_helpers_test.go:28`
- `sdk/deploykit/plan_compile_test.go:42`
- `charly/layers.go:504,507,508`
- `tools/golden-compile/main.go:430`

A rename requires a cross-submodule R5 sweep (spec + sdk + candy/plugin-check +
candy/plugin-bundle + charly + tools) — a real cutover of its own, not a B5-scope drive-by edit.
Registering it here as a NON-BLOCKING finding for a future thematic batch (rename + doc-comment
sweep), per R2's discriminator: B5's own claim (the 10-file STAY confirm-and-close) is true and
proven without this rename.

## Clause key

- **M** — wire-broker Mechanism (one of the 3 sanctioned reverse-channel legs: venue-executor
  rematerialization, InvokeProvider peer dispatch, plugin-binary build + CLI reentry — or the
  plugin-loading mechanism itself).
- **E** — generic kind-blind vocabulary/envelope helper.
- **B** — irreducible same-process/live-handle Boundary fact (cannot marshal across the wire).
- **D** — Data-only registry/trait projection.
