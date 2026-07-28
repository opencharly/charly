# K47 — K1-LOADER RELOCATION — HANDOFF PACKAGE (working note, do NOT commit)

**Branch:** `feat/118-k47` (worktree `/home/atrawog/Atrapub/o/charly-wt-p12check`), off terminus `cd560ba9`.
**Folds into:** `feat/118-terminus` locally (operator ruling — NOT a separate PR). #118 = terminus, ONE PR, GREEN at end.
**Tier:** commit at `syntax check only`; team-lead OWNS the R10 bed (loader loads a project + deploys via the legs).

## GOAL (keystone scope, lead-approved)
Make `loaderkit.LoadUnified` runnable PLUGIN-SIDE via a reverse-channel executor, + ONE witness consumer
(`resolveTreeRoot` in candy/plugin-bundle calling `loaderkit.LoadUnified` directly). P13/P14/P15 collapses
are FOLLOW-ON units, not this one.

## CURRENT STATE (updated 2026-07-27, successor k47-loader) — ALL GREEN, buildable, uncommitted WIP
`loaderkit.LoadUnified(dir, LoadSeams)` ALREADY EXISTS (predecessor task #24 unit 2, `sdk/loaderkit/load_unified.go`)
— the orchestration is relocated; charly's `LoadUnified(dir)` (charly/unified.go) is a THIN wrapper building a
`loaderkit.LoadSeams` from 11 host callbacks. The keystone remainder = make those seams plugin-callable
(pure seams → loaderkit direct-call; host-coupled seams → executor dispatch), then witness.

### DONE + VERIFIED THIS SESSION (build+vet+test green; see PROOF below)
- **Slice 1 (predecessor):** DATA-driven descent stamp (`loaderkit.StampBundleDescents`), `spec.Threaded.DeployTraits`,
  `loaderkit.ValidateEphemeralUnified`+`ValidateVmNamingGuard`+`ValidateEphemeralOnNode` (`sdk/loaderkit/validate_ephemeral.go`,
  using `spec.Diagnostics` per RULING 2). Core `validate_ephemeral.go`+test DELETED.
- **Unit A ENTIRE pure-move phase (this session) — COMPLETE, all behaviour-identical, all verified green:**
  - `sdk/loaderkit/validate_check_beds.go` (NEW): `ValidateCheckBeds(uf, spec.Threaded)` + `ValidateIterateBed`.
    **EXACT-DATA (per lead's RDD directive — NOT a reconstruction):** the external-substrate check reads a new
    `spec.Threaded.ExternalDeploySubstrates` set that the HOST fills in `loaderThreaded()` by EVALUATING its own
    registry-live `isExternalDeploySubstrate` over every recognized substrate word — so loaderkit checks
    membership of the byte-exact host decision, never reconstructs the resourceKindSet ∧
    externalizedDeploySubstrates ∧ recognizedDeploySubstrate predicate. `spec.Threaded` gained the
    `ExternalDeploySubstrates map[string]bool` field (loader_seam.go — hand-written contract type, NOT generated,
    so no cue:gen). Core `validateCheckBeds`/`validateIterateBed` DELETED from unified.go; seam wired to
    `loaderkit.ValidateCheckBeds(uf, loaderThreaded())`. Tests repointed (android_loader_test,
    check_bed_run_test, plugin_agent_module_test).
  - `sdk/loaderkit/bundle_load.go` (NEW): the fully-pure, registry-free LOAD-half seams, all
    behaviour-identical relocations:
  - `SortedDeployKeys` / `SortedMemberKeys` (the two shared sort helpers).
  - `FlattenBundleVenues` (+ `flattenBundleOne`/`hoistVenueSubtree` unexported) + `VenueIsAgentProvisioned`
    (moved from charly/node_bundle_venue.go — **FILE DELETED**).
  - `FoldMembers` (moved from bundle_members.go LOAD-half).
  - `ValidateMembers` (+ `validMemberTarget`, registry-free: valid-target set = `spec.ResourceKinds` minus
    `"group"` — the SAME derivation core's `deployTargetWords` uses, so behaviour-EXACT, zero drift; covered by
    TestValidateMembers_AcceptsCanonicalSubstrates/RejectsGroup/AcceptsEmptyTarget).
  - charly rewired: `unified.go` LoadSeams now point `FlattenBundleVenues`/`FoldMembers`/`ValidateMembers` at
    `loaderkit.*`; `bundle_members.go` DEPLOY-half (bringUpMembers/tearDownMembers/isPodMember/isVmMember/
    withMemberTag) STAYS core, repointed to `loaderkit.SortedMemberKeys`; callers repointed:
    `host_build_check_run.go` (`loaderkit.VenueIsAgentProvisioned`), `host_build_check_bed.go`
    (`loaderkit.SortedMemberKeys`). Tests updated in-place to call `loaderkit.*`.
  - **R2 fix (predecessor's leftover):** `ephemeral_classification_test.go`'s `TestValidateVmNamingGuard`
    repointed to `loaderkit.ValidateVmNamingGuard` + `spec.Diagnostics`.
  - **P16 gate reconciled:** pruned STALE floor entry `node_bundle_venue.go` (reclassified: it's a loaderkit
    mechanism now, not core fabric) + STALE residue entry `validate_ephemeral.go`; R5-swept the stale
    "still-present validate_ephemeral.go" comment. Gate now **UNOWNED 0 / STALE 0**, FLOOR 102→101,
    RESIDUE 95→94 (the residue-count failure is the intended #118-GREEN tracker, unchanged in kind).

### PROOF (this session, GOWORK=off for sdk, workspace for charly)
- `GOWORK=off go build ./loaderkit/...` exit 0; `GOWORK=off go vet ./loaderkit/...` exit 0;
  `GOWORK=off go test ./loaderkit/... ./spec/... ./kit/...` exit 0.
- `go build ./...` (charly, workspace) exit 0.
- gofmt clean on all touched files.
- Full `go test ./charly/...` (132s, CONFIRMED this session): EXIT=1 with EXACTLY ONE failure —
  `TestKernelManifest_CoreIsPinnedToTheFabricFloor` (the by-design P16 residue-count tracker; now
  STALE 0 / UNOWNED 0 — a clean −1 FLOOR / −1 RESIDUE, no regression). Every other test PASSES.
- R5 grep self-test on deleted core identifiers (flattenBundleVenues/foldMembers/validateMembers/
  validMemberTarget/venueIsAgentProvisioned/sortedDeployKeys/sortedMemberKeys/hoistVenueSubtree/
  flattenBundleOne): no broken code refs (build proves it); remaining matches are prose describing
  still-true behaviour + the loaderkit relocation notes (unified.go LoadUnified doc-comment updated).

## LEFT (the keystone remainder) — ordered units

### Unit A remainder — the 3 validators that GENUINELY need the reverse-leg/callback (NOT pure)
The A-class PURE-move phase is DONE (validateCheckBeds included — the isExternalDeploySubstrate DATA equivalence
was RESOLVED + is behaviour-exact). The 3 below need Unit B/C infra (a resolve callback or sharedCueSchema), so
do them WITH the reverse-leg family, not before:
- **`validatePreemptibleUnified` (validate_preempt.go:73) → SPLIT:** the DATA half (nodeTraits→Threaded) →
  loaderkit; the `resolveVmViaPlugin`/`resolveResources` calls are registry (InvokeProvider) → C-leg.
- **`validateAndroidDevices` (unified.go:277) → C-leg:** `resolveAndroids` = `loaderkit.ResolvePluginKindViaPlugin(uf,
  "android", resolveAndroidViaPlugin)` — the resolve callback is registry-coupled (InvokeProvider). The
  `resolveAndroids` helper is already in loaderkit; the seam needs the host resolve callback → reverse leg.
- **GateDoc `validateNodeDocCUE` (cue_node.go) → loaderkit (lead U2):** needs `sharedCueSchema` (cue_schema.go) over
  the `sdk/schema` embed + `schemaconcat` (sdk-public). Check what `sharedCueSchema` drags before moving. NOTE:
  GateDoc is a WalkSeams field, NOT a LoadSeams field — it's reached through the WALK, not LoadUnified directly.
- `ValidationError` (charly/validate.go) STAYS core-transitional (still used by generate.go/validate_preempt.go/
  validate_project_host.go). RULING 2 already reused `spec.Diagnostics` for the moved ephemeral validators; the
  full `ValidationError`→spec migration is a later step, not this keystone.

### Unit B — reverse-leg host handlers (charly) — the genuinely-host seams
`registerHostBuilder` for: loader-bootstrap (wrap `runBootstrapPhase`), loader-connect-kinds (wrap
`prescanDeclaredPluginWords`+`connectDeclaredKindPlugins`, thread `inKindConnectPass` guard — U4), loader-threaded
(return `loaderThreaded()` snapshot), loader-resolve-ref (wrap `canonicalRef`), loader-materialize (wrap
`materializeLoadedProject` — RULING 1: TRANSITIONAL host leg, the whole embed+parser+registry orchestration;
returns MaterializedProject delta). **Design note (lead):** for the IN-PROC reverse legs (compiled-in only, U3),
prefer the existing TYPED `ProjectWalker`/`Materializer` seam pattern (spec/loader_seam.go — already has
`MaterializeSeams`, `WalkSeams` typed callbacks) over JSON HostBuild — cleaner, no marshal, same-process.

### Unit C — LoadSeamsViaExecutor + charly.LoadUnified thin wrapper
- loaderkit: `LoadSeamsFromExecutor(exec LoaderExecutor, threaded spec.Threaded) LoadSeams` (the pure seams wired to
  the moved loaderkit funcs directly; the host-coupled seams dispatch over the executor). Use a
  dependency-inverted `LoaderExecutor` interface in loaderkit (`InvokeProvider(...)` + `HostBuild(...)`);
  `sdk.Executor` satisfies it via a 1-line plugin-side adapter (spike-proven, compiles clean).
- charly.LoadUnified → thin TRANSITIONAL wrapper calling `loaderkit.LoadUnified(dir, hostExec)` where hostExec is an
  in-proc executor over the leg handlers (mirror the `inprocExecutorClient{srv: &executorReverseServer{}}` pattern —
  10+ existing call sites, e.g. build.go:178, oci_step_emit.go:43). Behaviour-preserving. DELETED by #118 GREEN
  (no permanent charly→loaderkit wrapper — IMPORT-PURITY end-state).

### Unit D — witness consumer (candy/plugin-bundle)
Replace the host `resolveTreeRoot` (charly/deploy_tree.go:99, 8 callers) usage in the plugin path with a
plugin-side `loaderkit.LoadUnified(dir, LoadSeamsFromExecutor(exec, threaded))` — proving
plugin-bundle→loaderkit.LoadUnified end-to-end. This is the R10 witness. (Full P13 collapse = follow-on.)

### DESIGN CLARIFIED (verified this session) + the ONE open decision
The B/C/D reverse-leg pattern is CONFIRMED by how candy/plugin-bundle ALREADY reaches LoadUnified-coupled
resolution: JSON-marshaled `HostBuild("resolved-project")` / `HostBuild("deploy-tree-resolve")` seams
(host_seams.go `hostDeploySeamJSON` — marshal req → `cmdExec.HostBuild(ctx, kind, reqJSON)` → unmarshal reply).
`LoadSeamsFromExecutor`'s COUPLED seams follow this EXACT pattern (marshal → HostBuild("loader-*") → unmarshal);
the reverse legs are `registerHostBuilder("loader-bootstrap"/"loader-walk"/"loader-materialize"/…)` wrapping the
existing charly funcs. WalkProject marshals `{dir, rootData}`→`spec.LoadedProject`; MaterializeLoadedProject
marshals `{lp}`→ the materialized UnifiedFile (host leg owns its own byID; plugin passes none). B/C/D is ONE
interdependent unit — verifiable only END-TO-END (first runtime test = charly.LoadUnified routing through the
in-proc executor; full test = the witness), NOT sub-verifiable in isolation — so build it in ONE fresh-context push.

**OPEN DECISION (confirm before building the legs):** the loader-leg wire request/reply types — CUE-source them
(sdk/schema, full SDD) vs. hand-write minimal typed structs (U3 says compiled-in-only + in-proc → typed/minimal
OK, precedent: the hand-written non-wire MaterializedProject/CandyRefs in loader_seam.go). team-lead's U3 note
leans typed-minimal; the SDD "wire types CUE-sourced without exception" mandate leans CUE. RESOLVE THIS FIRST —
it shapes every leg. (My read: these DO cross HostBuild's []byte so they're wire types → CUE-source per SDD,
unless team-lead rules them same-process-pipeline-state like MaterializedProject.)

### IMPLEMENTATION ORDER for B/C/D (successor — the novel bootstrap-critical build)
The A-class mechanical moves are DONE; B/C/D is the reverse-channel plumbing. Recommended order:
1. **loaderkit: `LoaderExecutor` interface** (dependency-inverted, no sdk-root import) —
   `InvokeProvider(ctx, class, word string, op string, params, env []byte) ([]byte, error)` +
   `HostBuild(ctx, kind string, spec []byte) ([]byte, error)`. `sdk.Executor` satisfies it via a 1-line
   plugin-side adapter (drop InvokeProviderOpts{}). Spike-proven (team-lead: compiled clean, zero charly imports).
2. **charly: host reverse-leg handlers.** For the COMPILED-IN in-proc path, PREFER the EXISTING typed seams
   (spec.WalkSeams / spec.MaterializeSeams / spec.ProjectWalker / spec.Materializer already exist +
   activeProjectWalker/activeMaterializer are registered — loader_threaded.go) over JSON HostBuild — no marshal.
   The host-coupled seams that need a leg: RunBootstrapPhase (runBootstrapPhase), WalkProject (hostWalkProject —
   already typed via activeProjectWalker), MaterializeLoadedProject (materializeLoadedProject — RULING 1
   TRANSITIONAL host leg), + the 3 not-yet-moved validators (validatePreemptible / validateAndroidDevices /
   GateDoc-in-WalkSeams). loaderThreaded() is the Threaded snapshot leg. U4: thread the inKindConnectPass
   guard through the connect leg (connectDeclaredKindPlugins re-enters LoadUnified).
3. **loaderkit: `LoadSeamsFromExecutor(exec LoaderExecutor, threaded spec.Threaded) LoadSeams`** — pure seams
   wired to the moved loaderkit funcs directly (FlattenBundleVenues/FoldMembers/ValidateMembers/
   ValidateEphemeralUnified(_, threaded)/ValidateCheckBeds(_, threaded)/StampBundleDescents(_, threaded)); the
   host-coupled seams dispatch over `exec`.
4. **charly.LoadUnified → thin TRANSITIONAL wrapper**: `loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(hostExec, loaderThreaded()))`
   where hostExec is an in-proc executor over the leg handlers — MIRROR the existing
   `sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}})` pattern (10+ sites: build.go:178,
   oci_step_emit.go:43, preempt.go:98, status_substrate_host.go:28, …). Behaviour-preserving; DELETED by #118 GREEN.
5. **Unit D witness (candy/plugin-bundle):** plugin-side `resolveTreeRoot` calling `loaderkit.LoadUnified(dir,
   loaderkit.LoadSeamsFromExecutor(exec, threaded))` — proves plugin→loaderkit.LoadUnified end-to-end. Hand to
   team-lead for the R10 bed (bootstrap + load + deploy via the legs) once green.

BOOTSTRAP CAUTION: LoadUnified runs on EVERY command. Verify the in-proc wrapper is behaviour-IDENTICAL (every
existing host command still loads) at each step — a bug here breaks everything. If you hit a bootstrap DEADLOCK
the compiled-in loader can't break, STOP and report (do not hack around it).

### Unit E — verify
build+vet+test all touched modules GOWORK=off (sdk/loaderkit, sdk/spec, candy/plugin-bundle, candy/plugin-loader)
+ `go build/test ./charly/...` under workspace. Gate test — paste RESIDUE/UNOWNED/STALE. Hand to lead for R10 bed.

## THE TWO CORRECTED RULINGS (unchanged)
1. **MATERIALIZE FORK:** keystone uses (a) — materialize+merge+embed orchestration as a TRANSITIONAL host reverse
   leg (loader-materialize); loaderkit.LoadUnified calls the leg; only the per-node DECODE is clause-M host. (b)
   FULL relocation of the materialize ORCHESTRATION into loaderkit is a MANDATORY tracked FOLLOW-ON (task #48).
2. **ValidationError → sdk/spec** (NOT loaderkit; charly's validate.go needs it, charly imports only spec).
   REUSE `spec.Diagnostics` (DONE for the ephemeral validators). validate.go stays transitional.

## HARD CONSTRAINTS (unchanged)
- Folds into feat/118-terminus locally — NEVER a separate PR. #118 = terminus, ONE PR, GREEN at end.
- IMPORT-PURITY end-state: charly keeps ONLY the reverse-leg handlers (import spec/proto); charly.LoadUnified as a
  loaderkit-importing wrapper is TRANSITIONAL (deleted by GREEN).
- HARD CUTOVER: no dual-mode; R5 grep self-test on deleted identifiers. Gate: UNOWNED 0 / STALE 0.
- Wire types CUE-sourced (SDD). For in-proc reverse legs (U3), prefer TYPED seams over JSON HostBuild.
- U4: thread the `inKindConnectPass` re-entrancy guard through the connect leg.
- Commit at HONEST tier `syntax check only` — ORCHESTRATOR owns the R10 bed. Do NOT inflate.
- BOOTSTRAP is the #1 risk. On a genuine bootstrap DEADLOCK, STOP and report — do not hack around it.

## RISK NOTES
- Loader is THE most bootstrap-critical path — a bug breaks every command. RDD on a bed (lead owns).
- `sdk/kit/endpoint.go` is a THROWAWAY SPIKE from ANOTHER effort (spike/substrate-endpoint, the resolveCheckEndpoint
  boundary leak), untracked in the sdk submodule — NOT part of K47. It builds fine (does not block) but should NOT
  ship in the terminus. Flag to whoever owns that spike / prune before #118 GREEN.
- LSP diagnostics in this worktree are NOISE — gopls indexes the MAIN worktree (/home/atrawog/Atrapub/o/charly),
  not this one, so every core symbol shows "undefined". Trust `GOWORK=off go build` / workspace `go build`, never
  the LSP diagnostics.

## ============ UNITS B/C/D — COMPLETE + VERIFIED GREEN (successor k47-legs, 2026-07-27) ============
Tier: `syntax check only` — ORCHESTRATOR owns the R10 bed (loader loads a project + deploys via the legs). NOT committed (folds into feat/118-terminus).

### What landed
- **Unit C (bootstrap-critical rewire):** `charly/load_executor_host.go` — `hostLoaderExecutor` implements the pre-existing `loaderkit.LoaderExecutor` (TYPED, zero-marshal, U3) with the 6 host funcs directly; `charly.LoadUnified` (unified.go) now drives `loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(hostLoaderExecutor{}))`. Every command's load now routes through LoadSeamsFromExecutor — bootstrap proven by full `go test ./charly/` (only the by-design manifest tracker fails).
- **Unit B (reverse legs):** `charly/host_build_loader.go` — 6 `registerHostBuilder` legs: `loader-bootstrap`/`loader-walk`/`loader-materialize`/`loader-threaded`/`loader-android-validate`/`loader-preempt-validate`, each wrapping the SAME host func Unit C calls directly. WIRE TYPE: only `loader-walk` needed a new envelope → `spec.LoaderWalkRequest{Dir,RootData}` CUE-sourced in `sdk/schema/seam.cue` + `task cue:gen` (reproducibility gate green). Every other leg carries an existing type verbatim (LoadedProject/Threaded/UnifiedFile marshalled directly — no spec↔loaderkit cycle since both import loaderkit).
- **Unit D (witness):** `candy/plugin-bundle/load_executor.go` — `execLoaderExecutor` implements `loaderkit.LoaderExecutor` over `sdk.Executor.HostBuild`, and `resolveTreeViaLoader` drives `loaderkit.LoadUnified` PLUGIN-SIDE end-to-end for `charly bundle add`. `walk.go` `BundleAddCmd.Run()` now calls it. The former host `deploy-tree-resolve` seam was cleanly repurposed → `deploy-plugins-connect` (host preamble: `loadDeployPlugins` + project dir only); `#DeployTreeResolveRequest/Reply` → `#DeployPluginsConnectRequest/Reply{dir}`. RootVenueSSH read plugin-side from the stamped `node.Descent.Venue=="ssh"` (byte-identical to nodeTraits for stamped nodes).

### U4 (re-entrancy guard) — preserved BY DESIGN, no new leg
`connectDeclaredKindPlugins` + `inKindConnectPass` run inside `hostWalkProject`'s `Boundary` seam (host-side) within the single `loader-walk`/WalkProject leg. The nested re-entrant load stays host-side (in-proc typed path); the guard is a host-process var — never crosses the wire. (The predecessor's refined LoaderExecutor folds connect into WalkProject — safer than a separate connect leg.)

### PROOF (all green)
- sdk (GOWORK=off): `go build ./...` + `go vet ./spec ./loaderkit` + `go test ./loaderkit ./spec` OK; `TestGenReproducible` OK (cue:gen reproducible).
- charly (workspace): `go build ./charly` OK; `go vet ./charly` OK; full `go test ./charly/` — ONLY `TestKernelManifest_CoreIsPinnedToTheFabricFloor` fails (by-design residue tracker).
- plugin-bundle (workspace): build + vet + test OK.
- Gate: RESIDUE 96 / UNOWNED 0 / STALE 0 (was 94; +2 = load_executor_host.go + host_build_loader.go, both classified P15). host_build_deploy_tree_resolve.go floor entry renamed → host_build_deploy_plugins_connect.go.
- R5 grep: `DeployTreeResolve`/`deploy-tree-resolve` → CLEAN (only CHANGELOG). gofmt clean on all touched files.

### FOR THE ORCHESTRATOR
Run the R10 bed (load a project + `charly bundle add` via the legs — the witness path). This is the ONLY runtime gate left; the code is behavior-preserving through the wrappers. sdk changes: seam.cue + regenerated cue_types_gen.go (+ predecessor's loader_seam.go Threaded ext + the 5 new loaderkit files). Follow-on #48 (materialize orchestration → loaderkit) unchanged.

## ============ FOLLOW-UP (k47-legs, team-lead review round) ============
### Point 1 — Unit D is NOT dual-mode (confirmed)
walk.go `BundleAddCmd.Run()` has a SINGLE tree-resolution path: resolveTreeViaLoader → loaderkit.LoadUnified (plugin-side). The old `deploy-tree-resolve` host builder is DELETED; no tr.Tree, R5-grep clean.
The host `resolveTreeRoot` (charly/deploy_tree.go) is NOT deleted because it has 6 OTHER non-witness callers — check_cmd.go, check_venue.go (×2), update_deploy_dispatch.go, host_build_pod_config_seams.go, host_build_deploy_entity_resolve.go — all DIFFERENT host-side operations (check-live gather, update, pod-config, kind:local template lookup). So deploy_tree.go stays P13 residue (dissolves when THOSE host commands move to plugins — broader P13/P14 scope, not this cutover). For the `charly bundle add` witness operation specifically there is exactly ONE path now.

### Point 2 — leg classification SPLIT (honest gate, residue→0 reachable)
host_build_loader.go was SPLIT by GREEN-reachability:
- **host_build_loader_floor.go → FLOOR** (permanent M/D a plugin-side loader ALWAYS calls back for):
  - loader-bootstrap → runBootstrapPhase (M — bootstrap-phase PLUGIN DISPATCH; bootstrap_phase.go is floor)
  - loader-walk → hostWalkProject (M — prescanDeclaredPluginWords + connectDeclaredKindPlugins plugin-loading/prescan-dispatch)
  - loader-threaded → loaderThreaded (D — registry-derived kind-recognition snapshot; loader_threaded.go is floor)
- **host_build_loader.go → RESIDUE (P15)** (transitional, dissolves as loader moves into its plugin):
  - loader-materialize → materializeLoadedProject (materialize ORCHESTRATION, RULING 1(a); per-node kind-DECODE M is separate = provider_kind_invoke.go floor; moves to loaderkit at #48)
  - loader-android-validate → validateAndroidDevices (capability validator R → moves to loaderkit via generic Threaded/InvokeProvider)
  - loader-preempt-validate → validatePreemptibleUnified (same)

### Gate math (net delta from this cutover)
- RESIDUE: 94 → 96 (+2: load_executor_host.go + host_build_loader.go[transitional-only]). UNOWNED 0 / STALE 0.
- FLOOR: +1 net (host_build_loader_floor.go added; host_build_deploy_tree_resolve.go → host_build_deploy_plugins_connect.go is a rename, net 0).
- residue→0 GREEN now reachable: the 3 permanent legs are floor; only the 3 transitional legs + the 2 wrapper bridges remain to dissolve.

### PROOF (all green, this round)
- charly: build + vet OK; full `go test ./charly/` (133s) — ONLY TestKernelManifest fails (by-design tracker).
- sdk GOWORK=off: build ./... + vet ./spec ./loaderkit + test ./loaderkit ./spec OK.
- candy/plugin-bundle: build + vet + test OK. candy/plugin-loader: build + vet OK (no test files).
- gofmt clean; R5 grep clean.

## ============ R10-BED REGRESSION FIX (k47-legs, R1 RCA + R2 same-cutover) ============
### Bug (R10 bed step 3, deploy-add witness)
`kind:check bed "check-agent-local" references local entity "check-feature-app" which is not defined`
(sdk/loaderkit/validate_check_beds.go:64). Host-side `charly box validate` PASSES; plugin-side `charly bundle add` FAILS — only difference = the executor (in-proc hostLoaderExecutor vs plugin-side execLoaderExecutor over the reverse legs).

### RCA (verified by reading code, not just the bed log)
`UnifiedFile.PluginKinds` is tagged `yaml:"-" json:"-"` ("Host-internal — never serialized"). A `local:` template folds into `uf.PluginKinds["local"][name]` (uf.Local()/VM()/Android()/Pod()/K8s() all read PluginKinds[disc]). My loader legs round-tripped the WHOLE UnifiedFile through plain json.Marshal → PluginKinds SILENTLY DROPPED plugin-side. The check-bed validator (line 64: `uf.PluginKinds[node.Target][node.From]`) then saw an empty map → false "not defined". Namespaces + RootDir survive (they carry only `yaml:"-"`, no `json:"-"`), so PluginKinds was the ONLY dropped field. This hit ALL THREE legs that cross a UnifiedFile: loader-materialize (reply → ValidateCheckBeds), loader-android-validate (uf.Android()), loader-preempt-validate (uf.VM()).

### Fix (byte-identical UnifiedFile transfer)
NEW sdk/loaderkit/materialize_wire.go: MarshalMaterialized/UnmarshalMaterialized — carry PluginKinds out-of-band (recursively: root + every mounted namespace, so a namespaced entity survives too) alongside the JSON-round-tripped UnifiedFile. Wired into all three legs:
- host_build_loader.go: hostBuildLoaderMaterialize → MarshalMaterialized; hostBuildLoader{Android,Preempt}Validate → UnmarshalMaterialized.
- candy/plugin-bundle/load_executor.go: MaterializeLoadedProject → UnmarshalMaterialized; validateLeg → MarshalMaterialized.
The in-proc TYPED path (charly.LoadUnified / hostLoaderExecutor) is UNAFFECTED — it never serializes (mutates one UnifiedFile in place), so host-side stays byte-identical as before.

### Test (check-coverage: FAILS without the fix)
sdk/loaderkit/materialize_wire_test.go — TestMaterializedWire_PreservesPluginKinds asserts (a) plain json round-trip DROPS PluginKinds (precondition — proves the bug), (b) MarshalMaterialized preserves it; TestMaterializedWire_PreservesNamespacedPluginKinds covers the recursive namespace case. Both PASS.

### PROOF (all green)
- sdk GOWORK=off: loaderkit + spec build/vet/test OK (incl. the 2 new round-trip tests).
- charly: build+vet OK; full `go test ./charly/` — ONLY TestKernelManifest fails (by-design tracker).
- candy/plugin-bundle: build+vet+test OK. gofmt clean.
- Gate unchanged: RESIDUE 96 / UNOWNED 0 / STALE 0 (materialize_wire.go is a NEW sdk/loaderkit file, not charly — no gate impact).
READY for R10 bed re-run (team-lead owns). The plugin-side load now reconstructs PluginKinds byte-identically, so check-agent-local resolves check-feature-app.
