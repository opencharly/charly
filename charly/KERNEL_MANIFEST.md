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
| `check_endpoint_resolve.go` | ~115 (SHRUNK, #55 W3 B7) | M — wire-broker reverse-channel service surface ONLY (**genuine STAY**, settled by the B7 spike): the RESOLUTION BODIES relocated to `candy/plugin-check/resolve_endpoint.go` (compiled-in-REQUIRED placement class, `bed_session.go`'s precedent; the plugin tracks its own per-process endpoint-cleanup state, drained on the host's explicit OpDrainEndpointCleanups signal — LIFO ordering pinned by resolve_endpoint_test.go); this file keeps only the fixed reverse-RPC methods + the thin wire-forward calls to `verb:check-resolve`'s new `OpResolveEndpoint`/`OpResolveImageLabel`/`OpDrainEndpointCleanups` legs | `resolveVerbEndpoint`/`resolveImageLabel` are wired into `plugin_checkcontext_reverse.go`'s `checkContextReverseServer` via `provider_checkenv.go:187` (`resolveEp: h.resolveVerbEndpoint, resolveImgLabel: h.resolveImageLabel`) — the literal reverse-RPC service every out-of-process live-container verb (cdp/wl/vnc/dbus/mcp) dials back into; `hostVerbResolver.RunVerb` wraps `providerRegistry.ResolveVerb`, a core-private M-mechanism no out-of-process dispatch can bypass — the genuine reason this service surface itself cannot move. |
| `check_graphics_endpoint.go` | 141 | M/B — permanent STAY, live-handle constraint | `resolveVerbGraphics` constructs genuinely live, unmarshalable runtime handles (`sshx.NewSSHTunnel`, `checkhost.UnixToTCPBridge`) and is wired into the SAME `checkContextReverseServer` (`provider_checkenv.go:187`, `resolveGfx: h.resolveVerbGraphics`). Cleanest verdict in the set — not a "plugin can't load the project" constraint (K1/loaderkit does not dissolve it), a live-socket-lifecycle one. |
| `planrun_adapter.go` | 125 | M — wire-broker rendezvous | Defines `hostCheckCarrier`/`hostVerbResolver`, constructed at `plugin_dispatch_reverse.go:156,165` — the reverse dispatch server for out-of-process `InvokeProvider` calls. |
| `checkrun_act.go` | 110 | M — shared dispatch, fan-out beyond check | `resolveProvisionScript` called from `plugin_executor_reverse.go:197` (production, not test-only) in addition to its own `runProvisionAct`, which `planrun_adapter.go:117` calls — confirms fan-out beyond the check package into the generic act-emit path. |
| `verb_builtins.go` | 72 | M — wire-broker leg 2 | Defines `pluginVerb{}`, the generic `plugin:` verb discriminator, registered at `registry_bootstrap.go:51`. Irreducibly small. |
| `check_venue_resolve.go` | 62 | M — legs 1+2 (venue rematerialization + resolve) | `checkVenueExecFromReply`/`resolveCheckVenueReply` called from `check_graphics_endpoint.go` AND `plugin_grpc.go:157,161` — confirms it is not check-exclusive (also consumed by the generic gRPC dispatch path). `check_endpoint_resolve.go`'s two former callers moved off it (#55 W3 B7 — `resolveVerbEndpointFor`/`resolveImageLabelFor` now wire-forward to `verb:check-resolve`'s own `OpResolveEndpoint`/`OpResolveImageLabel`, which classify the venue plugin-side via `venue.go`'s `resolveCheckVenue` directly, same-package, no round trip back through this seam). Textbook three-legs match, now for graphics + gRPC dispatch only. |
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
  **SUPERSEDED (#55 W3 B3 remainder)**: `Test`'s own precheck (team-lead's ruling: grep its real
  production callers before deciding) found ZERO — `charly check live` never reached it, its ONE
  caller anywhere was a unit test. Triple-deleted per the ruling's zero-callers branch:
  `Test`/`TestOpts`/`runUnifiedTargetChecks` (charly), `verifyChecksRunOps`/`filterOpsByID` +
  the `ops`/`only_ids` wire fields (candy/plugin-check + `spec.VerifyChecksRequest`), and the dead
  `"test"` op in `#DeployTargetDispatchRequest`'s enum. A FOURTH casualty surfaced by the same
  precheck: `dispatchVerifyChecks` itself (check_cmd.go) turned out to have zero production
  callers too — the "target: local --verify" path (this unit's other landed change,
  `candy/plugin-bundle/verify_local.go`) bypasses it via a direct `sdk.Executor.InvokeProvider`
  call, never the core-side wrapper — so it relocated to `checkrun_helpers_test.go` as test-only
  infrastructure rather than staying in production. A FIFTH: `venueExecutor`
  (`unified_targets.go`), the A9 adjudication's STAY-ruled leg-1 rematerializer, had exactly TWO
  callers — `Test` and the pre-relocation core-side `--verify` — both gone by the end of this same
  unit; deleted (row removed above). The box-mode context-skip regression coverage
  (`TestLiveVerb_SkipsUnderBoxMode`) moved onto the surviving Plan wire shape with zero coverage
  loss (RunOne, `sdk/kit/planrun.go`, is the shared per-step primitive both the deleted
  `kit.Runner.Run(ops)` and the surviving `kit.RunPlan(plan)` dispatched through).
- `ledgerRoot` struct field + its `dispatch()`-time `req.LedgerRoot` threading deleted — zero
  production callers (verified via grep), its only use was one E2E test injecting a temp ledger
  path. Rewired to redirect `$HOME` for the test's duration instead, landing on
  `candy/plugin-bundle`'s existing `ledgerPathsFor` → `kit.DefaultLedgerPaths()` fallback (already
  `os.UserHomeDir()`-anchored) — the SAME isolation guarantee through an existing default-resolution
  path, no wire field needed.

## `update_deploy_dispatch.go` — CONFIRMED STAY (K-wave W3a unit A5)

