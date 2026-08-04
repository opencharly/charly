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

## `unified_targets.go` — the `pluginDeployTarget` adapter (K-wave W3a unit A9)

A9's spike classified all 627 LOC of `unified_targets.go` at the function level (sent to the
orchestrator before any edit, per the north-star's spike discipline). Two candidate thin/move
targets were confirmed and executed (see below); the third candidate — the ~300 LOC of per-verb
lifecycle methods (`Add`/`Update`/`Del`/`Rebuild`/`Logs`/`Shell`/`Attach`/`Start`/`Stop`) — was
RULED STAY: extracting their inline wire-struct constructions into named `spec.XxxOptsFromYyy()`
conversion helpers would be LOC-neutral churn (the conversion has to live somewhere), not a real
reduction — these method bodies already ARE the plan's own "CLI→dispatch glue" end state. Recorded
here as the receipt this slice's variance (measured ~60 LOC vs the plan's ~150-250 LOC estimate)
is checked against, per the program's "measured beats estimated" running correction log.

| Symbol | LOC | Clause | Evidence |
|---|---:|---|---|
| `pluginDeployTarget` struct + `Name`/`Kind`/`Executor` | ~55 | M — adapter data | Holds ONLY plain data (name/word/hasLifecycle/hasPreresolve/node) + the live venue executor per S3b; constructible from data alone, never a stored core-private registry object. |
| `hostEnvJSON` | ~19 | B — same-process fact, threaded as DATA | `os.Executable()` resolves correctly to the charly binary ONLY when called in-core (R10 bed-found bug #5); computed once per dispatch call, threaded on the wire — does not anchor a seam, IS the seam's one irreducible fact. |
| `distroCfgJSON` | ~12 | M — wire helper for the adapter's own field | Marshals `t.build.DistroCfg`, tightly coupled to the struct's own `build buildEngineContext` field; no independent existence. |
| `dispatch` | ~24 (post-A9; was ~26, `ledgerRoot` guard deleted) | M — the dispatch mechanism itself | Every lifecycle method funnels through this ONE per-method wire call (build common fields, Invoke, track the returned venue) — the textbook adapter mechanism the boundary law's clause-M describes. |
| `applyParentExecOverride` | ~41 | M — live-executor-boundary handling | A live Go interface value (`opts.ParentExec`) cannot cross the wire; this function is the ONE place that both mutates `t.exec` AND flattens the SAME executor into `venue_json`, an invariant unit-tested directly (R10 bed-found bug, the nested-child-venue regression). |
| `venueExecutor` | ~17 | M — venue rematerialization (leg 1) | Re-materializes the CURRENT venue from `t.venueJSON` via `specexec.VenueFromDescriptor` — textbook three-legs leg 1, explicitly named STAY by the orchestrator adjudication. |
| `bracketedLifecycle` | ~17 | D — registry-bound trait read | Reads the declared `#DeployTraits.bracketed_lifecycle` via `deployTraitsFor(t.word)`, a core-private `providerRegistry`-backed resolver — cannot move without moving the registry itself. |
| `ResolveTarget` | ~40 | M — legs 1+2 (venue rematerialization + resolve) | Resolves `providerRegistry.ResolveDeploy`, constructs the live executor via `specexec.RootExecutorForDeployNode`, explicitly named STAY by the orchestrator adjudication. |
| `unresolvedDeployTargetError` | ~15 | M — co-located `ResolveTarget` error helper | Distinguishes "unknown target" from "known but unconnected" — reads `externalizedDeploySubstrates`/`externalDeploySubstratePlugins`, both registry-adjacent core data. |
| `Add`/`Update`/`Del`/`Rebuild`/`Status`/`Start`/`Stop`/`Logs`/`Shell`/`Attach` | ~300 | M — per-verb CLI→dispatch glue (RULED STAY, not extracted) | Each: build a wire-safe opts struct (one already via the named `spec.LifecycleOptsFromEmit`; the rest inline literals of comparable size), call `t.dispatch`, project the reply. This IS the adapter's per-verb shape the plan's own end state keeps — extracting the inline literals into named spec-side conversion functions moves LOC without reducing it. |
| `ErrNotSupportedOnExternal` + compile-time assertions | ~10 | E/M — trivial | Stays with the type it documents/asserts. |

**Executed thinning (K-wave W3a A9, ~60 LOC out):**
- `runUnifiedTargetChecks`'s `OnlyIDs` pre-filter loop moved to `candy/plugin-check`'s
  `verifyChecksRunOps` (new `spec.VerifyChecksRequest.OnlyIDs` field, opt-in additive — a
  zero-value request is byte-identical to every pre-existing `dispatchVerifyChecks` caller,
  verified against `check_cmd.go`'s `runLocalDeployScopePlan` — at the time the only other caller,
  which never set it; that function relocated to candy/plugin-bundle's verify_local.go at #55 W3
  B3). R1 fix in the same commit: the function's own doc comment ("Shared by Pod/Vm/the local
  deploy target.Test — the three were byte-identical") was stale — verified via grep, its real
  fan-in is exactly one call site (`pluginDeployTarget.Test`) post-S3b.
- `ledgerRoot` struct field + its `dispatch()`-time `req.LedgerRoot` threading deleted — zero
  production callers (verified via grep), its only use was one E2E test injecting a temp ledger
  path. Rewired to redirect `$HOME` for the test's duration instead, landing on
  `candy/plugin-bundle`'s existing `ledgerPathsFor` → `kit.DefaultLedgerPaths()` fallback (already
  `os.UserHomeDir()`-anchored) — the SAME isolation guarantee through an existing default-resolution
  path, no wire field needed.

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

