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

## W4 K5 final-sweep additions

Independently re-derived with this teammate's own defines-vs-calls grep (per the north-star's
"a file's header claim is a CLAIM — verify"), including the mandatory survivor re-audit of the
two flagged K1-loader files.

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| `host_build_loader_floor.go` | 91 | M — wire-broker reverse-channel legs, the loader-mechanism face | Defines 4 HostBuild handlers (`loader-bootstrap`/`loader-walk`/`loader-threaded`/`loader-materialize`), each a pure forward into an ALREADY-established core M (`runBootstrapPhase`, `hostWalkProject`'s prescan+connect, `loaderThreaded()`'s D-snapshot, `provider_kind_invoke.go`'s registry kind-decode via `hostMaterializeProjectSeams()`). Confirmed, not refuted — the audit's own accepted exception among ~45 `host_build_*.go` files holds up. |
| `load_executor_host.go` | 62 | M (**UPGRADE** from the audit's weak-B flag) | Every method (`LoaderThreaded`/`RunBootstrapPhase`/`WalkProject`/`MaterializeLoadedProject`/`ValidateAndroidDevices`/`ValidatePreemptible`) is a literal one-line forward to the SAME mechanisms `host_build_loader_floor.go` dispatches to over the wire — this is that file's compiled-in TYPED (zero-marshal) mirror, the exact `plugin_inproc.go`-vs-`plugin_grpc.go` in-proc/gRPC transport duality already accepted elsewhere for the SAME mechanism. Not "layered on R residue": config.go/unified.go are the SAME floor per this file's own (K1-landed, credible) header. The audit's doubt ("simply the compiled-in dispatch table for R-residue bodies") does not survive — the bodies it forwards to are independently M, not R. |
| `plugin_providers_cmd.go` | 39 | M (**REFUTE** the audit's R/K5 call) | `PluginProvidersCmd.Run` is a thin `__plugin-providers` CLI wrapper directly over `collectPluginProviders` (`plugin_prescan.go`, confirmed M) — the SAME shape as `plugin_cmd.go`'s already-M `__plugin serve/list` CLI. No business logic of its own to move. |
| `cli_model_cmd.go` | 108 | M (**REFUTE** the audit's R/K5 call) | `buildCLIModel` reflects the literal `CLI{}` Kong struct (`package main`-only — Go forbids importing package main from anywhere else, a structural language impossibility, not difficulty) plus `providerRegistry.allProviders()`. Cannot move: no package can construct/import the reflected struct. Same class as `plugin_cmd.go`/`plugin_providers_cmd.go` — a CLI-introspection surface directly over core-only mechanisms. |
| `image.go` | 88 | B — CLI-root wiring | ~60 LOC is a migration-inventory comment (non-blocking CHANGELOG-trim candidate, not a K-wave question). Real code: `BoxCmd` (a bare `kong.Plugins` holder, zero logic — box command-dispersal is COMPLETE, core knows zero box verbs) + `FormatCLIError` (~20 LOC, CLI-root Kong-dispatch error-message rewriting for `main()`'s error path) — same class as `main.go`'s own B classification. |
| `host_exec.go` | 45 | M — pre-dispatch routing decision | `shouldReexecForHost` decides WHERE a command executes (local vs `--host` SSH remote) BEFORE Kong dispatches to anything — the same "decide-before-dispatch" shape as the already-M `plugin_command_prescan.go` pre-parse hooks (the file's own header draws this parallel; independently verified: no alternative placement is possible before Kong parse resolves the command path). `ReexecOverSSH`'s actual body already lives in `spec/hostenv` (an allowed core import, not sdk). Distinct from `main_freshness.go` (below): this one redirects dispatch; that one only gates it. |
| `vm_plugin_client.go` | 64 | M — wire-broker reverse leg, confirmed-permanent consumer | `invokeVmPlugin`/`invokeVmPluginEnv`'s ONE remaining caller is `check_graphics_endpoint.go`'s `resolveVerbGraphics` — already confirmed M/B permanent-STAY above ("constructs genuinely live, unmarshalable runtime handles... a live-socket-lifecycle constraint"). `connectPluginByWordRef`+`Operation` are core-private (the provider registry, a kernel Mechanism a plugin cannot call) per the file's own header; the OTHER former consumer (`preempt.go`) already proved plugins CAN reach `verb:libvirt` peer-to-peer directly, so this is not a blanket "can't move" claim — only the live-handle-building reverse leg genuinely needs it. |
| `oci_step_emit.go` + `step_emit_hostbuild.go` | 83 + 128 | M — STAY+CONSOLIDATE (per the W3 orchestrator adjudication item 6) | Confirmed only ONE word (`"oci-emit-step"`) is still registered on the `stepEmitters` micro-registry post-C1.5. `dispatchOCIStep`/`stepEmitOCIEmitStep` are thin forwarders to the compiled-in `"oci-dispatch"` class:step provider (`candy/plugin-installstep`) over the SAME `InvokeProvider`/in-proc-reverse-channel legs every other class:step dispatch uses — the host-side half every OUT-OF-PROCESS caller (`candy/plugin-deploy-pod`) still needs (it cannot hold the in-proc reverse channel + `buildEngineContext` itself). Consolidation done: `stepEmitters`/`registerStepEmitter`/`stepEmitterFor` indirection folded away since a word-keyed micro-registry adds nothing at n=1. |

### K5 seam-death (fully or partially dissolved, not thinned)

- **`host_build_hostprobe.go` (143 LOC) + `distro.go` (95 LOC): DELETED.** Both "stays core" header claims were refuted (host-hardware detection reaches `candy/plugin-gpu`'s verb:gpu / `candy/plugin-secrets`'s verb:credential peer-to-peer over `InvokeProvider` — the EXACT pattern the arbiter/`charly vm gpu` already use; the data tables are `candy/plugin-doctor`'s own embed now). The "hostprobe" HostBuild kind + its 4 CUE wire types (`#HostProbeRequest`/`#HostProbeReply`/`#HostProbeDevice`/`#HostProbeDistro`) are gone.
- **`devices.go` (119→~40 LOC) + `gpu_shim.go` (109→~65 LOC): PARTIALLY dissolved.** `appendEnvUnique` (dead — zero real callers, `candy/plugin-deploy-pod` already ports its own copy) + `DetectGPU`/`DetectAMDGPU`/`MemlockLimitBytes`/`VfioGroupAccessible`/`detectAMDGFXVersion` (dead once hostprobe's caller is gone) + the 3 embedded data tables (moved to `candy/plugin-gpu`'s own embed) + `deviceDescriptions` (moved to `candy/plugin-doctor`'s own embed) are DELETED. What remains is a genuine IOU, not a permanent floor: `DetectVFIO` (gpu_allocate.go's `bedGPUPrereqMissing`) and `DetectHostDevices`/`EnsureCDI` (host_build_pod_config_seams.go) plus `LogDetectedDevices` — all three consumers are HARD-FENCED files this cutover cannot touch. Per the W3 orchestrator adjudication (unit B6), those two files' GPU legs are ALREADY slated for peer-`InvokeProvider` dispatch ("seam leg dies") — landing B6 is what finally deletes these two files. `gpu_allocate.go`'s OWN `vfioPciAvailable()` helper — DEAD there once host_build_hostprobe.go (its one caller) was deleted — was itself deleted under a narrow, team-lead-granted fence exception (scope: that function + its own doc comment only; nothing else in that fenced file touched).
- **`credential_plugin.go` (238→~215 LOC): PARTIALLY dissolved.** `credentialHealth()`/`credentialHealther`/`.health()`/the `Health` field are DELETED (dead once hostprobe's caller is gone — `candy/plugin-doctor` now peer-`InvokeProvider`s verb:credential's `health` method itself, hostfacts.go). The rest (Get/Set/Delete/List/Name/resolve/awaitUnlock) STAYS — its one remaining caller, `check_endpoint_resolve.go`, is itself "STAY pending the B7 spike — not settled" per this same manifest (W3b, fenced).
- **Dead schema found + deleted**: `spec/schema/seam.cue`'s `#DevicePatternsRequest`/`#DevicePatternsReply` — a fully-generated, fully-documented CUE type describing a PLANNED `"device-patterns"` HostBuild seam with ZERO actual Go implementation or callers anywhere in the tree (core or candy/). Orphaned scaffolding, not a live seam — R1/R5 finding, removed alongside the hostprobe types.
- **`main_freshness.go` (281 LOC) + `main_freshness_test.go`: DELETED.** Failed all four kernel escapes by the letter (not E; not one of the 3 M's — touches none of loading/prescan/broker; not B — freshness isn't required for any plugin to load; not D). Moved to `candy/plugin-doctor`'s new `verb:freshness-guard` capability (`freshness.go`), invoked via a NEW `phase.PhasePreflight` lifecycle phase (`spec/phase`) mirroring `bootstrap_phase.go`'s `runBootstrapPhase`/`providersInPhase` shape exactly (team-lead ruling: phase-keyed enumeration, not a fixed-provider resolve). `preflight_phase.go`'s `runPreflightPhase`/`runPreflightPhaseWith` (below) is the new M-clause reverse-channel face; `main.go` now calls it once, unconditionally, before any command dispatch. Verified end-to-end live (not just unit tests): a stale-binary refusal fires with the byte-identical original message on a touched-newer-than-60s `charly/main.go`, safe verbs and the `CHARLY_SKIP_FRESHNESS_CHECK` bypass both still work.
| `preflight_phase.go` | ~56 | M — wire-broker reverse-channel leg, the preflight-phase mechanism face | Defines `runPreflightPhase`/`runPreflightPhaseWith`, the phase-ascending enumerate-and-Invoke pre-pass for `phase.PhasePreflight` providers — the exact `providersInPhase`/`Invoke` shape `bootstrap_phase.go`'s already-M `runBootstrapPhase` uses, one phase earlier. Called ONCE from `main.go`, unconditionally, before Kong dispatches to any command. |

## Clause key

- **M** — wire-broker Mechanism (one of the 3 sanctioned reverse-channel legs: venue-executor
  rematerialization, InvokeProvider peer dispatch, plugin-binary build + CLI reentry — or the
  plugin-loading mechanism itself).
- **E** — generic kind-blind vocabulary/envelope helper.
- **B** — irreducible same-process/live-handle Boundary fact (cannot marshal across the wire).
- **D** — Data-only registry/trait projection.