A5's brief named this file as a candidate relocation, citing its OWN header's "straightforward-
but-large RELOCATION work" framing (dated 2026-07-23, K1-UNBLOCK wave-4 spike). That framing
predates a LATER cutover (Cutover B, `host_build_pod_lifecycle_dispatch.go`) whose own header
already ruled this file "keeps its existing podUpdateCmd/dispatchByDeployTarget body UNCHANGED —
that resolver is registry+loader-coupled the same way [as `dispatchLifecycleTarget`]" — but that
correction never propagated back into `update_deploy_dispatch.go`'s own comment, leaving a stale
claim sitting alongside three OTHER already-correct sibling confirmations
(`charly/commands.go`'s header, `charly/pod_lifecycle_verb.go`'s header, `spec/schema/seam.cue`'s
`#PodUpdateRequest` comment — since renamed to `#PodUpdatePayload` by #55 W3 A10b's wire
unification, its confirmation carried forward unchanged in spirit) plus a LIVE check-plan
assertion (`charly.yml`'s check-fedora-pod bed: "dispatchByDeployTarget... UNCHANGED by this
cutover").

| Symbol | LOC | Clause | Evidence |
|---|---:|---|---|
| `dispatchByDeployTarget` | ~68 | M — deploy-dispatch orchestration, calls TWO irreducible core-private mechanisms | Calls `loadDeployPlugins` (`plugin_loader.go` — THE plugin-loading mechanism, mutates the core-private provider registry by connecting new out-of-process plugins) and `ResolveTarget` (`unified_targets.go` — reads `providerRegistry.ResolveDeploy` + type-asserts to the core-private `*grpcProvider`, already independently named STAY by the A9 orchestrator adjudication above). Neither has a plugin-visible equivalent — the EXACT same "one step that cannot cross the plugin boundary" pattern `pod_lifecycle_verb.go`'s `dispatchLifecycleTarget` already established for start/stop/shell/logs/service/cmd. |
| `resolveUpdateDeployNode` / `noteUpdateDisposability` | ~35 | M — tightly sequenced with the STAY body | Both are pure spec-native logic (no core-private coupling of their own) but feed directly into the SAME `dispatchByDeployTarget` call sequence — moving them plugin-side would be a wire-shape change (thread the resolved node instead of the whole tree) for ~35 LOC of savings, the same "LOC-neutral churn, not a real reduction" verdict the A9 ruling gave `unified_targets.go`'s per-verb methods. |

**Verdict**: confirm-and-close, no code moved (matching B5's tranche-1 precedent for the
check-harness STAY set) — fixed the ONE stale header in `update_deploy_dispatch.go` itself to
match what four other independent sources already correctly said.

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
| `host_build_buildengine.go` | 100 | MIXED: M (registry-connect) / B (embed) | `hostBuildConnectPlugins` calls `loadProjectPlugins`, registering project build-time plugins into `providerRegistry` — M, the plugin-loading mechanism itself. `hostBuildContextIgnoreBaseline` returns `baselineContextIgnore`, parsed HERE from charly's OWN `//go:embed charly/charly.yml` — B, a separate Go module/binary structurally cannot embed another module's file. NO K1-IOU remainder: K-wave 2 cone R1 (A2) DELETED FIVE legs, taking the family from 7 registered kinds to 2, and every survivor is now a clean E/M/B/D escape. The five: `buildengine-prep` (populated a render-seam Generator cache with no readers), `buildengine-collect-remote-refs` + `buildengine-ensure-repo` (unit 1 — only CALLED a relocated mechanism on the plugin's behalf, R-items by the defines-vs-calls test; candy/plugin-build drives them over `loaderkit.RefsSeamsFromExecutor`, with `spec.BoxResolveOpts`/`spec.WithLocalRawRefs` in the shared fabric rather than duplicated across the boundary), `buildengine-scan-remote` (unit 2) + `buildengine-scan-local` (unit 3), which both existed because the per-candy manifest parse APPEARED to need the clause-B `buildCandy` factory — an RDD spike over the whole 324-manifest corpus DISPROVED that (the pre-move `pn->genericNode->pn` round trip through it was an identity: 321 node-form manifests plus all 3 error paths, byte-identical), so the parse relocated to `loaderkit.ParseCandyManifest` and the scan body to `loaderkit.ProjectCandiesScanned`; and `buildengine-namespaced` (unit 3b), whose host-side recursion of the import-namespace tree emitted a flat `spec.NamespaceScanReply` that candy/plugin-build then walked AGAIN to fold — the same recursion paid twice across a process boundary, over a `uf` the plugin already held from `loaderkit.LoadUnifiedViaExecutor`. Its two per-namespace steps (`ProjectCandiesScanned`, `CollectRemoteRefsOpts`) had both left core in units 3a and 1, so the leg was pure call-not-define residue; candy/plugin-build's `fillNamespacedBoxes` now does the whole walk in ONE plugin-side pass and the `#NamespaceScanReply`/`#NamespaceScanEntry` wire envelope is deleted. `buildCandy`/`candyIsImage` stay core for their GENUINE clause-B consumers (the discovered-candy pre-check, `foldCandyKind`). |
| `host_build_construct_step.go` | 94 | M — THE kind/verb dispatch mechanism | `hostBuildConstructStep` resolves against `providerRegistry.ResolveVerb`/`providerRegistry.resolve(ClassStep,...)` — audit's own text names this "the TRUE kind dispatch... explicitly named by the skill as staying." |
| `tasks.go` | 55 | M — registry-coupled build-emit dispatch | `invokeOpEmitFragmentOpt` Invokes a resolved provider's `OpEmit` for the pod-overlay external-STEP build emit — the registry `Invoke` mechanism (leg 2). K-wave 2 cone R1 deleted this file's OTHER registry consumer, `resolveInlineBuilderSeam`: its "registry-coupled, stays core" claim did not survive the defines-vs-calls test — it only CALLED the registry, and the identical OpResolve dispatch already ran plugin-side for the detection/external builder legs, so it moved to `sdk/deploykit`'s `NewRenderGeneratorFromProject` beside them. |
| `host_build_box_ref_resolve.go` | 67 | M — host-internal reverse-channel caller | `resolveImageRefForEnsure`'s SOLE production caller is `plugin_executor_reverse.go:161`'s injected `ResolveImage` closure inside the reverse channel's `BuilderStep` host-step dispatch (leg 1 — live venue re-materialization during install-plan apply) — a host-internal, not cross-process, call site. |
| `builder_venue.go` | 88 | B — irreducible DATA envelope | `buildEngineContext` carries `*spec.Config`/`*Generator`/`*buildkit.ResolvedBox` (core-only types no sdk plugin can hold), threaded across a dozen already-floor `host_build_*.go` seams — the generic per-invoke descriptor those seams' own floor justification depends on. K-wave 2 cone R1 COLOCATED `Generator` here from the deleted generate.go: it is now a two-scalar carrier (`ExtraCandyRefs`/`DevLocalPkg`) read only by `oci_step_emit.go`'s BuildEnv population, so it has no independent identity to justify a file of its own. |
| `host_build_remote_image_resolve.go` | 58 | B (K1 floor) | `hostBuildRemoteImageResolve` calls `EnsureRepoDownloaded` (refs.go, the K1-floor git clone/cache) only — the box-RESOLVE half already moved plugin-side (`candy/plugin-build`'s `ensureRemoteRef`). Same K1-floor mechanism as `host_build_box_fetch_resolve.go`. |
| `dispatch_build_ensure.go` | 57 | M — pure registry dispatch | `dispatchBuildEnsure` calls `providerRegistry.resolve(ClassBuild,"ensure")` + `prov.Invoke(...)` — zero business logic beyond the registry dispatch wrapper. |
| `host_build_box_fetch_resolve.go` | 56 | B (K1 floor) | `hostBuildBoxFetchResolve` wraps `ResolveProjectRepo`→`EnsureRepoDownloaded` (refs.go — CHARLY_REPO_OVERRIDE + registered refs-backend download + auto-migration) — none of which an sdk-only plugin can run itself; refs.go is its own not-yet-K3 floor file. |
| `resource_resolve.go` + `distro_resolve.go` | 33 + 24 | M — kind-dispatch callback wrappers | `resolveResourceViaPlugin`/`resolveDistroViaPlugin` call `hostInvoke(ClassKind,...)` — the SAME `InvokeProvider`/registry kind-dispatch mechanism (leg 2) as `sdk/loaderkit`'s own `resolveDistroLeg`/`resolveResourceLeg` closures on the plugin side. Tiny core-internal callbacks `format_config.go`'s `LoadBuildConfigForBox` passes to `spec.ProjectDistroConfig`/`ResolvePluginKindViaPlugin`. |
| `layers.go` | 191 | K1-IOU | `ScanCandy`/`scanLocalCandies` call `LoadUnified`/`ApplyDiscover`/`projectCandiesScanned` (unified.go — core-only per W1's own "config.go/LoadUnified STAYS" verdict). Blocked on K1's own further resolution, not independently K3-movable. K-wave 2 cone R1 (A2) took 320 LOC out of this file: the candy-MANIFEST parse machinery (`parseCandyYAML` + `singleCandyMappingNode` + the legacy-key/unknown-key guards + the known-fields table) relocated to `sdk/loaderkit.ParseCandyManifest`, the distro/format vocabulary caches + `looksLikeDistroOrFormatKey` to `spec.CandyVocab`, and `withLocalRawRefs` to `spec.WithLocalRawRefs`. The parse was long assumed immovable because its node-form branch called the clause-B `buildCandy` factory; an RDD spike over the whole 324-manifest corpus proved that call was a `pn->genericNode->pn` IDENTITY round trip (321 node-form manifests plus all 3 error paths, byte-identical), so only the vocabulary REGISTRATION (`RegisterBuildVocabulary`) and the 3-line seam forward `parseCandyYAML` remain in core, colocated onto loader_threaded.go, the file that declares the CandyScanner seam they call. |

## K-wave W2 unit 3 — the validate.go/validate_project_host.go move, task #13

Per orchestrator ruling 3(a)-(c): the ~250 LOC of pure raw-config validate rules
(`validateBuildAndDistro`/`validateBoxBaseFrom`/`validateMergeConfig`/`validateBuildTunables`/
`validateBuilderRefs`) MOVED to `candy/plugin-box/validate_config_rules.go`, which self-loads the
raw `*spec.Config`/`*spec.DistroConfig`/`*spec.BuilderConfig` via the hoisted
`sdk/loaderkit.LoadUnifiedViaExecutor` witness (unit 2, same task). That unit left `validate.go` at
181 LOC and `validate_project_host.go` at 325, each carrying a named exit; **K-wave 2 cone R1 unit B
took both exits** — `validate.go` is DELETED and the host file is down to 90 LOC.

| File (slimmed remainder) | LOC | Clause | Evidence |
|---|---:|---|---|
| `validate_project_host.go` | 90 | M — the provider registry | **Both named exits are TAKEN; `validate.go` is DELETED and its row with it.** `validateCandyCUESchemas`/`validateProjectCUESchemas`/`validateRemoteCandies` moved VERBATIM into `candy/plugin-box/validate_schema_rules.go`. The CUE half needed no enabler once ruling 1 landed: `CueDocFromYAML`/`ValidateEntityClosedCUE`/`ValidateCandyManifestCUE`/`ValidateNodeFormSteps` are FREE FUNCTIONS in `sdk/loaderkit`, compiled against loaderkit's OWN schema, and the `spec.ProjectLoader` methods core called were bare forwards to exactly them — the "HOST's spliced cross-plugin CUE schema" justification was already recorded as REFUTED in unit 2's own row. The refs half's IOU was likewise already paid: `loaderkit.RefsSeamsFromExecutor` IS the executor-backed `spec.RefsCollectSeams` bridge that row said did not exist — `candy/plugin-build/resolve_legs.go` has paired it with `loaderkit.CollectRemoteRefsOpts` at two call sites since cone R1 A2. The candy set the rules run over is byte-identical either way: the envelope's `deploykit.NewSpecCandyModel` adapters are the SAME values `loaderkit.FinalizeScannedCandies` builds for the host scan (identical constructor, already-bare `GetRequire`/`GetIncludedCandy` refs). What remains is ONE seam, `hostBuildValidateWordSets` (`"validate-word-sets"`): the plugin sends the `plugin:` words it enumerated from its OWN envelope plus its out-of-tree candies' declared capability strings, and the host answers `ProviderCapabilities` + `ActCapableVerbs` off `providerRegistry` — the registry being a genuine kernel M-mechanism, and the only part of this seam that ever was. No load, no scan, no host-side diagnostics: the `loadProjectForResolve`/`loadedProject`/`addLoadDiag`/`runHostNaturalValidateChecks` re-derivation path is DELETED, as are the test-only `validateProjectForBuild` dispatch (relocated to `validate_dispatch_test.go`) and `plugin_prescan.go`'s now-callerless `registerExternalVerbsFromCandies` (its registration is inlined into the seam, fed by plugin-supplied data). |

**Cascade check — SUPERSEDED by K-wave 2 cone R1.** The paragraph here formerly recorded that the CUE pair STAYED and so the K1 CUE files' cascade-deletion did not fire. Ruling 1 fired it: `cue_defaults.go`, `cue_node.go`, and the nine `cue_kind_*.go` files are DELETED, `cue_schema.go` is down to the two kernel mechanisms above, and every former same-named core wrapper (`validateEntityClosedCUE`/`validateNodeFormSteps`/`validateCandyManifestCUE`/`cueDocFromYAML`/`validateNodeDocCUE`/`applyCueDefaults`) is gone — the remaining callers reach `requireProjectLoader()` directly, and the seam methods no longer take a schema handle.
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
- **`credential_plugin.go` (238→~215 LOC): PARTIALLY dissolved.** `credentialHealth()`/`credentialHealther`/`.health()`/the `Health` field are DELETED (dead once hostprobe's caller is gone — `candy/plugin-doctor` now peer-`InvokeProvider`s verb:credential's `health` method itself, hostfacts.go). The rest (Get/Set/Delete/List/Name/resolve/awaitUnlock) STAYS — its one remaining caller is `check_endpoint_resolve.go`'s `resolveVNCPassword` (the credential-store leg for VNC graphics auth, UNCHANGED by #55 W3 B7 — the B7 relocation touched only the endpoint/image-label resolution bodies, not this credential read), a genuine STAY per the B7 spike's settled verdict (this same manifest, above).
- **Dead schema found + deleted**: `spec/schema/seam.cue`'s `#DevicePatternsRequest`/`#DevicePatternsReply` — a fully-generated, fully-documented CUE type describing a PLANNED `"device-patterns"` HostBuild seam with ZERO actual Go implementation or callers anywhere in the tree (core or candy/). Orphaned scaffolding, not a live seam — R1/R5 finding, removed alongside the hostprobe types.
- **`main_freshness.go` (281 LOC) + `main_freshness_test.go`: DELETED.** Failed all four kernel escapes by the letter (not E; not one of the 3 M's — touches none of loading/prescan/broker; not B — freshness isn't required for any plugin to load; not D). Moved to `candy/plugin-doctor`'s new `verb:freshness-guard` capability (`freshness.go`), invoked via a NEW `phase.PhasePreflight` lifecycle phase (`spec/phase`) mirroring `bootstrap_phase.go`'s `runBootstrapPhase`/`providersInPhase` shape exactly (team-lead ruling: phase-keyed enumeration, not a fixed-provider resolve). `preflight_phase.go`'s `runPreflightPhase`/`runPreflightPhaseWith` (below) is the new M-clause reverse-channel face; `main.go` now calls it once, unconditionally, before any command dispatch. Verified end-to-end live (not just unit tests): a stale-binary refusal fires with the byte-identical original message on a touched-newer-than-60s `charly/main.go`, safe verbs and the `CHARLY_SKIP_FRESHNESS_CHECK` bypass both still work.

  <!-- W5 gate-3 fix (TestKernelManifestBidirectional): this row was previously a bare
  table line with no header of its own, orphaned mid-bullet-list — unparseable by any
  table-scoped reader. Same receipt, now a valid standalone one-row table. -->

| File | LOC | Clause | Evidence |
|---|---:|---|---|
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

  <!-- W5 gate-3 fix (TestKernelManifestBidirectional): same fix as preflight_phase.go above —
  this row was an orphaned bare table line with no header of its own. -->

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| `host_build_check_bed_gpu_prereq.go` | 27 | M — wire-broker leg, THE ONE seam surviving check-bed's dissolution | GPU host-DETECTION (`gpu_allocate.go`'s `bedGPUPrereqMissing`/`DetectVFIO`) is the project's explicitly operator-dropped exception (no hardware to verify a relocation against) — `gpu_shim.go`'s own header fences it from every K-wave cutover including this one, pending unit B6. This seam threads the claimant's resource tokens out and the verdict back so the fenced core logic runs completely unchanged; `candy/plugin-check/bed_session.go`'s `bedGpuPrereqCheck` is its sole caller. |

## W5 census — the remaining 94 files (task #16)

The W5-gates teammate drafted this section directly from the sources the program lead
pre-authorized (msg 2026-08-04): `/tmp/audit-prod-report.md`'s 150-row per-file table (the
E/M/B/D/R verdicts, near-verbatim where the file's current LOC still roughly matches the
audit's snapshot) + `/tmp/w3-adjudication.md`'s BINDING per-file corrections (overriding the
audit wherever the two disagree — e.g. `deploy_tree.go` R->D) + a corroborating defines-vs-calls
spot-grep on every file whose current LOC diverged >15% from the audit (a signal an earlier wave
already touched it), which surfaced several audit verdicts that no longer held (`node_build.go`
R->B, `node_normalize.go` R->M, `build_overlay.go` R->M, `substrate_template_resolve.go` R->M —
each cited "own header, corroborated — OVERRIDES the audit's stale call" below). A file the
per-file test still classifies R (residue) but that remains present because its dissolving
wave/unit/IOU hasn't landed yet is NOT invented into a false kernel-legal verdict — it gets an
`R-RESIDUE (exit: <wave/unit/IOU>)` row instead (see the Clause key below): documented residue,
not pretend-kernel. This census resolved every one of the 94 files unilaterally — no row needed
escalation to a CONTESTED list (the two sources never directly conflicted on the same file
without a clear tie-break; `deploy_tree.go` is the one direct disagreement, resolved by the
stated adjudication-overrides-audit priority order).

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| `agent_target_cmd.go` | 36 | M | generic `Provider.Channel` relay command — a transport CLI wrapper over the loading mechanism, not per-kind logic. (audit-prod-report.md) |
| `bootstrap_phase.go` | 54 | M | `runBootstrapPhase`/`providersInPhase` — the generic PhaseBootstrap dispatch (phase-keyed, kind-agnostic); `preflight_phase.go` (already documented above) mirrors this exact shape one phase later. (audit-prod-report.md) |
| `embed_defaults.go` | 58 | B | embedded default `charly.yml` bytes — a bootstrap data holder, same class as `main.go`'s process root. (audit-prod-report.md) |
| `loader_threaded.go` | 336 | M — the loader/refs/scanner seam registry + its LOAD-ENTRY forwards | Holds the registered spec.DocParser / ProjectWalker / ProjectLoader / CandyScanner / Materializer handles, the registry-derived `loaderThreaded()` D-snapshot, and the WalkSeams/MaterializeSeams the plugin calls back through — the plugin-loading + prescan-dispatch mechanisms themselves. K-wave 2 cone R1 COLOCATED the project LOAD-ENTRY forwards here from the deleted config.go / format_config.go / main_repo.go / unified.go: `LoadUnified`, `LoadConfig`, `LoadBuildConfigForBox`/`LoadDefaultBuildConfig`, `ResolveProjectRepo`, `ErrNoCharlyYml`. Each is a one-or-two-line forward into the seam declared in this same file, so the seam is their owner; inlining them into ~40 call sites instead would have GROWN the kernel to make four files disappear. Everything in those files that carried real LOGIC left to candy/plugin-loader instead. |
| `main.go` | 309 | B | process bootstrap / CLI root; calls `runPreflightPhase` once, unconditionally, before Kong dispatches to any command (see `preflight_phase.go` above). (audit-prod-report.md) |
| `materialize.go` | 145 | B | materialize leaf legs (bootstrap-candy-routed fold + embedded-default parse) — corrected post K1-unit-1; the box⊻layer routing predicate `candyIsImage` (`node_candy.go`, below) is its sibling. (audit-prod-report.md) |
| `node_candy.go` | 56 | B | `candyIsImage`/`buildCandy` — the box⊻layer bootstrap factory; `unified.go`'s discovered-candy pre-check calls it directly, so it stays *genericNode-typed permanently (own header, corroborated). (audit-prod-report.md) |
| `node_parse.go` | 23 | E | `genericNode` TYPE only — a pure carrier struct, no logic. (audit-prod-report.md) |
| `plugin_checkcontext_reverse.go` | 104 | M | `checkContextReverseServer` — the CheckContext reverse leg (wire broker, legs 1+2). (audit-prod-report.md) |
| `plugin_cmd.go` | 42 | M | `__plugin serve/list` CLI directly over the loading mechanism — same shape as `plugin_providers_cmd.go`/`cli_model_cmd.go` (already documented above). (audit-prod-report.md) |
| `plugin_command_prescan.go` | 77 | M | pre-parse external command-word prescan — the same "decide-before-dispatch" family `host_exec.go` (already documented above) draws its own parallel to. (audit-prod-report.md) |
| `plugin_dispatch_reverse.go` | 329 | M | defines `registerHostBuilder`/`hostBuilders` — THE HostBuild registry + dispatch + `InvokeProvider` wire-broker mechanism every `host_build_*.go` file registers into (see gate 2, reverse_channel_three_legs_test.go). (audit-prod-report.md) |
| `plugin_executor_reverse.go` | 377 | M | `executorReverseServer` — the reverse channel wire-broker leg (E3b). (audit-prod-report.md) |
| `plugin_grpc.go` | 452 | M | `grpcProvider` — the out-of-process plugin transport. (audit-prod-report.md) |
| `plugin_build_stamp.go` | 258 | M | content stamp for plugin-binary build reuse — the freshness half of THE loading mechanism (`plugin_loader.go`'s `buildPluginBinary`, whose source-path cache key deliberately cannot answer "is this binary current"). Kind-agnostic: digests a candy's source tree plus its local `replace` modules, never a kind word. |
| `plugin_inproc.go` | 143 | M | `inprocProvider` — the in-proc Provider placement (loading mechanism). (audit-prod-report.md) |
| `plugin_inproc_reverse.go` | 73 | M | in-proc reverse-channel bridge (wire broker). (audit-prod-report.md) |
| `plugin_loader.go` | 994 | M | plugin build/connect/schema-gate — THE loading mechanism (the largest M file; genuinely serves six provider classes across builtin/external/compiled-in placements per the audit's own per-class LOC-totals analysis). (audit-prod-report.md) |
| `plugin_prescan.go` | 525 | M | external deploy-substrate/kind/verb/step parse pre-scan — the six-class parse-time recognition mechanism. (audit-prod-report.md) |
| `plugin_provider_common.go` | 195 | M | shared capability metadata lift (both builtin+external placements). (audit-prod-report.md) |
| `plugin_transport.go` | 136 | M | plugin connect + schema/unit lift — part of the loading mechanism. (audit-prod-report.md) |
| `plugins_generated.go` | 129 | B | generated compiled-in-plugin registration (pluginsgen output) — reproducibility-gated bootstrap data. (audit-prod-report.md) |
| `provider.go` | 158 | M | the `Provider` interface — the ONE extension abstraction every kind/verb/deploy/step/builder/command placement implements. (audit-prod-report.md) |
| `provider_checkenv.go` | 214 | M | `CheckEnv` wire snapshot + `invokeVerbProvider` broker leg. (audit-prod-report.md) |
| `provider_command.go` | 46 | M | `CommandProvider` interface. (audit-prod-report.md) |
| `provider_command_external.go` | 406 | M | external command connect+dispatch — prescan-dispatch mechanism. (audit-prod-report.md) |
| `provider_deploy.go` | 152 | M | the `UnifiedDeployTarget`-adjacent interface + `externalDeploySubstratePlugins`/`deployTargetWords` D-data (the D-data half already independently corroborated via gate 1's grep survey). (audit-prod-report.md) |
| `provider_invoke.go` | 74 | M | shared host→plugin call codec — the generalized `Invoke` mechanism every provider class dispatches through. (audit-prod-report.md) |
| `provider_kind.go` | 43 | M | `KindProvider` interface — the typed dispatch contract. (audit-prod-report.md) |
| `provider_kind_invoke.go` | 410 | M | `runPluginKind`/`foldSubstrateKind`/`foldCandyKind` — the TRUE kind dispatch, explicitly named by the boundary-law skill as staying. (audit-prod-report.md) |
| `provider_registry.go` | 323 | M | `providerRegistry` — THE registry every other M mechanism resolves through. (audit-prod-report.md) |
| `provider_step.go` | 64 | M | pod-overlay build-emit bijection gate — `StepProvider` dispatch remnant. (audit-prod-report.md) |
| `provider_verb.go` | 140 | M | `CheckVerbProvider` interface. (audit-prod-report.md) |
| `registry_bootstrap.go` | 176 | B | `builtinProviderInstances` + the bijection gate — process-bootstrap wiring, same class as `main.go`. (audit-prod-report.md) |
| `reserved_registry.go` | 117 | D | reserved-word bijection gate + CUE-derived vocab sets. (audit-prod-report.md) |
| `version.go` | 76 | D | `BuildCalVer`/`ComputeCalVer` identity constants — pure D data. (audit-prod-report.md) |
| `host_build_deploy_plugins_connect.go` | 36 | K1-IOU | `hostBuildDeployPluginsConnect` CALLS `loadDeployPlugins` (does not define it) — a HostBuild consumer of the K1 plugin-loading mechanism, not independently movable ahead of the loader wave's own remainder. (audit-prod-report.md) |
| `node_desugar.go` | 63 | K1-IOU | parse-time plugin-verb sugar desugar — a kind-blind parse-family mechanism in the same K1 CUE/node-decode cluster as `cue_node.go`/`node_normalize.go`, not independently movable ahead of K1's own remainder. (audit-prod-report.md) |
| `node_parsed.go` | 133 | K1-IOU | host materialize-half (`ParsedNode` → `genericNode`) — the K1 node-decode split's core-resident half (its sibling, the dispatch orchestrator, already relocated to `sdk/loaderkit` in K1 unit 1). (audit-prod-report.md) |
| `unified.go` | 340 | K1-IOU | `LoadUnified` orchestration — K1-proper's own explicitly named remainder (per the K1 loader wave's verdict already cited by `layers.go`'s existing K1-IOU row above). (audit-prod-report.md) |
| `node_build.go` | 21 | B | own header: kept ONLY for `node_candy.go`'s `buildCandy` (bootstrap-critical) — "the discovered-candy pre-check in `unified.go` calls it directly, so it stays *genericNode-typed permanently" (self-declared B). The former entity-body assembler itself already relocated to `sdk/loaderkit.AssembleEntityBody` (K1 unit 3b); what remains is the bootstrap-coupled wrapper. Shrank 53->21 LOC since the audit. (own header, corroborated — OVERRIDES the audit's stale R/K1 call) |
| `node_normalize.go` | 50 | M | own header: "kind-decode SUPPORT helpers consumed by the TRUE clause-M dispatch (`provider_kind_invoke.go`'s `runPluginKind`/`foldSubstrateKind`)" — the former per-node DISPATCH ORCHESTRATOR already moved to `sdk/loaderkit` (K1 unit 1); what remains (`foldStandaloneTemplateReply`, `isStandaloneResourceKind`, the generic `ensureMap` helper) feeds the M dispatch directly, not independently movable business logic. Shrank 124->50 LOC since the audit. (own header, corroborated — OVERRIDES the audit's stale R/K1 call) |
| `cue_schema.go` | 54 | M | the KERNEL's own compiled base schema (`cueSchemaCtx`/`sharedCueSchema`), kept for exactly two in-core mechanisms: the plugin-schema SPLICE (`plugin_loader.go` unifies each plugin's served `schema_cue` onto this base to gate its authored input) and the structural-kind VALUE gate (`provider_kind_invoke.go`'s `validateKindValueCUE`). Both are clause-M (plugin loading / prescan-dispatch), so the schema they consume stays with them. K-wave 2 cone R1 (ruling 1) moved the kind-def table (`registerCueKind`/`cueKindDefs`, formerly fed by nine `cue_kind_<name>.go` init files), the `coreCueSchema()` handle constructor, and the six seam-forwarding wrappers to the LOADER, which owns the schema it validates against (`sdk/loaderkit/cue_schema.go`). Shrank 261->130->54 LOC. |
| `build_overlay.go` | 278 | M | own header: registers `registerHostBuilder(overlayBuilderKind, ...)` (see gate 2's reverse-channel whitelist) — `hostBuildOverlay` calls `candy/plugin-deploy-pod`'s own `InvokeProvider("build","generate",sdk.OpResolve,...)`, the SAME plugin-side `resolveBuildEngine` pipeline `build:box`/`build:generate` already run; the now-deleted `NewGenerator` reconstruction is gone. A wire-broker reverse-channel leg, not residue — shrank only 295->278 LOC since the audit (build-time coupling, not thinnable business logic). (own header, corroborated — OVERRIDES the audit's R/K3 call) |
| `bundle_add_cmd.go` | 214 | R-RESIDUE (exit: W3 unit A1-A8, deploy-dispatch cluster — /tmp/w3-scoping-map.md) | bundle add/del host residue (`deriveChildExecutorForPath` etc.) — the W3a deploy-dispatch residue cluster, not yet spiked/thinned. (audit-prod-report.md) |
| `bundle_from_box_cmd.go` | 89 | R-RESIDUE (exit: W3 unit A1-A8, deploy-dispatch cluster) | source-less deploy orchestration — same W3a deploy-dispatch cluster as bundle_add_cmd.go. (audit-prod-report.md) |
| `check_cmd.go` | 136 | R-RESIDUE (exit: W3b check unit, in progress) | own header: "the residual host-side check-project plumbing after the K1-unblock wave's 'live' and 'feature-live' arms moved to candy/plugin-check" — shrank 364->136 LOC as those arms already relocated; what remains is W3b's own remaining check-harness residue. (own header, corroborated; audit-prod-report.md) |
| `checkspec.go` | 107 | R-RESIDUE (exit: W3b check unit — /tmp/w3-adjudication.md 'Confirmed STAY' notes a rename-with-hygiene, not a relocation) | verb-catalog/do-mode grammar (check semantics) — prescan-consumed; w3-adjudication.md's "Confirmed STAY" list notes it stays with a hygiene rename (`op_vocabulary.go`), already flagged non-blocking in this manifest's checkspec.go rename-finding section above. (audit-prod-report.md; w3-adjudication.md) |
| `credential_plugin.go` | 213 | R-RESIDUE (exit: check_endpoint_resolve.go's B7 spike, not settled) | the credential adapter (VNC password resolve) that survived K5's partial dissolution (`credentialHealth`/`.health()` already deleted, per this manifest's K5 seam-death section above) — its one remaining caller, `check_endpoint_resolve.go`, is itself "STAY pending the B7 spike — not settled" per this same manifest; credential_plugin.go rides the same spike. Shrank 240->213 LOC via the K5-landed partial dissolution. (KERNEL_MANIFEST.md K5 seam-death section; audit-prod-report.md) |
| `deploy_target_dispatch.go` | 46 | R-RESIDUE (exit: W3a A9 spike — deploy_target_unified.go's interface-contract move candidate) | `dispatchDeployTarget` core-side S3b half — tightly coupled to `deploy_target_unified.go`'s interface set, itself flagged a CONTRACT MOVE candidate in w3-adjudication.md item 2 (spike in A9: move the interface set to spec). (audit-prod-report.md; w3-adjudication.md item 2) |
| `deploy_target_unified.go` | 222 | R-RESIDUE (exit: W3a A9 spike, w3-adjudication.md item 2) | the `UnifiedDeployTarget`/`LifecycleTarget` kind-agnostic interface contracts — w3-adjudication.md item 2 explicitly downgrades this from STAY-E to a CONTRACT MOVE candidate: "kind-agnostic interface contracts belong in spec, not core, once their only implementors/consumers span the boundary... type aliases FORBIDDEN — repoint. Whatever provably serves only core-internal wiring may stay." Not settled — A9's spike decides the final split. (w3-adjudication.md item 2) |
| `gpu_allocate.go` | 60 | EXCEPTION-GPU | own header: "the core-side GPU-resource PREREQ helpers that survive the P10 VM-CLI move... `bedGPUPrereqMissing`/`gpuPrereqMissing`, the pure resource-vocabulary predicate reached now via the narrow `host_build_check_bed_gpu_prereq.go` seam... GPU host-DETECTION being the project's explicitly operator-dropped exception, fenced from every K-wave cutover per `gpu_shim.go`'s own header." The named GPU-hardware exception CLAUDE.md and the north-star both carve out (no hardware to verify a relocation against). `requiredGPUResource` (dead code, its only caller already deleted) was removed in K-wave W3 unit A1. Shrank 85->60 LOC accordingly. (own header, corroborated) |
| `host_build_arbiter_bracket.go` | 45 | R-RESIDUE (exit: W3a A1-A8 arbiter cluster, feeds preempt.go's arbiterProxy — confirmed STAY, w3-adjudication.md) | `arbiter-bracket-acquire`/`arbiter-bracket-release` HostBuild handlers — w3-adjudication.md's "Confirmed STAY" list names `host_build_arbiter_bracket.go` explicitly ("process-env invariant"), so this is a STAY-classed HostBuild registration (see gate 2's whitelist), not thinnable residue in the current wave. (w3-adjudication.md 'Confirmed STAY') |
| `host_build_check_run.go` | 77 | R-RESIDUE (exit: W3b check unit, in progress) | check-run preflight HostBuild handler. (audit-prod-report.md) |
| `host_build_cli.go` | 90 | R-RESIDUE (exit: w3-adjudication.md 'Confirmed STAY' — clause-3 leg, textbook) | cli HostBuild re-entry handler — w3-adjudication.md's "Confirmed STAY" list names `host_build_cli.go` explicitly ("clause-3 leg, textbook") — leg 3 of the north-star's three legs (plugin-binary build + CLI reentry). (w3-adjudication.md 'Confirmed STAY') |
| `host_build_config_resolve.go` | 168 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | config-resolve HostBuild handler. (audit-prod-report.md) |
| `host_build_deploy_del_resolve.go` | 46 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | deploy-del-resolve HostBuild handler. (audit-prod-report.md) |
| `host_build_deploy_from_box.go` | 34 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | deploy-from-box HostBuild handler. (audit-prod-report.md) |
| `host_build_deploy_node_del_dispatch.go` | 56 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | deploy-node-del-dispatch HostBuild handler. (audit-prod-report.md) |
| `host_build_pod_config.go` | 107 | R-RESIDUE (exit: W3a unit B6 — per-leg death list, w3-adjudication.md item 3) | pod-config-* HostBuild handlers — w3-adjudication.md item 3 names this file explicitly in the per-leg death list (new unit B6, after A2 proves the peer-dispatch pattern): each leg dies individually (detect-devices -> plugin-deploy-pod peer InvokeProviders verb:gpu; ensure-image -> plugin drives podman itself; ssh-key -> plugin reads host FS itself). (w3-adjudication.md item 3) |
| `host_build_pod_config_seams.go` | 79 | R-RESIDUE (exit: W3a unit B6 — per-leg death list, w3-adjudication.md item 3) | narrow pod-config-* handlers post-collapse — w3-adjudication.md item 3's SAME per-leg death list (list-sidecars leg + `sidecar.go` embed -> the embed MOVES INTO `candy/plugin-deploy-pod`'s own `go:embed`; `hostEnvJSON`'s `os.Executable()` identity threads as DATA on the dispatch envelope, the #200 DATA-threading precedent — it does not anchor these seams). Each leg needs its own bed proof (check-pod); any leg that fails its spike gets an IOU, never a silent keep. (w3-adjudication.md item 3) |
| `host_build_pod_lifecycle_dispatch.go` | ~150 (A10-consolidated) | M — op-discriminated lifecycle dispatch | A10 LANDED (cc90441b): ONE hostBuildPodLifecycle handler + ONE registerHostBuilder("pod-lifecycle") replacing the 8 per-verb kinds; wire unified to #PodLifecycleRequest (A10b, d281dae). Measured honest delta net -24 across the 16-file blast radius; the win is the house op-discriminated idiom. |
| `host_build_resolve_target_add.go` | 125 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | resolve-target-add terminal step handler. (audit-prod-report.md) |
| `pod_lifecycle_dispatch.go` | ~200 (A10a ctxBox[T] compression) | M/D — hook registries + ctx-option threading | A10a LANDED (cc90441b): the 4 hand-rolled ctxKey triplets replaced by one ctxBox[T] generic; hook registries (registerLifecyclePlanHooks) stay — the registry-table M/D shape. |
| `pod_lifecycle_verb.go` | 76 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | start/stop verb dispatch to `LifecycleTarget`. (audit-prod-report.md) |
| `preempt.go` | 287 | R-RESIDUE (exit: w3-adjudication.md 'Confirmed STAY' — arbiterProxy process-env-gated; requiredGPUResource dead code already deleted W3 A1) | resource arbiter host-side lease/bracket glue — w3-adjudication.md's "Confirmed STAY" list names `preempt.go`'s `arbiterProxy` explicitly ("~250; process-env-gated"). Grew 258->287 LOC since the audit (unrelated churn), still the confirmed-STAY arbiter glue; not yet reclassified to a kernel-legal clause pending its own dedicated pass. (w3-adjudication.md 'Confirmed STAY') |
| `readiness_config.go` | 75 | R-RESIDUE (exit: w3-adjudication.md 'Confirmed STAY' — shrinks post-K1) | `ReadinessProvider` wiring (deploy capability data) — w3-adjudication.md's "Confirmed STAY" list names `readiness_config.go` explicitly ("shrinks post-K1"). (w3-adjudication.md 'Confirmed STAY') |
| `service_render.go` | 167 | R-RESIDUE (exit: W3a A1-A8 deploy-dispatch cluster) | service materialization host side + egress dispatch. (audit-prod-report.md) |
| `sidecar.go` | 43 | R-RESIDUE (exit: W3a unit B6 — list-sidecars leg + embed move, w3-adjudication.md item 3) | embedded sidecar template bodies (capability data) — w3-adjudication.md item 3: "list-sidecars leg + sidecar.go embed -> the embed MOVES INTO plugin-deploy-pod (its own go:embed)". (w3-adjudication.md item 3) |
| `substrate_template_resolve.go` | 95 | M | `resolveVmViaPlugin`/`resolveLocalViaPlugin`/`resolveAndroidViaPlugin` call `hostInvoke(ClassKind,...)` — the SAME `InvokeProvider`/registry kind-dispatch mechanism (leg 2) as `resource_resolve.go`/`distro_resolve.go`'s already-documented "M — kind-dispatch callback wrappers" rows above. Own header confirms the former typed aliases (ResolvedLocal/ResolvedAndroid/ResolvedK8s) are gone (W0) and `resolveK8sViaPlugin`'s caller relocated into K-wave W3a A3-phase-2 (every `kind:<word>` caller now self-loads plugin-side via `sdk/loaderkit`'s `Resolve{K8s,Vm,Android}EntityViaExecutor`). Shrank 114->95 LOC. (own header, corroborated — OVERRIDES the audit's stale R/K4 call; cross-references resource_resolve.go/distro_resolve.go's existing rows) |
| `unified_targets.go` | 541 | MIXED-THIN (exit: W3a unit A9, w3-adjudication.md item 1) — see the detailed per-symbol breakdown table below ('unified_targets.go — the pluginDeployTarget adapter') | w3-adjudication.md item 1: "STAY->MIXED-THIN. The pluginDeployTarget adapter + venueExecutor() re-materialization + ResolveTarget stay (M legs 1+2). But runUnifiedTargetChecks (~100 LOC), the per-verb option plumbing, and ledger glue THIN into plugin-bundle/spec" — unit A9's own thinning work (~60 LOC out) is already recorded in this manifest's dedicated per-symbol section below; that section is the authoritative per-symbol receipt, this row is the required top-level file pointer to it. (w3-adjudication.md item 1; this manifest's own 'unified_targets.go' section) |
| `update_deploy_dispatch.go` | 178 | R-RESIDUE (exit: confirmed STAY, no code moves — this manifest's own dedicated section above) | `charly update` deploy-name resolution + dispatch — this manifest ALREADY carries a full dedicated "update_deploy_dispatch.go — CONFIRMED STAY" section above (K-wave W3a unit A5): `dispatchByDeployTarget` calls two irreducible core-private mechanisms (`loadDeployPlugins`, `ResolveTarget`), confirm-and-close verdict, no code moved. This row is the required top-level file pointer into that section — R-RESIDUE is a misnomer here (see the dedicated section for the real M verdict), kept for gate bidirectionality only. |
| `vm_lifecycle_preresolve.go` | 46 | R-RESIDUE (exit: W4 K5-deferred, w3-adjudication.md 'Confirmed STAY' list: DEFERRED to W4 per the plan's own K5 assignment — not settled STAY) | VM F12 attach-resolver hook — w3-adjudication.md's "Confirmed STAY" list explicitly defers `vm_lifecycle_preresolve.go` (grouped with `vm_plugin_client.go` + `credential_plugin.go`) to W4's own K5 re-audit; W4 has landed (task #15) but this file's own re-audit disposition needs a fresh confirmation pass — flagging as the honest remaining state rather than inventing a W4 verdict that was never actually recorded for this specific file. (w3-adjudication.md 'Confirmed STAY') |
| `deploy_tree.go` | 63 | D | w3-adjudication.md's "Confirmed STAY" list: "deploy_tree.go (D; carry the scout's redundancy note as an R3 check)" — the recursive tree walker turns a BundleNode's children into a pre-order Emit()/post-order teardown sequence purely by TREE SHAPE, no per-kind business logic of its own (each per-target Emit() call is itself an M dispatch elsewhere). OVERRIDES the audit's original R/K4 call — w3-adjudication.md is the binding correction. (w3-adjudication.md 'Confirmed STAY', overriding audit-prod-report.md) |
| `commands.go` | 96 | R-RESIDUE (exit: not yet re-triaged post-W4 — isTerminal is a trivial host util, candidate for a future K5-adjacent sweep) | `isTerminal` host util — a tiny (96 LOC) CLI-root helper; the audit's K5 bucket, not yet individually re-triaged since W4's K5 sweep landed. (audit-prod-report.md) |
| `devices.go` | 55 | R-RESIDUE (exit: W3a unit B6, per this manifest's own K5 seam-death section above) | GPU/device detection data + helpers — this manifest's own "K5 seam-death" section above already documents this file precisely: "What remains is a genuine IOU, not a permanent floor: DetectHostDevices/EnsureCDI (host_build_pod_config_seams.go) plus LogDetectedDevices — consumers are HARD-FENCED files this cutover cannot touch... those two files' GPU legs are ALREADY slated for peer-InvokeProvider dispatch (seam leg dies) — landing B6 is what finally deletes these two files." Shrank 119->55 LOC via the already-landed partial dissolution. (KERNEL_MANIFEST.md 'K5 seam-death' section) |
| `gpu_shim.go` | 80 | R-RESIDUE (exit: W3a unit B6, per this manifest's own K5 seam-death section above) | this manifest's own "K5 seam-death" section above documents `devices.go` + `gpu_shim.go` together as "PARTIALLY dissolved": the alias-shaped re-export forms the original audit flagged as a gate-evasion finding are DELETED (W0); what remains is the SAME B6-gated IOU as `devices.go` above (`DetectVFIO`/`bedGPUPrereqMissing` reached via `host_build_check_bed_gpu_prereq.go`, already documented M above) plus the file's own fencing header naming it the operator-dropped GPU-hardware exception boundary. Shrank 134->80 LOC via the already-landed partial dissolution. (KERNEL_MANIFEST.md 'K5 seam-death' section) |
| `host_build_feature.go` | 77 | R-RESIDUE (exit: not yet re-triaged post-W4) | feature HostBuild handler (ADE `charly feature` command) — the audit's K5 bucket, not yet individually re-triaged since W4's K5 sweep landed. (audit-prod-report.md) |
| `host_build_retention_defaults.go` | 40 | R-RESIDUE (exit: not yet re-triaged post-W4) | retention defaults HostBuild handler — the audit's K5 bucket, not yet individually re-triaged since W4's K5 sweep landed. (audit-prod-report.md) |

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
- **EXCEPTION-GPU** — the project's explicitly operator-dropped GPU-hardware-DETECTION exception
  (no hardware to verify a relocation against); fenced from every K-wave cutover.
- **R-RESIDUE (exit: ...)** — the boundary law's own per-file test still classifies this file R
  (its behaviour genuinely belongs in a plugin), but it remains present in charly/ because the
  wave/unit/IOU that dissolves it has not landed yet. The `(exit: ...)` clause is MANDATORY (a
  bare `R-RESIDUE` with no named exit fails `TestKernelManifestBidirectional`'s clause-validity
  check) — residue is documented with its own dissolution path, never a place to stop looking.
  Re-audit whenever the named wave/unit lands; a file whose exit landed keeps its OLD row only
  long enough to gain its new kernel-legal (or further-thinned R-RESIDUE) clause in the SAME
  commit that lands the wave.