## K-wave W2 (K3 build residue) — spike reclassifications, task #13

The audit's K3 bucket (2,924 LOC / 19 files, theme-bucketed by filename/directory, not
per-file defines-vs-calls) substantially overstated genuinely-movable residue: most of the
listed files are ALREADY correct M/B/D placements from prior relocation waves (#55
coneB2/coneB-render/coneB-buildtail/K1-loader-cone), or are blocked on K1's own remaining
work (LoadConfig/LoadUnified/config.go — W1 explicitly left these core; a K1-IOU file cannot
move independently in K3). Every row below is a RE-VERIFIED defines-vs-calls trace (headers
read for orientation, never trusted). One file (`build_overlay.go`) had a genuinely refuted
header claim and MOVED — see the W2 commits (spec `9b5ae78`, super `6044e3fa`); it does not
appear in this STAY table.

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| `host_build_buildengine.go` | 260 | MIXED: M (registry-connect) / B (bootstrap-scan + embed) / K1-IOU (remainder) | `hostBuildConnectPlugins` calls `loadProjectPlugins`, registering project build-time plugins into `providerRegistry` — M, the plugin-loading mechanism itself. `hostBuildScanLocal`/`hostBuildEnsureRepo`/`hostBuildScanRemote` wrap the bootstrap-delicate local candy scan + `EnsureRepoDownloaded` (refs.go, the K1-floor git clone/cache) — B. `hostBuildContextIgnoreBaseline` returns `baselineContextIgnore`, parsed from charly's OWN `//go:embed charly/charly.yml` (generate.go) — B, a separate Go module/binary structurally cannot embed another module's file. `hostBuildCollectRemoteRefs`/`hostBuildNamespaced`/`hostBuildPrep` call `LoadConfig`/`CollectRemoteRefsOpts`/`ScanAllCandyWithConfigOpts` (all core-only per W1's config.go verdict) — K1-IOU, not independently K3-movable. |
| `host_build_render_seam.go` | 149 | M — registry + live-loader-scan mechanism | `hostBuildRenderSeam` dispatches exactly 2 remaining methods (`RenderSeamInlineBuilder`→`resolveInlineBuilderSeam`, `RenderSeamEnsureBuilders`→`ensureBuildersConnected`), both calling `providerRegistry.ResolveBuilder` — the registry mechanism. File's own header confirms every OTHER former case (RenderService, ValidateEgress, LocalPkg, EmitPluginOp) already relocated plugin-side in prior waves (#67/K1/P8b) — this is the correctly-shrunk M residue, not unmoved bulk. |
| `host_build_construct_step.go` | 94 | M — THE kind/verb dispatch mechanism | `hostBuildConstructStep` resolves against `providerRegistry.ResolveVerb`/`providerRegistry.resolve(ClassStep,...)` — audit's own text names this "the TRUE kind dispatch... explicitly named by the skill as staying." |
| `builder_preresolve.go` | 88 | M — registry-connect mechanism | `ensureBuildersConnected` calls `loadProjectPlugins` + `providerRegistry.ResolveBuilder`/`ResolveBuilder` — core-private project-loading + registry-connect. Header confirms the ONLY other half (per-(candy,builder) OpCollectContext/OpReverse RPCs) already moved to `candy/plugin-bundle`'s own `preresolveBuilderContexts`. |
| `tasks.go` | 86 | M — registry-coupled, self-documented | `resolveInlineBuilderSeam` calls `ensureBuildersConnected` + `providerRegistry.ResolveBuilder` + `resolveBuilderStage` (a registry `Invoke`). File's own header: "the builder-emit cluster... is registry-coupled and stays core" — verified accurate by trace. |
| `host_build_box_ref_resolve.go` | 67 | M — host-internal reverse-channel caller | `resolveImageRefForEnsure`'s SOLE production caller is `plugin_executor_reverse.go:161`'s injected `ResolveImage` closure inside the reverse channel's `BuilderStep` host-step dispatch (leg 1 — live venue re-materialization during install-plan apply) — a host-internal, not cross-process, call site. |
| `builder_venue.go` | 66 | B — irreducible DATA envelope | `buildEngineContext` carries `*spec.Config`/`*Generator`/`*buildkit.ResolvedBox` (core-only types no sdk plugin can hold), threaded across a dozen already-floor `host_build_*.go` seams — the generic per-invoke descriptor those seams' own floor justification depends on. |
| `host_build_remote_image_resolve.go` | 58 | B (K1 floor) | `hostBuildRemoteImageResolve` calls `EnsureRepoDownloaded` (refs.go, the K1-floor git clone/cache) only — the box-RESOLVE half already moved plugin-side (`candy/plugin-build`'s `ensureRemoteRef`). Same K1-floor mechanism as `host_build_box_fetch_resolve.go`. |
| `dispatch_build_ensure.go` | 57 | M — pure registry dispatch | `dispatchBuildEnsure` calls `providerRegistry.resolve(ClassBuild,"ensure")` + `prov.Invoke(...)` — zero business logic beyond the registry dispatch wrapper. |
| `provider_builder_external.go` | 57 | D — static word→plugin map | `externalBuilderPlugins` is a static D-data map; `externalBuilderPluginRef` is a pure D-lookup consumed by `builder_preresolve.go`'s registry-connect (M) — the D-data twin of `provider_deploy.go`'s `externalDeploySubstratePlugins`. |
| `host_build_box_fetch_resolve.go` | 56 | B (K1 floor) | `hostBuildBoxFetchResolve` wraps `ResolveProjectRepo`→`EnsureRepoDownloaded` (refs.go — CHARLY_REPO_OVERRIDE + registered refs-backend download + auto-migration) — none of which an sdk-only plugin can run itself; refs.go is its own not-yet-K3 floor file. |
| `resource_resolve.go` + `distro_resolve.go` | 33 + 24 | M — kind-dispatch callback wrappers | `resolveResourceViaPlugin`/`resolveDistroViaPlugin` call `hostInvoke(ClassKind,...)` — the SAME `InvokeProvider`/registry kind-dispatch mechanism (leg 2) as `sdk/loaderkit`'s own `resolveDistroLeg`/`resolveResourceLeg` closures on the plugin side. Tiny core-internal callbacks `format_config.go`'s `LoadBuildConfigForBox` passes to `spec.ProjectDistroConfig`/`ResolvePluginKindViaPlugin`. |
| `layers.go` | 511 | K1-IOU | `ScanCandy`/`parseCandyYAML`/`scanLocalCandies` call `LoadUnified`/`ApplyDiscover`/`buildCandy` (unified.go/materialize.go — core-only per W1's own "config.go/LoadUnified STAYS" verdict, 12 live core call sites). Blocked on K1's own further resolution, not independently K3-movable. Audit-correction: the audit's row justification ("Candy struct... the skill's motivating-incident precedent") is stale against the CURRENT file — no `type Candy struct` exists anywhere in `charly/` today (W9 already moved it to `spec.CandyModel`/`spec.CandyView` before the audit ran); `plugins/internals/skills/plugin/references/boundary-law.md`'s own mention of this incident is accurate as written (explicit past-tense "an auditor counted... then self-corrected" — a historical illustration, not a current-state claim; fixed a DIFFERENT, unrelated stale-example line in the same file, see the W2 doc-sweep commit). |
| `generate.go` | 287 | MIXED: K1-IOU / B (embed) / M (registry) | `newCandyScanGenerator`/`ScanAllCandyWithConfigOpts` call `LoadConfig`/`LoadUnified` — K1-IOU. `baselineContextIgnore` reads charly's own `//go:embed charly/charly.yml` — B (same embed-boundary reason as `host_build_buildengine.go`'s context-ignore leg). `resolveInlineBuilderSeam`/`invokeOpEmitFragmentOpt` Invoke the provider registry — M. `createRemoteCandyCopies`/`candyByName`/`candyStageDirName` are host-fs helpers consumed by the K1-IOU functions above, in the same file. |
| `format_config.go` | 58 | K1-IOU | `LoadBuildConfigForBox`/`LoadDefaultBuildConfig` call `LoadUnified` (core-only, K1) — dies only when `LoadUnified`/`config.go`'s own K1 resolution happens. |

## K-wave W2 unit 3 — the validate.go/validate_project_host.go move, task #13

Per orchestrator ruling 3(a)-(c): the ~250 LOC of pure raw-config validate rules
(`validateBuildAndDistro`/`validateBoxBaseFrom`/`validateMergeConfig`/`validateBuildTunables`/
`validateBuilderRefs`) MOVED to `candy/plugin-box/validate_config_rules.go`, which self-loads the
raw `*spec.Config`/`*spec.DistroConfig`/`*spec.BuilderConfig` via the hoisted
`sdk/loaderkit.LoadUnifiedViaExecutor` witness (unit 2, same task). `validate.go` shrank
511→181 LOC (including the `boxEntityWireYAML`/`isNodeFormFile` CUE-support helpers that stay);
`validate_project_host.go` shrank 334→325 LOC (its `loadedProject` load path is UNCHANGED — the
CUE checks still need a fully-scanned project; only `runHostNaturalValidateChecks`'s function-call
list shrank from 8 calls to 3, dropping the `builderCfg` field it no longer needs).

| File (slimmed remainder) | LOC | Clause | Evidence |
|---|---:|---|---|
| `validate.go` | 181 | M — CUE-splice mechanism | `validateCandyCUESchemas`/`validateProjectCUESchemas` call `cueDocFromYAML`/`validateEntityClosedCUE`/`validateNodeFormSteps` (cue_schema.go/cue_node.go), which read `coreCueSchema()` — the HOST's SPLICED cross-plugin CUE schema (every connected plugin's own schema fragment unified at registry/schema-gate time into ONE `cue.Value` graph). A live, non-marshalable object — genuinely process-local, not portable to any plugin without first building a schema-splice-carrying seam (rejected as a NEW seam family per ruling 3(c); the existing `ProjectLoader` seam legs (`requireProjectLoader().ValidateEntityClosedCUE`/etc., U2/U3c) already carry the SAME functions for core's OWN in-process callers — `provider_kind_invoke.go`'s kind-dispatch — reusing an established pattern, not adding one, but that pattern's callers are all in-process; an out-of-process reach would still need a new wire leg). |
| `validate_project_host.go` | 325 | M (CUE, same as above) + K1-loader-refs-IOU (`validateRemoteCandies`) | `hostBuildValidateProjectChecks`'s `loadProjectForResolve` load path stays (feeds the CUE pair with a fully-scanned project + registers the build vocabulary `validateCandyCUESchemas` needs). `validateRemoteCandies` calls `CollectRemoteRefs` (refs.go), which needs `spec.RefsCollectSeams` (`Downloader`/`MigrateCache`/`ResolveLocal` — registry-coupled host callbacks) with NO existing executor-backed bridge (unlike `LoadSeams`, which the K1-loader wave already bridged) — an IOU for a future wave that builds that bridge, not this unit's scope (north-star heuristic 3: "the move WAITS for the enabler"). |

**Cascade check (per ruling 3(c), "verify each with the per-submodule grep")**: `validateEntityClosedCUE`/`validateNodeFormSteps`/`validateCandyManifestCUE` (cue_schema.go, K1 files) each still have their ONE production caller inside `validate.go` (`cueDocFromYAML`/`validateEntityClosedCUE` at `validateProjectCUESchemas`; `validateCandyManifestCUE` at `validateCandyCUESchemas`; `validateNodeFormSteps` at `validateProjectCUESchemas`'s root-file loop) — since the CUE pair STAYED (ruling 3(c), no new seam), these K1 files' cascade-deletion did NOT fire this unit; `cueDocFromYAML` additionally still serves `provider_kind_invoke.go` (the M kind-dispatch) regardless. No K1 file changed as a result of this unit.
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
- **`host_build_check_bed.go` (407 LOC) + `check_bed_run.go` (~110 LOC) + `bundle_members.go`
  (~180 LOC, its transitional-duplicate copy): FULLY DELETED (#55 W3 B2-full).** The file's own
  "STAYS PERMANENTLY — same live-OS-handle family as `host_build_arbiter_bracket.go`" framing was
  refuted (the boundary law's own named trap: a stays-core header is a claim, not a verdict) —
  contradicted by three already-written docs (`spec/schema/seam.cue`'s own `#CheckBedRequest`
  comment: "DIES at K5"; `spec/exec/venue_wait.go`'s header, which already relocated the readiness
  gates citing this exact principle; decisively, `sdk/deploykit/bed_session.go`'s own header:
  "the lock/lease/persist family... STAYS here (K5)" — the declared destination was never
  permanent core residence). Every piece dissolved without a host round-trip: the flock
  (`spec/lock`, plugin-importable), the preempt lease (direct `InvokeProvider(verb,"arbiter")`,
  the `vm_arbiter_shim.go` precedent — bypassing this manifest's own `arbiterProxy` STAY entirely
  for this ONE caller), the repo-override/deploy-config env vars (`os.Setenv` in the compiled-in
  plugin's own process, landing in the SAME process `hostBuildCli`'s cli-reentry children fork
  from). See `candy/plugin-check/bed_session.go` for the new home — documented there as a
  **compiled-in-REQUIRED placement class**, the same documented exception a bootstrap plugin
  carries, not an incidental fact about today's placement.
| `host_build_check_bed_gpu_prereq.go` | 27 | M — wire-broker leg, THE ONE seam surviving check-bed's dissolution | GPU host-DETECTION (`gpu_allocate.go`'s `bedGPUPrereqMissing`/`DetectVFIO`) is the project's explicitly operator-dropped exception (no hardware to verify a relocation against) — `gpu_shim.go`'s own header fences it from every K-wave cutover including this one, pending unit B6. This seam threads the claimant's resource tokens out and the verdict back so the fenced core logic runs completely unchanged; `candy/plugin-check/bed_session.go`'s `bedGpuPrereqCheck` is its sole caller. |

## Clause key

- **M** — wire-broker Mechanism (one of the 3 sanctioned reverse-channel legs: venue-executor
  rematerialization, InvokeProvider peer dispatch, plugin-binary build + CLI reentry — or the
  plugin-loading mechanism itself).
- **E** — generic kind-blind vocabulary/envelope helper.
- **B** — irreducible same-process/live-handle Boundary fact (cannot marshal across the wire).
- **D** — Data-only registry/trait projection.
- **K1-IOU** — blocked on the K1 loader wave's OWN remaining core files (`LoadConfig`/
  `LoadUnified`/`config.go`/`unified.go`) resolving further; not independently movable by a
  later wave without churning call sites to fake portability (forbidden — north-star heuristic
  3). Re-audit once K1 lands its own remainder.
