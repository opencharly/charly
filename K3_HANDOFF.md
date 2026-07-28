# K3 — Build-Engine Envelope-Out: RDD HOW-Spike Handoff

**Status:** HOW verified + accepted (team-lead). Ready for a fresh full-budget implementer.
**Baseline:** worktree `feat/118-k3-buildengine` @ `e4678701` (K1-LOADER keystone), sdk `176b6a8`. `go build ./...` GREEN.
**Sequencing ruling:** K3 lands **BEFORE** #48. Keep `MaterializeLoadedProject` a transitional host leg (exactly as the K1 witness does). #48 (materialize orchestration → loaderkit) is only a leg-count reducer, not a blocker.
**Verdict:** **YES** — the build-engine RESOLVE is plugin-callable as a mechanical extension of K1-LOADER. **No new mechanism, no new spike.**

Every claim below was read at file:line. Independently re-verified by team-lead: buildkit imports no charly/loaderkit; `charly.ResolveBox` (config.go:181) is a thin wrapper delegating to `buildkit.ResolveBox` (only `fillBuildConfigFallback` is host); `deploykit.Compute*` are pure sdk.

---

## §1 — HOW: the build-engine RESOLVE becomes plugin-callable

The build ENGINE sits ATOP the loader, and K1 already made the loader plugin-callable. The resolve is a **pure composition of already-plugin-importable primitives**:

- `loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(exec))` → `*UnifiedFile` → `.ProjectConfig()` → `*spec.Config` — PLUGIN-SIDE via reverse legs (K1, landed).
  - Witness: `candy/plugin-bundle/load_executor.go` (`execLoaderExecutor`) + `walk.go` (`resolveTreeViaLoader`) already drive `loaderkit.LoadUnified` end-to-end from a genuine out-of-module plugin. `sdk/loaderkit/load_unified.go:108`, `unified_file.go:159` (`ProjectConfig`).
- `buildkit.ResolveAllBox(cfg, tag, dir, opts)` / `ResolveBox(...)` — PURE sdk over `*spec.Config` (`sdk/buildkit/config_resolve.go:56,347`). buildkit imports neither loaderkit nor charly (verified).
- `deploykit.ComputeIntermediates` / `GlobalCandyOrder` / `ComputeEffectiveVersions` / `ResolveBoxOrder` / `ResolveBoxLevels` — PURE sdk (`sdk/deploykit/intermediates_compute.go:57`, `intermediates.go:75`, `effective_version.go:22`, `graph.go:319,460`).
- charly's `ResolveBox`/`ResolveAllBox` (`config.go:181/191`) are ALREADY thin wrappers delegating to `buildkit.ResolveBox`; the only host bit is `fillBuildConfigFallback` (a `LoadBuildConfigForBox` reload), which vanishes once the plugin loads the project itself.

**Therefore the entire `NewGenerator` body** (`generate.go:130` — LoadConfig → ScanAllCandy → loadProjectPlugins → validateProjectForBuild → ResolveAllBox → ComputeIntermediates → GlobalCandyOrder → ComputeEffectiveVersions) **can run PLUGIN-SIDE in `candy/plugin-build`**, reaching the host only for the genuinely-coupled legs — exactly as the loader did.

**Current shape (to invert):** today plugin-build asks the host to do everything via ONE fat `HostBuild("build-prep")` → `hostBuildBuildResolve` (`build_resolve_host.go:44`), which runs `NewGenerator` host-side and ships back the resolved-project envelope. The render DRIVE already moved plugin-side (#67, `candy/plugin-build/render.go`). The resolve is the last host-resident half.

---

## §2 — Per-file classification

### MOVE → candy/plugin-build (the resolve body — pure composition the plugin runs)

| File | What moves | Note |
|---|---|---|
| `charly/config.go` (212) | ResolveBox/ResolveAllBox/ResolveOpts wrappers | buildkit delegate already sdk; `fillBuildConfigFallback` DIES (plugin loads vocab itself) |
| `charly/generate.go` (632) | `NewGenerator` + host-fs prep: `cleanStaleBuildDirs`, `writeContextIgnore`, `createRemoteCandyCopies`, `emitBakedPlugins`, remote-candy-copy helpers (`remoteBuildConfigCacheRoot`/`materializeBuildConfigAsset`/`rewriteHeaderCopyForRemote`/`candyByName`) | candy runs on the SAME host — pure fs/exec, no registry. `resolveUserContext` already calls verb:oci (`invokeOciInspectUser`) |
| `charly/build_resolve_host.go` (251) | `hostBuildBuildResolve` → plugin-side `resolveBuildEngine` | host keeps only thin legs (see §4) |
| `charly/resolved_project_host.go` (658) | `projectResolvedProject*`, `fillNamespacedBoxes`, `fillBoxPlans`, `projectTemplates`, `projectResolvedBox`, `projectBoxAggregates` | pure projection over cfg+layers+uf — all now plugin-reachable |
| `charly/layers.go` (scan ~300: `ScanAllCandyWithConfigOpts`/`scanCandyFromLocal`/`scanLocalCandies`) | candy scan + per-entity-version arbitration | ONE host dep: `EnsureRepoDownloaded` (git clone/cache) → reach via existing `kit.RefsDownloader` seam (P7 precedent) |
| `charly/distro.go` (95) / `distro_resolve.go` (24) | distro cascade resolve | pure over resolved graph |
| `charly/builder_preresolve.go` (97) | builder preresolve | pure over resolved graph |
| `charly/tasks.go` (239, `emitTasks` shim) | already a thin shim to `deploykit.Generator.EmitTasks` | stays only for the pod-overlay path per its header — pure |

### REVERSE-LEG (host-coupled, reached via a small `buildengine-*` leg family paralleling `loader-*`)

| Construct | Why a leg | Leg |
|---|---|---|
| `loadProjectPlugins` / `collectReferencedPluginWords` (`plugin_loader.go:634`) | build-time plugin CONNECT needs the provider REGISTRY (kernel M) | `buildengine-connect-plugins` |
| `validateProjectForBuild` (`validate_project_host.go:278`) | ALREADY a plugin dispatch (command:validate); its own comment names exit **"K3"** | becomes a plugin↔plugin `InvokeProvider` — trivial |
| render-seam callbacks (`host_build_render_seam.go`) | `EmitPluginOp` = package-main type-assert `ProvisionActor`/`BuildEmitter` (**genuine floor**); `EnsureBuilders`/`InlineBuilder` = registry connect (leg) | already isolated in the existing `render-seam` host-builder |
| `dispatchBuildEnsure` (`dispatch_build_ensure.go`) | already a thin host helper dispatching TO plugin-build's `build:ensure` word | stays as-is (reverse direction) |

### STAYS-FLOOR (genuine kernel M — never moves)

- The provider REGISTRY + in-proc reverse channel + `HostBuild`/`InvokeProvider` broker (plugin-loading M).
- `EnsureRepoDownloaded`/`ResolveProjectRepo` refs-git host coupling — served THROUGH `kit.RefsDownloader`, not core-resident logic (K1 precedent).
- `charly/engine.go` `ResolveBoxEngine` — needs `*Config`+layers; its header self-classifies **UNTIL-K4** (moves with the deploy/config cone, not K3). Leave it.
- `charly/builder_venue.go` — venue BuilderStep exec; deploy-path, moves with K4. Its injected `dispatchBuildEnsure`+`resolveImageRefForEnsure` closures already work cross-boundary.

---

## §3 — What K3 unblocks

- **P13** (#38, ResolveBox/dispatchBuildEnsure callers): once the resolve is a plugin capability, `box_inspect_overlay.go:40`, `bundle_add_cmd.go:323`, `host_build_box_ref_resolve.go:45,127`, `image.go:95` repoint from in-core `ResolveBox` to a plugin↔plugin `InvokeProvider` — the last thing keeping those call sites in core.
- **P14** (#39, image/remote_image/transfer/box_inspect): all four are ResolveBox/dispatchBuildEnsure CALLERS (grep-confirmed: `image.go:95,100,103`, `remote_image.go:68`, `transfer.go:48`, `box_inspect_overlay.go:40`); they collapse the moment the resolve leaves core.
- **P8b**: the build-prep host seam (`build_resolve_host.go`) inverts from "host runs NewGenerator" to "plugin runs resolve, host serves legs" — deleting the fat seam, leaving the thin `buildengine-*` legs.

---

## §4 — The mechanism: `resolveBuildEngine` + `buildengine-*` legs

Extend the K1 pattern verbatim.

1. **New plugin-side entry in `candy/plugin-build`** — `resolveBuildEngine(ctx, ex, req)`:
   ```
   uf, ok, err := loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(exec))   // K1 reverse legs
   cfg := uf.ProjectConfig()
   layers := <scan, plugin-side>                                                       // via kit.RefsDownloader for fetch
   <connect build-time plugins>                                                        // buildengine-connect-plugins leg
   <validate>                                                                          // InvokeProvider → command:validate
   boxes  := buildkit.ResolveAllBox(cfg, tag, dir, opts)                               // pure sdk
   boxes  = deploykit.ComputeIntermediates(boxes, layers, cfg, tag)                    // pure sdk
   order  := deploykit.GlobalCandyOrder / ResolveBoxOrder(boxes, layers)              // pure sdk
   _      = deploykit.ComputeEffectiveVersions(boxes, layers)                          // pure sdk
   // → build the resolved-project envelope plugin-side, then render (render.go, already plugin-side)
   ```
2. **New thin host legs** in a `host_build_buildengine.go`, mirroring `host_build_loader.go`'s `loader-materialize`/`loader-walk`/`loader-*` registrations:
   - `buildengine-connect-plugins` (registry connect — `loadProjectPlugins`).
   - Reuse existing: `render-seam` (EmitPluginOp/EnsureBuilders/InlineBuilder), `kit.RefsDownloader` fetch leg, `command:validate` via InvokeProvider, `verb:oci` for the external-base user probe.
3. **Reference implementation to copy:** `candy/plugin-bundle/load_executor.go` (`execLoaderExecutor` — the K1 witness) + `charly/host_build_loader.go` (the host legs). Same shape, one level up the stack.

The `build-prep` fat seam is then deleted; `hostBuildBuildResolve` shrinks to the leg set.

---

## §5 — K3-before-#48 note (sequencing)

- K3 can land **before** #48. TODAY `MaterializeLoadedProject` is a **transitional host leg** (RULING 1, `sdk/loaderkit/load_executor.go:36-38`); the K1 witness (`candy/plugin-bundle/load_executor.go:69`) already keeps it as a host leg and works end-to-end. K3 inherits that same leg — so #48 (materialize orchestration → loaderkit) is only a **leg-count reducer**, never a hard blocker.
- **No doc drift found.** `build_resolve_host.go`, `generate.go`, and `validate_project_host.go:277` ("Named exit K3") all already anticipate this exact move; the skills/comments are accurate.
- **LOC:** the resolve cluster (config.go 212 + generate.go 632 + build_resolve_host.go 251 + resolved_project_host.go 658 + layers.go-scan ~300 + distro/builder ~200) is **~2.2k+ gross host LOC** that becomes plugin-side; net core reduction clears the ≥2,000-LOC wave floor once combined with the P13/P14 caller repoints.
- **R10:** no bed needed for the HOW (composition compiles from proven primitives). A bed IS needed for the eventual R10 — build a real box through the plugin-side resolve (a `box/<distro>` bed or a build-engine bed). Orchestrator owns bed selection/run.

---

*Spike by k3-buildengine-spike. Throwaway — no code shipped; worktree left clean on the keystone HEAD. This handoff is the durable deliverable for the fresh implementer.*

---

## §6 — IMPLEMENTATION VERIFICATION (k3-impl, 2026-07-27) — RDD re-derivation of the HOW

Per "never trust, verify," every §1–§4 claim was re-read at file:line (+ two deep-mapping agents on the projection and the scan). **The composition mechanism (§4) is sound, but the spike's "already-plugin-reachable / pure projection / no new mechanism, mechanical extension of K1" framing is materially optimistic.** `buildkit.ResolveAllBox` + `deploykit.Compute*` being pure is necessary but FAR from sufficient: FOUR more package-main clusters must become plugin-reachable before §4's body compiles plugin-side, and the scan + two projection legs require GENUINELY NEW reverse legs (not just reuse of the K1 loader legs). Verified facts:

### What is genuinely plugin-ready (good news, confirmed)
- `loaderkit.LoadUnified(dir, LoadSeamsFromExecutor(exec))` → `uf` → `uf.ProjectConfig()` — DONE (K1). The walk leg folds `discover:` into `uf.Candy` already.
- `buildkit.ResolveAllBox`/`ResolveBox` — pure sdk (config_resolve.go:56/347). `config.go` wrappers are thin; `fillBuildConfigFallback` dies when the plugin supplies `DistroCfg/BuilderCfg`.
- `deploykit.ComputeIntermediates`/`GlobalCandyOrder`/`ResolveBoxOrder`/`ResolveBoxLevels`/`ComputeEffectiveVersions` — pure sdk.
- `ResolveBoxEngine` (engine.go:35) + `CollectBoxAlias` (alias_collect.go:43) are **internally PURE** (only `cfg.BoxConfig` + `deploykit.*` + `layer.*`) → movable to deploykit; the handoff's STAYS-FLOOR-UNTIL-K4 for engine.go refers to the DEPLOY-side `ResolveBoxEngineForDeploy`, NOT this build-side one. `ComputeIntermediates` (intermediates_shim.go) + `ComputeCalVer` (version.go) — pure.
- The envelope PROJECTION CORE (`projectResolvedBox`/`rawCandyPair`/`projectResolvedProject[WithBoxes]`/`projectBoxAggregates`/`fillBoxPlans`/`projectTemplates`/`fillNamespacedTemplates`) is PURE over `(cfg,layers,uf,distroCfg,builderCfg,initCfg,dir,version,opts,preResolvedBoxes)` — movable to deploykit.
- render-prep (`renderPrepBox`/`globalOrderForBox`/`buildBakedMetadata`) is mostly pure (deploykit/buildkit collectors over `g.Candies`); it needs `*Generator` field access only (no registry).

### The FOUR package-main clusters that must relocate first
1. **Build-vocab projections** — `ProjectDistroConfig/BuilderConfig/InitConfig` (unified.go:294-321) + their `Distros/Builders/resolveInits` decoders (read `uf.PluginKinds`). Small; → loaderkit (adds a loaderkit→buildkit import edge — verify no cycle) or a thin build-vocab kit.
2. **The candy SCAN** — `ScanAllCandyWithConfigOpts`/`scanCandyFromLocal`/`projectCandiesScanned` (layers.go:588/608, unified.go:340). **Dominated by the loader/registry cone, NOT git**: `requireCandyScanner().ScanCandyManifest/ScanRemoteCandy` + `parseCandyYAML` (registry + core `cueSchemaCtx`) + `CollectRemoteRefsOpts`'s `resolveLocalViaPlugin` (plugin-kind resolve). Git primitives are already in sdk/kit; the narrow git leg is `EnsureRepoDownloaded`'s `autoMigrateCacheProjectOnly` (`providerRegistry.resolve(command:migrate)+Invoke`, refs.go:257) + the registered `activeRefsDownloader` swap. **This cluster is K1-loader-sized and needs a NEW candy-materialize/scan leg** — LoadSeamsFromExecutor does NOT cover per-candy `ScanCandyManifest` (that cone is separate from LoadUnified's walk).
3. **render-prep** — render_prep.go + buildBakedMetadata (435 LOC) + globalOrderForBox → deploykit (pure, on `*Generator` fields → thread as inputs).
4. **The envelope projection** (resolved_project_host.go, 658) → deploykit, with TWO hard legs: (a) `resolveResources(uf)` = per-node `InvokeProvider(ClassKind,"resource",OpResolve)` → NEW InvokeProvider leg; (b) `fillNamespacedBoxes` EMBEDS a second filesystem scan + git-fetch + the render-prep engine mid-projection → must be delivered as PRE-computed inputs (mirror the existing `preResolvedBoxes` seam) rather than re-scanning inside the projection.

### NEW reverse legs required (contradicts "no new mechanism")
- `buildengine-connect-plugins` (loadProjectPlugins — registry connect) — §4 already names this.
- A candy-scan/materialize leg (ScanCandyManifest/ScanRemoteCandy + parseCandyYAML) OR make the CandyScanner plugin-served.
- `resolveResources` → InvokeProvider(ClassKind,"resource") leg.
- The fetch-orchestration migrate-invoke (autoMigrateCacheProjectOnly) — reuse/extend the refs seam.
- The render-seam floor legs (EmitPluginOp/EnsureBuilders/InlineBuilder) SURVIVE via `loadRenderGen`'s existing fresh-`NewGenerator` fallback — so `NewGenerator` STAYS host-resident for the render-seam floor even after the resolve inverts (the plugin holds the ResolvedBox; the host builds a minimal gen lazily for the registry/package-main type-assert only). This is the cleanest inversion but means NewGenerator does NOT fully leave core.

### Proposed ordered, independently-verifiable decomposition (each build+vet+test green)
- **U1** vocab projections → loaderkit (or build-vocab kit). Verify.
- **U2** envelope-projection CORE + ResolveBoxEngine/CollectBoxAlias/ComputeCalVer/ComputeIntermediates-shim → deploykit (keep charly wrappers). Verify.
- **U3** render-prep → deploykit (inputs-threaded). Verify.
- **U4** candy scan → a kit + the candy-materialize leg + fetch-orchestration leg. Verify. (Largest/riskiest — K1-sized.)
- **U5** resolveResources InvokeProvider leg + fillNamespacedBoxes as pre-computed-inputs. Verify.
- **U6** plugin-side `resolveBuildEngine` (candy/plugin-build) + `buildengine-*` legs; INVERT build-prep; delete the fat seam. Verify END-TO-END (bootstrap-critical — every build).
- **U7** repoint P13/P14 callers (box_inspect_overlay/bundle_add_cmd/host_build_box_ref_resolve/image/remote_image/transfer) to the plugin resolve via InvokeProvider; drop their residueOwner entries. Verify. Residue DROPS here.
- **U8** delete dead code + R5 grep sweep + P16 gate reconcile (UNOWNED 0 / STALE 0, residue drop).

### Sequencing collisions to confirm with the orchestrator
- U2/U3 target deploykit which #48 (materialize→loaderkit) and the pending P13/P14 (#38/#39) also touch.
- The render-seam-NewGenerator-stays decision interacts with K4 (engine.go / deploy-side resolve).
- All U1–U6 edits span the **sdk submodule** (loaderkit/deploykit/buildkit) → gitlink coordination, folds into feat/118-terminus.

*Verified by k3-impl. Surfaced to team-lead; SCOPE ACCEPTED + 3 rulings (target kits: deploykit for projection-core/render-prep/engine/alias, loaderkit for scan+vocab; #48/P13/P14 deferred behind K3 so k3-impl is sole thread on deploykit/loaderkit; NewGenerator STAYS host-resident for the render-seam FLOOR — classify FLOOR).*

---

## §7 — EXECUTION LOG (k3-impl)

### U1 — build-vocab projections → loaderkit — DONE, GREEN (2026-07-27)
- NEW `sdk/loaderkit/vocab.go`: `ProjectDistroConfig(uf, resolveDistro)` / `ProjectBuilderConfig(uf)` / `ProjectInitConfig(uf, resolveInit)` — the SHARED build-vocab projections both charly core and candy/plugin-build call (R3). The opaque distro/init bodies resolve via a caller-supplied OpResolve callback (charly passes its in-proc registry `resolveDistroViaPlugin`/`resolveInitConfigViaPlugin`; plugin-build will pass InvokeProvider-backed callbacks in U6). Builder bodies decode purely. loaderkit→buildkit edge is acyclic (buildkit imports only kit).
- charly: DELETED the 3 `Project*Config` wrappers (unified.go); repointed `LoadBuildConfigForBox` (format_config.go) + `init_def_label_test.go` to `loaderkit.Project*Config(uf, <cb>)`. `Distros`/`Builders`/`resolveInits` raw accessors STAY (bind charly's registry callbacks; read by tests). Dropped now-unused buildkit import from unified.go.
- PROOF: `GOWORK=off go build+vet+test ./loaderkit ./spec ./buildkit` OK; workspace `go build -o /dev/null ./charly` + `go vet ./charly` exit 0; U1 tests (InitDefLabel/EmbeddedDefaults/Unified) pass; gofmt clean; R5 grep — charly Project*Config wrappers GONE. charly −19 net LOC.

### U2 target-kit RESOLUTION (cycle-forced split of team-lead's deploykit ruling)
team-lead ruled "deploykit for projection-core", but the VERIFIED constraint is deploykit↛loaderkit (loaderkit→deploykit→buildkit→kit), and the projection reads `*loaderkit.UnifiedFile` (`uf.Bundle`/`uf.PluginKinds`/`uf.Local()`/`uf.Namespaces`). No-new-kit ruling stands. So U2 SPLITS by boundary-law placement:
- **uf-FREE helpers → deploykit** (team-lead's preferred home): `projectResolvedBox`, `rawCandyPair`, `projectBoxAggregates` (over cfg+layers+ResolvedBox), `fillBoxPlans` (over cfg+layers), + the pure movers `CollectBoxEngine`/`CollectBoxAlias`/`ComputeIntermediates`/`ComputeCalVer`.
- **uf-COUPLED recursion → loaderkit** (loader-specific per team-lead's own "loader-specific → loaderkit"): `projectTemplates`/`fillNamespacedTemplates`, and the `uf.Bundle`/`uf.PluginKinds["agent"]`/`uf.Namespaces` reads. The top-level assembler `projectResolvedProjectWithBoxes` orchestrates both; it lives where it can import both → loaderkit (top of stack) OR stays a thin host+plugin-shared entry. `fillNamespacedBoxes` (embeds scan+render-prep) resolves via U5 (pre-computed inputs).
- This honors both rulings (deploykit-preferred where cycle-safe; loaderkit for loader-specific/uf-coupled) with ZERO new kit. Proceeding on this split unless redirected.

### U2 (in progress) — leaf movers DONE, GREEN (2026-07-27)
- NEW `sdk/deploykit/box_build_resolve.go`: `CollectBoxAlias` + `ResolveBoxEngine` (both PURE over cfg+layers+boxName — relocated verbatim from charly/alias_collect.go + charly/engine.go). Sit beside the other deploykit box collectors (R3) so charly core AND candy/plugin-build both call the ONE copy.
- charly: DELETED `alias_collect.go` (95 LOC) + `engine.go` (52 LOC) entirely; repointed the 4 call sites (resolved_project_host.go ×2, render_baked_metadata.go ×1, alias_collect_test.go ×3-call+type) to `deploykit.CollectBoxAlias`/`deploykit.ResolveBoxEngine` + `spec.CollectedAlias`. `collectedAliasesToLabel` param → `[]spec.CollectedAlias`.
- P16 gate reconciled: pruned the two now-deleted residueOwner entries (engine.go, alias_collect.go). **Gate: RESIDUE 96→94, UNOWNED 0, STALE 0** (the manifest tracker still FAILs by design until #118 GREEN — a clean −2, no regression).
- PROOF: GOWORK=off build+vet+test ./deploykit OK; workspace charly build+vet exit 0; alias/candy/init tests pass; gofmt clean; R5 grep clean (deleted-id matches are only test-message strings).
- **Running total this session: charly −167 net LOC (190 del / 23 ins); +2 sdk files (loaderkit/vocab.go, deploykit/box_build_resolve.go).**

### U2 REMAINDER (next — the projection body, largest single move)
Move to **deploykit** (uf-free): `projectResolvedBox`, `rawCandyPair`, `projectBoxAggregates` (now that CollectBoxAlias/ResolveBoxEngine live there, its body is pure), `fillBoxPlans`, + `ComputeIntermediates`-shim (intermediates_shim.go → deploykit; note deploykit already has the pure ComputeIntermediates) + `ComputeCalVer` (version.go).
Move to **loaderkit** (uf-coupled): `projectTemplates`/`fillNamespacedTemplates` + the assembler `projectResolvedProjectWithBoxes` (reads uf.Bundle/PluginKinds/Namespaces) — loaderkit is the only cycle-safe home for the assembler since it imports both deploykit+buildkit AND owns UnifiedFile.
STAY host (become legs in U5): `resolveResources(uf)` (per-node InvokeProvider(ClassKind,"resource")); `fillNamespacedBoxes` (embeds scan+render-prep — deliver as pre-computed inputs). `ResolveOpts` is package-main — either move a minimal projected-opts to the kit or pass the 2 fields (IncludeDisabled + names) it needs. `hostBuildResolvedProject`/`buildResolvedProjectFromDir`/`loadProjectForResolve` STAY host (the F11 "resolved-project" seam + the load), calling the relocated kit projectors.
Then U3 render-prep → deploykit, U4 scan → loaderkit (+ candy-materialize leg + fetch-orchestration leg — K1-sized), U5 legs, U6 plugin-side resolveBuildEngine + invert build-prep (bootstrap-critical, end-to-end), U7 P13/P14 repoints (residue DROPS), U8 gate reconcile.

*Session boundary: U1 + U2-leaf-movers landed GREEN + gate-honest. Remaining U2-body/U3-U8 is a large multi-session build; a fresh full-budget successor (or a compacted continuation) picks up from HERE — the worktree is at a clean green boundary, all real builds/vets/tests pass (only the by-design residue tracker fails, showing the intended −2).*

### U2-body PROJECTION HELPERS — DONE, GREEN (k3-legs, 2026-07-27)
Continued the U2 REMAINDER split above. Landed the two **pure/uf-projection** sub-units (the assembler + its host-coupled seams are deliberately NOT yet moved — see next section):
- **deploykit half** — NEW `sdk/deploykit/resolved_project.go`: `ProjectResolvedBox` (buildkit.ResolvedBox→spec.ResolvedBoxView), `RawCandyPair` (spec.CandyReader escape hatch), `ProjectBoxAggregates` (cfg+layers+ResolvedBox → the Ports/Volumes/Aliases/Engine/authored-plan aggregates), `FillBoxPlans` (cfg+layers → per-box flattened acceptance plan). All PURE (no uf, no registry, no package-main type) — relocated verbatim from `charly/resolved_project_host.go`, exported. charly repointed: the 4 assembler call-sites + `fillNamespacedBoxes` + `bundle_compile_seam.go:325` + 2 tests (`bundle_compile_parity_test.go`, `resolved_project_host_test.go` — added deploykit import).
- **loaderkit half (templates)** — NEW `sdk/loaderkit/templates_project.go`: `ProjectTemplates(uf)` + unexported `fillNamespacedTemplates` (uf.Local/K8s/Pod/VM/Android + uf.Namespaces recursion → *spec.ProjectTemplates, kind-blind RawBody copy). uf-COUPLED → loaderkit (owns UnifiedFile). charly repointed: `resolved_project_host.go:214`, `k8s_config.go:51` (`findK8sSpec` — added loaderkit import), `resolved_project_namespace_test.go` (added loaderkit import). Removed now-unused `encoding/json` + `kit` imports from resolved_project_host.go.
- R5 claim-sweep: updated stale comment refs to the moved names (resolved_project_host.go header + intermediate comment, k8s_config.go ×2, local_spec.go).
- **Gate UNCHANGED: RESIDUE 94 / UNOWNED 0 / STALE 0** — this unit SHRINKS files (moves functions), deletes no whole charly file, so no residueOwner entry drops (that happens at U7 when the P13/P14 callers repoint). Correct + expected.
- PROOF: `GOWORK=off go build+vet+test ./deploykit ./loaderkit ./buildkit` OK; workspace `go build -o /dev/null ./charly` + `go vet ./charly` exit 0; targeted charly tests (`TestResolvedProject*`/`TestBundleCompile*`/Alias/Candy/Init/Namespace/K8s) PASS; gofmt clean; R5 grep clean (only comment/test-string residue, updated).
- **Running total (uncommitted on the worktree, incl. predecessor U1+U2-leaf): charly −369 net LOC (424 del / 55 ins across 15 files); +4 sdk files (loaderkit/vocab.go, loaderkit/templates_project.go, deploykit/box_build_resolve.go, deploykit/resolved_project.go).**

### U2-body REMAINDER + U3–U8 (next — the ASSEMBLER + the hard integration, unchanged from §7 plan)
The pure movers are done; what's LEFT of U2-body is the genuinely-coupled integration piece, plus U3–U8:
- **U2-assembler** — `projectResolvedProject` / `projectResolvedProjectWithBoxes` → loaderkit (owns UnifiedFile, imports deploykit+buildkit). This is NOT a pure move: the assembler calls package-main `ResolveBox` (buildkit wrapper + fillBuildConfigFallback), `renderPrepBox` (Generator), `resolveResources(uf)`, `fillNamespacedBoxes` (embeds scan+render-prep), `ComputeCalVer`, `ComputeIntermediates`-shim. Each must become an INJECTED SEAM (func-param) into the loaderkit assembler — this is the "genuinely new legs" §6 flagged. `ComputeCalVer` (version.go, 14 call-sites/8 files) + `ComputeIntermediates`-shim → deploykit belong HERE (the plugin consumes them via the assembler), not as a standalone broad sweep. `ResolveOpts` is package-main → thread the 2 fields (IncludeDisabled + names) it needs, or a minimal projected-opts.
- Then **U3** render-prep → deploykit (inputs-threaded), **U4** candy scan → loaderkit + candy-materialize leg + fetch-orchestration leg (K1-sized, largest/riskiest), **U5** `resolveResources` InvokeProvider(ClassKind,"resource") leg + `fillNamespacedBoxes` pre-computed inputs, **U6** plugin-side `resolveBuildEngine` in candy/plugin-build + `buildengine-*` legs + INVERT build-prep + delete the fat seam (bootstrap-critical, end-to-end), **U7** repoint P13/P14 callers → InvokeProvider (RESIDUE DROPS), **U8** dead-code + R5 + gate reconcile.
- `NewGenerator` STAYS host-resident for the render-seam FLOOR (ruled — classify FLOOR).

### U2-body ASSEMBLER — DONE, GREEN (k3-legs, 2026-07-27) → **U2-body COMPLETE**
The envelope assembler relocated to loaderkit; U2-body (the 658-LOC projection) is now fully done.
- NEW `sdk/loaderkit/resolve_project.go`: `ProjectResolvedProject(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, calver, seams, diags, preResolvedBoxes)` — the whole assembler body (box loop / auto-intermediates / candy fill / namespaced fold / deploy fold / resource fold / vocab / templates / agent catalog / box-plans / build-order). loaderkit is the cycle-safe home (owns UnifiedFile, imports deploykit+buildkit).
- **The opts-agnostic seam design** (the key to a clean move): the assembler NEVER inspects a package-main `ResolveOpts`. Host-coupled legs are injected as `loaderkit.ResolveProjectSeams` func fields — `ResolveBox` / `FillNamespacedBoxes` / `ResolveResources` / `ShouldIncludeDisabled` / `ComputeIntermediates` / `ExternalizedBuilders`. The host wrapper builds closures that CAPTURE `opts` (incl. the DistroCfg/BuilderCfg perf pre-fill), so `opts` never crosses into loaderkit and no ResolveOpts projection was needed. `calver` is threaded as a param (eliminating the `ComputeCalVer` dependency from the assembler — no ComputeCalVer move needed). `resolveResources`/`fillNamespacedBoxes` STAY host (become U5 legs); this move made them injected seams, ready for U5 to swap the closure body for an InvokeProvider leg / pre-computed inputs with ZERO assembler change.
- charly: `projectResolvedProject`/`projectResolvedProjectWithBoxes` are now THIN wrappers (same signatures → every caller unchanged: build_overlay/build_resolve_host/validate_project_host/buildResolvedProjectFromDir + the installstep-envelope-parity test) that compute calver, apply the perf pre-fill, build the seams, and delegate. Removed now-unused `fmt` import.
- No identifiers deleted (wrapper names persist; the seam impls — fillNamespacedBoxes/resolveResources/ComputeIntermediates/externalizedBuilders — still live in charly), so no R5 sweep beyond the earlier ones. Gate UNCHANGED (94/0/0 — no whole-file delete).
- PROOF: `GOWORK=off go build+vet+test ./loaderkit ./deploykit` OK; workspace charly build+vet exit 0; FULL `go test ./charly` (132s): ONLY failure is TestKernelManifest (the #118 tracker) — every other test PASSES incl. TestResolvedProject*/InstallstepEnvelope/BundleCompile/Namespace/ValidateProject; gofmt clean.
- **Running total (uncommitted, incl. predecessor U1+U2-leaf): charly −549 net LOC (621 del / 72 ins, 15 files); +5 sdk files (loaderkit: vocab.go, templates_project.go, resolve_project.go; deploykit: box_build_resolve.go, resolved_project.go). resolved_project_host.go: 659→269 LOC.**

*Session boundary (k3-legs): **U2-body COMPLETE + GREEN** (deploykit projection helpers + loaderkit templates + the loaderkit assembler with opts-agnostic seams). Worktree clean-green (WIP durable, uncommitted). U3–U8 remain: U3 render-prep→deploykit (buildBakedMetadata 435 LOC, thread *Generator fields as inputs) · U4 candy scan→loaderkit + materialize/fetch legs (K1-sized) · U5 resolveResources InvokeProvider leg + fillNamespaced pre-computed inputs (SWAP the seam closures — assembler unchanged) · U6 plugin-side resolveBuildEngine + buildengine-* legs + INVERT build-prep (bootstrap-critical, end-to-end; the assembler is now plugin-callable — plugin-build passes its OWN seams) · U7 P13/P14 repoints (RESIDUE DROPS) · U8 gate reconcile. Every real build/vet/test passes; only the by-design #118 tracker FAILs (steady 94/0/0). A fresh full-budget successor (or compacted continuation) picks up at U3.*

### U3 — render-prep → deploykit — DONE, GREEN (k3-final, 2026-07-27)
- Added `Config *spec.Config` + `InitConfig *buildkit.InitConfig` fields to `deploykit.Generator` (render_generator.go). The host render-prep pass reads them; the plugin-build render path (NewRenderGeneratorFromProject) leaves them nil (reads pre-computed caches from the envelope).
- NEW `sdk/deploykit/render_prep.go`: `(*Generator).RenderPrepBox`/`RenderPrepAll` + `buildBakedMetadata` (435-LOC OCI-label gather) + `collectChainDataEntries` + `collectedAliasesToLabel` + `sortedEnvDepsFromCandies` + unexported `sortedEnvDeps` — relocated VERBATIM from charly/render_prep.go + charly/render_baked_metadata.go, now methods on `*deploykit.Generator` (GlobalOrderForBox/CollectBuilderRuntimeEnv are already deploykit methods; no ResolveOpts inspected — threads *Generator fields per the seam pattern).
- charly: DELETED render_prep.go (80) + render_baked_metadata.go (435) entirely; deleted the now-dead `globalOrderForBox` shim (generate.go) + the now-dead `sortedEnvDeps` helper + its `sort` import (layers.go). `toDeploykit()` sets `dg.Config`/`dg.InitConfig`. Repointed 3 callers (build_resolve_host.go, resolved_project_host.go, plugin_installstep_envelope_parity_test.go) to `gen.toDeploykit().RenderPrepAll()`/`.RenderPrepBox(name)`. `collectBuilderRuntimeEnv` shim STAYS (4 test callers).
- Gate: pruned the 2 now-deleted residueOwner entries (render_baked_metadata.go, render_prep.go). **RESIDUE 94→92, UNOWNED 0, STALE 0** (clean −2, P8b 20→18).
- PROOF: `GOWORK=off go build+vet+test ./deploykit ./loaderkit ./buildkit` OK; workspace `go build ./charly` + `go vet ./charly` exit 0; `TestPluginInstallstepEnvelopeParity` (byte-equivalence, 21s) + TestBundleCompileParity/TestResolvedProject*/TestInitDefLabel/TestGenerate* PASS; gofmt clean; R5 grep — deleted charly identifiers only survive in updated comments.
- **Running total (uncommitted, incl. predecessor U1+U2): charly −1080 net LOC (1167 del / 87 ins, 21 files); +6 sdk files (loaderkit: vocab/templates_project/resolve_project; deploykit: box_build_resolve/resolved_project/render_prep).**

### U4 SCOPING INTEL (k3-final, 2026-07-27) — verified facts for the next successor
The candy SCAN is LESS greenfield than §6 implied — the MANIFEST primitives ALREADY live in
`sdk/loaderkit/scan_candy.go` (403 LOC): `ScanCandyManifest` / `ScanInlineCandy` / `ScanRemoteCandy` /
`scanFromParsed` / `populateFromYAML` / `QualifyRemoteSiblingDeps` / `derivePackageSections`. Core's
`requireCandyScanner()` returns a `spec.CandyScanner` DI-backed by these. So U4 is NOT "invent the scan in
loaderkit" — it is relocate the scan ORCHESTRATION + close the deferred host-completion.

What REMAINS host-side in `charly/layers.go` (the U4 target set):
- `ScanAllCandyWithConfigOpts` (573) + `scanCandyFromLocal` (593, the remote-ref fix-point fetch + per-entity-version arbitration) + `scanLocalCandies` (152) + `legacyScanCandiesDirScanned` (170).
- `finalizeScannedCandies` (511, the SOLE spec.CandyReader choke point) + `completeCandyRunOps` (474) + `PopulateCandyInitSystem` (423) + `withLocalRawRefs` (534).
- `parseCandyYAML` (200) + `singleCandyMappingNode` + `rejectLegacyCandyKeys`/`rejectUnknownCandyTopLevelKeys`/`looksLikeDistroOrFormatKey`.

The precise coupling points (each is a design decision):
1. **opInContext / #39 completion-deferral.** `completeCandyRunOps` needs `opInContext` (registry-adjacent D-data). loaderkit's `scanFromParsed` DEFERS exactly this (scan_candy.go:138 comment names "task #39"). The DI-hook is ALREADY THERE: `deploykit.OpInContext` (`var OpInContext func(op *Op, ctx spec.ExecContext) bool`, compiler_deps.go:42), injected by charly at init (`deploykit.OpInContext = opInContext`, layers.go). So `completeCandyRunOps`/`finalizeScannedCandies`/`PopulateCandyInitSystem` are cycle-safe in loaderkit (loaderkit→deploykit confirmed; scan_candy.go already imports deploykit) and reach opInContext via that hook — closing #39. LOW RISK, pure functions, ~130 LOC. **This is the cleanest first U4 green sub-boundary.**
2. **The fetch/git leg.** `scanCandyFromLocal` drives `CollectRemoteRefsOpts` + `kit.GitDefaultBranch` + the `(repo,git-tag)` fix-point fetch, and `EnsureRepoDownloaded`'s `autoMigrateCacheProjectOnly` (`providerRegistry.resolve(command:migrate)+Invoke`, refs.go:257) + the `activeRefsDownloader` swap. §6's "candy-materialize/fetch leg" — thread via the EXISTING `kit.RefsDownloader` seam (P7 precedent).
3. **parseCandyYAML → buildCandy (B bootstrap root) + requireLoaderParser (registry).** The node-form parse path calls `requireLoaderParser().ParseDoc` + `parsedNodeToGeneric` + `buildCandy`. buildCandy is bootstrap-critical (STAYS core per the boundary law); loaderkit's parse must reach it via the `DocParser`/candy-materialize seam. This is the genuinely bootstrap-delicate piece — do it via the K1 witness leg pattern, NOT a direct move.
4. **`scanLocalCandies` calls package-main `LoadUnified`/`ApplyDiscover`** — loaderkit.LoadUnified exists (K1); repoint.

**Recommended U4 order for the successor:** (a) move the pure finalize/completion cluster (finalize/complete/populate) → loaderkit closing #39 via deploykit.OpInContext [green sub-boundary]; (b) move the fetch fix-point orchestration → loaderkit via kit.RefsDownloader; (c) parseCandyYAML shape-parse → loaderkit reaching buildCandy via the materialize seam; (d) ScanAllCandyWithConfigOpts/scanCandyFromLocal assembler → loaderkit with the seams injected (mirrors U2's ResolveProjectSeams pattern). Then U5/U6 as §7.

**HAND-BACK STATE (k3-final):** U3 landed GREEN + verified (byte-equivalence via TestPluginInstallstepEnvelopeParity). Worktree clean-green, WIP durable/uncommitted. Session total charly −1080 net LOC, gate RESIDUE 92 / UNOWNED 0 / STALE 0. U4-U8 remain for a fresh full-budget successor (U4 is K1-sized + bootstrap-critical; the scoping above de-risks it).

---

## §8 — U4-a + U4-b — DONE, GREEN (k3-u4, 2026-07-27)

Landed the two loaderkit-relocation sub-boundaries of U4 per §7's U4 SCOPING INTEL recommended order (a)+(b). Both green, WIP durable/uncommitted.

### U4-a — the finalize/complete/populate cluster → loaderkit (#39 CLOSED)
- NEW `sdk/loaderkit/finalize_candy.go`: `PopulateCandyInitSystem` / `CompleteCandyRunOps` / `FinalizeScannedCandies` — relocated verbatim from `charly/layers.go`. The op-context classifier (`opInContext`, the #39 registry-adjacent D-data) is reached via the EXISTING `deploykit.OpInContext` DI hook (charly injects it at init: `deploykit.OpInContext = opInContext`, layers.go:`init()`; candy/plugin-build injects an InvokeProvider-backed callback in U6). So `CompleteCandyRunOps` is now plugin-callable and **#39's "the host still owns" deferral is CLOSED** — loaderkit owns the completion; the op-context data rides the hook (same pattern deploykit.install_build.go:708 already uses).
- charly: DELETED the 3 funcs from `layers.go`; repointed the 5 code call sites (`ScanCandy`, `scanCandyFromLocal` ×3, `unified.go` `projectCandiesScanned`) + the R5 comment sweep (config.go/generate.go/tests) to `loaderkit.FinalizeScannedCandies` / `loaderkit.PopulateCandyInitSystem`. Repointed `tasks_test.go` to `loaderkit.CompleteCandyRunOps` (added loaderkit import). Dropped the now-unused `slices` import from layers.go.
- PROOF: `GOWORK=off go build+vet+test ./loaderkit` OK; workspace `go build+vet ./charly` exit 0; targeted tests pass; gofmt+R5 clean; gate UNCHANGED 92/0/0 (moves funcs, deletes no whole charly file — residue drops at U7).

### U4-b — the candy fetch fix-point + RemoteDownload → loaderkit (opts-agnostic ScanSeams)
- NEW `sdk/loaderkit/scan_orchestrate.go`: `RemoteDownload` (moved from `charly/refs.go`), `ScanSeams`, and `ScanCandyFromLocal(localScanned, initCfg, seams)` — the whole step-2-onward fetch-fixpoint body (remote-ref collect, `(repo,git-tag)` fix-point fetch, per-entity-version arbitration via `PickCandyVersion`, finalize) relocated verbatim from `charly/layers.go` `scanCandyFromLocal`. The three host-coupled legs are injected `ScanSeams` func fields — `CollectRemoteRefs` (the reachability walk over cfg+registry), `EnsureRepo` (git clone/cache + auto-migrate), `ScanRemote` (per-candy remote manifest scan via `parseCandyYAML`) — mirroring U2's `ResolveProjectSeams`. The pure mechanism (queue, arbitration) is loaderkit-side; `kit.GitDefaultBranch`/`spec.ParseRemoteRef`/`PickCandyVersion` are all already plugin-reachable.
- charly: `scanCandyFromLocal` is now a THIN wrapper (identical signature → `fillNamespacedBoxes` unchanged) that builds the seams via a new `scanSeamsFor(cfg, opts)` closure-builder (captures cfg+opts so they never cross into loaderkit) and delegates. DELETED the `RemoteDownload` struct from `refs.go`; repointed `CollectRemoteRefs(Opts)` return types + construction + `refs_test.go` to `loaderkit.RemoteDownload`.
- PROOF: `GOWORK=off go build+vet+test ./loaderkit` OK; workspace `go build+vet ./charly` OK; targeted tests (Refs/ScanCandy/Candy/ResolvedProject/Generate/Namespace/BundleCompile/Validate) pass; FULL `go test ./charly` (132s): ONLY `TestKernelManifest` (the #118 tracker) fails, every other test PASSES; gofmt+R5 clean; gate UNCHANGED 92/0/0.

- **Running total (uncommitted, incl. predecessors U1+U2+U3): charly −1328 net LOC; +8 sdk files (loaderkit: vocab/templates_project/resolve_project/finalize_candy/scan_orchestrate; deploykit: box_build_resolve/resolved_project/render_prep). layers.go: 752→511 LOC.**

### U4-(c) scope resolution — the local-scan is a U6 REVERSE LEG, not a loaderkit move
Per §6/U4 note (3): `parseCandyYAML → requireLoaderParser().ParseDoc + buildCandy` is **bootstrap-delicate** (buildCandy is the B bootstrap root, STAYS core). So `scanLocalCandies` / `parseCandyYAML` / `legacyScanCandiesDirScanned` STAY host and become a **U6 `buildengine-scan-local` reverse leg** (the K1-witness leg pattern, exactly as `MaterializeLoadedProject` stays a host leg) — NOT a loaderkit relocation. `parseCandyYAML` already rides into `ScanCandyFromLocal` as the injected `parseDoc` inside the `ScanRemote` seam closure. **U4's loaderkit relocations are therefore substantially COMPLETE after (a)+(b).**

---

## §9 — U5/U6 RECIPE (mapped by k3-u4; the bootstrap-critical inversion — the designated hand-back point)

**State:** the assembler (`loaderkit.ProjectResolvedProject`, U2) is already opts-agnostic with injected `ResolveProjectSeams`; the scan (`loaderkit.ScanCandyFromLocal`, U4-b) is already opts-agnostic with injected `ScanSeams`. So the plugin-side resolve is a **pure composition of already-plugin-importable primitives + closures backed by reverse legs** — the assembler/scan bodies need ZERO further change. What remains is U6: build those closures in candy/plugin-build over new `buildengine-*` legs, then INVERT the fat `build-prep` seam.

**The fat seam to invert:** `charly/build_resolve_host.go` `hostBuildBuildResolve` (251 LOC) runs the WHOLE resolve host-side: `NewGenerator` (LoadConfig→ScanAllCandy→validate→ResolveAllBox) → build-prep fs ops → `RenderPrepAll` → `ResolveBoxOrder` → `projectResolvedProjectWithBoxes` → drive-model. plugin-build's `drive.go` `resolveBuild` just calls `HostBuild("build-prep")` and gets the envelope back.

**The U6 composition (plugin-side `resolveBuildEngine` in candy/plugin-build), reaching the host ONLY for the coupled legs:**
```
uf, ok := loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(ex))   // K1 (landed) — reverse legs
cfg := uf.ProjectConfig()
localScanned := <HostBuild("buildengine-scan-local", dir)>                  // NEW leg — bootstrap-delicate parseCandyYAML/buildCandy (K1-witness pattern)
layers := loaderkit.ScanCandyFromLocal(localScanned, initCfg, pluginScanSeams)   // U4-b — plugin supplies ScanSeams closures over legs
boxes  := buildkit.ResolveAllBox(cfg, tag, dir, opts)                       // pure sdk (config.go wrapper already thin)
boxes  = deploykit.ComputeIntermediates(boxes, layers, cfg, tag)           // pure sdk
order  := deploykit.ResolveBoxOrder / ResolveBoxLevels(boxes, layers)      // pure sdk
_      = deploykit.ComputeEffectiveVersions(boxes, layers)                 // pure sdk
rp     := loaderkit.ProjectResolvedProject(cfg, layers, uf, …, pluginResolveSeams)   // U2 — plugin supplies ResolveProjectSeams closures
// then render.go (already plugin-side) renders from rp
```

**NEW host legs to register (mirror `host_build_loader.go`'s `loader-*` registrations; class-generic action nouns, F11-safe):**
- `buildengine-scan-local` — wraps `scanLocalCandies(dir)` (returns `map[string]spec.ScannedCandy` as JSON). Bootstrap-delicate (parseCandyYAML→buildCandy) → do via the K1-witness leg pattern, NOT a move.
- `buildengine-collect-remote-refs` — wraps `CollectRemoteRefsOpts(cfg, finalizedLocal, withLocalRawRefs(opts, localScanned))` (reads cfg + registry `resolveLocalViaPlugin`). The plugin's `ScanSeams.CollectRemoteRefs` closure calls it.
- `EnsureRepo` / `ScanRemote` — the plugin's other two `ScanSeams` closures: `EnsureRepo` → an `EnsureRepoDownloaded` leg (or reuse `kit.RefsDownloader`, P7); `ScanRemote` → a `ScanRemoteCandy` leg (registry+parseCandyYAML).
- `buildengine-connect-plugins` — wraps `loadProjectPlugins` (registry connect). §4 already named this.
- `ResolveProjectSeams.ResolveResources` — **already a callback**: `resolveResources` is `loaderkit.ResolvePluginKindViaPlugin(uf, "resource", <hostInvoke closure>)` (resource_resolve.go); the plugin supplies a closure calling `InvokeProvider(ClassKind,"resource",OpResolve)` — trivially plugin↔plugin (S1).
- `ResolveProjectSeams.FillNamespacedBoxes` — `fillNamespacedBoxes` embeds a second scan+render-prep mid-projection (resolved_project_host.go:117) → deliver as PRE-computed inputs (mirror `preResolvedBoxes`), per §6 note 4b.
- `validateProjectForBuild` — ALREADY a `command:validate` dispatch (its comment names exit K3) → plugin↔plugin `InvokeProvider`, trivial.
- render-seam floor: `NewGenerator` STAYS host for the render-seam host-builder (RULED — classify FLOOR); the plugin holds the ResolvedBox, the host builds a minimal gen lazily for the registry/package-main type-asserts (`loadRenderGen`'s existing fresh-`NewGenerator` fallback, §6).

**Reference to copy:** `candy/plugin-bundle/load_executor.go` (`execLoaderExecutor`, the K1 witness) + `charly/host_build_loader.go` (the host legs). Same shape, one level up the stack. **INVERT:** build-prep's `hostBuildBuildResolve` shrinks to the leg set; the plugin runs the resolve; delete the fat resolve half. **BOOTSTRAP-CRITICAL, END-TO-END — verify a real box builds via the plugin resolve (the R10 build bed, e.g. check-pod-overlay), which the PERSISTENT session must own** (an ephemeral teammate cannot own a long bed — CLAUDE.md).

**HAND-BACK STATE (k3-u4):** U4-a + U4-b landed GREEN + full-suite-verified (only the #118 tracker fails). Worktree clean-green, WIP durable/uncommitted. Session total charly **−1328 net LOC** (cumulative incl. predecessors), gate RESIDUE 92 / UNOWNED 0 / STALE 0. U4 loaderkit relocations SUBSTANTIALLY COMPLETE (the local-scan is a U6 reverse leg per §8 U4-(c)). **U5/U6 is the bootstrap-critical plugin-side inversion mapped in §9 above — the designated hand-back point for the R10 build bed (persistent-session-owned).** U7 (P13/P14 caller repoints → InvokeProvider; RESIDUE DROPS, the ≥2000 floor lands) + U8 (dead-code + gate reconcile) follow U6.

---

## §10 — U6 EXECUTION LOG (k3-u6, 2026-07-27) — the bootstrap-critical inversion LANDED GREEN

**U6 (the plugin-side build-engine RESOLVE inversion) is DONE + smoke-proven.** The fat host build-prep
seam is DELETED; candy/plugin-build now runs the whole RESOLVE itself. This is the crux every prior
session deferred at the U5/U6 boundary.

### What landed (files)
- **NEW `candy/plugin-build/resolve.go`** — `resolveBuildEngine(ctx, ex, req, generateOnly)`: the whole
  NewGenerator+build-prep RESOLVE plugin-side. LoadUnified (K1 legs) → vocab projection
  (loaderkit.Project{Distro,Builder,Init}Config + InvokeProvider(kind:distro|init) callbacks) → scan
  (buildengine-scan-local leg + loaderkit.ScanCandyFromLocal over the 3 remote-seam legs) →
  buildengine-connect-plugins → validate (InvokeProvider command:validate) → buildkit.ResolveAllBox →
  deploykit.ComputeIntermediates/GlobalCandyOrder/ComputeEffectiveVersions → buildengine-prep leg →
  RenderPrepAll (pure deploykit.Generator method, plugin-side) → ResolveBoxOrder/FilterBox →
  resolveUserContext (reproduced plugin-side; external-base probe via InvokeProvider(verb:oci)) →
  loaderkit.ProjectResolvedProject (plugin seams) → drive-model (engine/platform/levels/descriptors/
  tunables, pure sdk). Also `candy/plugin-build/resolve_legs.go` (the leg helpers) + `load_executor.go`
  (the K1 loader-executor, mirror of plugin-bundle's witness).
- **NEW `charly/host_build_buildengine.go`** — the 7 thin `buildengine-*` reverse legs (scan-local,
  collect-remote-refs, ensure-repo, scan-remote, connect-plugins, namespaced, prep). The prep leg keeps
  the render-seam-floor NewGenerator host (RULED) + host-fs prep + ensureCharlyBinaryFresh.
- **DELETED `charly/build_resolve_host.go`** (the fat build-prep seam, 188 LOC) — hard cutover.
- **NEW `sdk/buildkit/externalized_builders.go`** (`buildkit.ExternalizedBuilders`, charly aliases it) +
  **`sdk/buildkit/calver.go`** (`ComputeCalVer`/`ComputeCalVerAt` relocated; charly delegates) — R3 single
  source both charly + plugin read.
- **NEW CUE type** `#BuildEngineScanRemoteRequest` (sdk/schema/buildwire.cue → regen; the ONE new wire
  type). `dispatchBuild` reverse channel already serves InvokeProvider (no new broker work).
- `drive.go`: `resolveBuild` now calls `resolveBuildEngine` directly (no HostBuild round-trip).
- Gate reconciled: pruned `build_resolve_host.go`, added `host_build_buildengine.go` (owner K3) →
  **RESIDUE 92 / UNOWNED 0 / STALE 0**. R5 claim-sweep on every `build-prep`/`hostBuildBuildResolve`
  reference (comments + parity test) — grep clean (deleted ids only in CHANGELOG).

### PROOF (all against the FINAL, transitional-free code)
- `go build` GREEN: sdk (all), charly (workspace), candy/plugin-build (standalone). `go vet` GREEN (all
  3 modules). gofmt clean.
- `go test ./charly` (132s): ONLY `TestKernelManifest` fails (the by-design #118 tracker); EVERY other
  test PASSES incl. TestPluginInstallstepEnvelopeParity (byte-equivalence), TestGenerate*, TestResolved*,
  TestReserved*, TestBundleCompile*, TestValidateProject*.
- `go test ./sdk/{spec,buildkit,loaderkit,deploykit}` GREEN — incl. `TestGenReproducible` (the cue:gen
  regen is reproducible).
- **SMOKE 1 — `charly box generate` GREEN (rc=0)**: full plugin-side resolve → envelope → render wrote a
  real Containerfile (19 `LABEL ai.opencharly.*`).
- **SMOKE 2 — `charly box build check-k8s-deploy-app` GREEN (rc=0)**: full plugin-side resolve →
  drive-model → 37-step podman build → all OCI labels → committed + tagged
  `ghcr.io/opencharly/check-k8s-deploy-app:2026.208.2124` → retention prune. The plugin-side resolve
  produced a real box end-to-end.

### LOC + the −2000 gap (HONEST)
`git diff --stat e4678701 -- charly/` = **32 files, 180 ins / 1761 del = −1581 net** (predecessor was
−1328; U6 added ~−253). **Short of the −2000 wave floor by ~−419.** As the trace-spike (§6/§9) predicted
and I confirmed to team-lead: U6 is roughly charly-LOC-NEUTRAL by itself (the resolve ORCHESTRATION +
drive-model move plugin-side, but the coupled bodies stay as leg bodies; NewGenerator STAYS host per the
render-seam-floor ruling). **The −2000 floor lands at U7** — repointing the P13/P14 callers (image.go /
remote_image.go / transfer.go / box_inspect_overlay.go / bundle_add_cmd.go) to a plugin single-box
resolve via InvokeProvider, which lets config.go's `ResolveBox`/`ResolveAllBox` wrappers + fillBuildConfigFallback
be DELETED. **U7 is the deploy/inspect cone (tracked as #38/#39) — it needs the deploy/inspect BEDS to
verify, which an ephemeral teammate cannot own.** I stopped at the U6/U7 boundary rather than hand back an
unverifiable (box-build-only-smoked) deploy-path change — that would be a masked-regression risk (R7/R10).

### U7/U8 map for the successor (or the orchestrator, who owns the beds)
- **U7** — add a build-plugin single-box RESOLVE capability (a `build:resolve` word, or reuse the envelope
  the plugin already builds) callers InvokeProvider; repoint the 6 P13/P14 callers; DELETE config.go
  `ResolveBox`/`ResolveAllBox`/`fillBuildConfigFallback` + any now-orphaned host resolve helpers; prune
  their residueOwner entries (RESIDUE drops; the ≥2000 floor lands). VERIFY with the deploy/inspect beds
  (bundle add/del, box inspect) — orchestrator-owned. NOTE: the host legs (fillNamespacedBoxes,
  loadProjectForResolve, the buildengine-* leg bodies) still call charly's ResolveBox — those stay until
  their own K-wave, so config.go's wrapper deletion needs those call sites moved to buildkit.ResolveBox
  (passing DistroCfg/BuilderCfg) FIRST.
- **U8** — dead-code sweep (lint+grep) + R5 grep + gate reconcile.

**HAND-BACK STATE (k3-u6): U6 COMPLETE + GREEN + smoke-proven (box generate + box build). Worktree
clean-green, WIP durable/uncommitted. charly −1581 net. Gate 92/0/0. Full test green except the #118
tracker. The R10 acceptance bed (`charly check run check-pod-overlay`) is the orchestrator's to run on
this final code; U7 (the −2000-closing deploy/inspect cone) needs the deploy beds.**

---

## §11 — U7 CALLER-REPOINT EXECUTION LOG (k3-u6, 2026-07-28) — DONE + GREEN; empirically proves −2000 needs #39

Per team-lead's U7 directive (repoint EVERY config.go ResolveBox/ResolveAllBox consumer → buildkit
direct, PRESERVE fillBuildConfigFallback semantics, delete the wrappers).

**DONE + GREEN.** DELETED config.go `ResolveBox`/`ResolveAllBox`/`fillBuildConfigFallback`/`toBuildkitOpts`.
Added ONE fallback-preserving helper `buildkitOptsWithVocab(dir, opts)` (folds fillBuildConfigFallback +
toBuildkitOpts; byte-equivalent — a caller with DistroCfg/BuilderCfg skips the reload, others get the
SAME LoadBuildConfigForBox vocab). Repointed ALL 9 production callers → `buildkit.ResolveBox`/`ResolveAllBox`
directly: resolved_project_host.go (the ResolveProjectSeams closure + fillNamespacedBoxes), generate.go
(NewGenerator), host_build_box_ref_resolve.go (×2), image.go, remote_image.go, box_inspect_overlay.go,
bundle_add_cmd.go. Repointed the 4 test files (config/install_build/namespace/resolver_unify) via a test
helper (resolve_test_helpers_test.go). R5 grep clean (fillBuildConfigFallback/toBuildkitOpts only in the
new helper's doc comment).

**VERIFIED GREEN:** go build + vet + gofmt green; `go test ./charly` green EXCEPT #118 tracker (the 48
config/resolver/namespace test assertions PROVE the fallback is byte-equivalent — no masked regression);
`charly box generate` rc=0 (build-resolve path via NewGenerator) + `charly box build check-k8s-deploy-app`
rc=0 (earlier) + `charly box inspect check-k8s-deploy-app` rc=0 (inspect-resolve path). Gate RESIDUE 92 /
UNOWNED 0 / STALE 0.

**EMPIRICAL LOC RESULT: charly −1571 net (was −1581 at U6-only).** The call-site repoint INCREASED charly
LOC by ~10 — deleting the ~40-LOC config.go wrappers is outweighed by the 2-line fallback expansion at ~9
call sites + the test helper. Gate residue UNCHANGED (the repoint moves no FILES). **This EMPIRICALLY PROVES
the U7 call-site repoint cannot reach −2000 or drop residue.**

**THE −2000 GATE REQUIRES #39 (file-body relocation, NOT repoint-in-place):** closing −429 more + dropping
residue requires relocating the P14 command file BODIES — image.go(133)/remote_image.go(111)/
box_inspect_overlay.go(92)/bundle_add_cmd.go(695)/host_build_box_ref_resolve.go(137) → plugin candies (the
tracked #39 cone), which is deploy/inspect-bed-verified. The U7 repoint UNBLOCKS it (callers now reach
buildkit directly; config.go no longer couples them). This is exactly team-lead's escape-clause crossroad,
surfaced with hard evidence for their ruling: (A) grind #39 now, or (B) fold U6+U7-repoint (−1571, green,
proven) and relocate #39 as its own bed-verified cutover.

---

## §12 — B-RELOCATION ARCHITECTURAL FINDING (k3-u6, 2026-07-28) — the 5-file→plugin-oci scope is boundary-law-unsound

team-lead ruled B = move image/remote_image/box_inspect_overlay/transfer/oci_plugin BODIES OUT of charly →
candy/plugin-oci, files GONE, ≥−2000. Investigating the dispatch structure (each file read at source +
its callers grepped) shows the 5-files→plugin-oci scope CONFLICTS with the code's OWN documented boundary-law
classification. These are NOT standalone commands — they are host-seam bodies + hidden CLI reentry points
that plugin-box/plugin-deploy-pod/plugin-build call back into. Evidence (file:line):

- **image.go `BoxCmd`** (charly/image.go:16-36, main.go:54) — the CORE `charly box` KONG GRAMMAR SPINE that
  embeds `kong.Plugins` for plugin-box's dynamic nested subcommands. It is the CLI host (a B/bootstrap-root).
  It CANNOT move to plugin-oci without breaking `charly box` dispatch. → STAYS CORE. So image.go can NOT
  fully leave charly (the litmus "ls charly/image.go → not-found" is unachievable).
- **image.go `BoxPullCmd`** (the hidden `__box-pull` reentry, main.go:92) — its own comment (image.go:38-50)
  classifies it as COMMAND-DISPERSAL (K5) behind plugin-box's `pull` word; its Run stays behind the reentry.
  → plugin-BOX (command family), NOT plugin-oci.
- **remote_image.go** — its OWN comment (image.go:52-58) EXPLICITLY re-classifies it: "NOT CLI-dispersal
  residue … re-classified from a K5-dispersal IOU to the K1/K3-ENGINE family (loader/build-engine cone, moves
  with those waves)". → plugin-BUILD (the build engine, K3), NOT plugin-oci.
- **box_inspect_overlay.go** (`__box-inspect-overlay` reentry) — dispatched by candy/plugin-box/inspect_list.go:88
  via `argv:=[]string{"__box-inspect-overlay",…}` over HostBuild("cli"). It is a `charly box inspect` subcommand
  → plugin-BOX (command:box family), NOT plugin-oci.
- **transfer.go `EnsureImage`** (53) — OCI image ensure/transfer, called by host_build_pod_config_seams.go:85
  (the DEPLOY/pod-config seam) + build.go. Plausibly plugin-oci, but on the deploy path (blast-radius).
- **oci_plugin.go** (104) — the verb:oci CLIENT; ONE remaining charly caller (generate.go:107
  invokeOciInspectUser, the host resolveUserContext for the render-seam-floor NewGenerator). Deletable from
  charly ONLY if generate.go:107 repoints to InvokeProvider(verb:oci) directly.

**CONSEQUENCE:** the boundary-law-sound placement SPLITS across THREE plugins (remote_image→plugin-build;
box_inspect_overlay + BoxPullCmd→plugin-box; transfer + oci_plugin→plugin-oci) and BoxCmd STAYS CORE — a
materially different, larger, multi-plugin cutover than "5 files→plugin-oci." And because BoxCmd (the grammar
spine, ~30 LOC of the 133) can't move, image.go does not fully leave, so team-lead's −2100 estimate does not
hold as-scoped. This is exactly team-lead's "if B does NOT clear −2000 … report the exact shortfall + which
additional build/OCI-themed file would close it — I'll rule" + the orchestrator's placement-duty.

**HAND-BACK (k3-u6): U6 + U7-caller-repoint delivered GREEN + proven (box build/generate/inspect rc=0, full
test green-except-#118, gate 92/0/0, charly −1571). The B file-relocation as specified is boundary-law-unsound
(evidence above) + a 3-plugin cutover touching the deploy path — it needs team-lead's re-scoping ruling + a
fresh full budget (I've spent mine on the U6 keystone + U7 + this trace). A fresh successor executes the
re-scoped relocation from this map.** Genuine architectural crossroad (plan-vs-boundary-law), not a size stop.

---

## §13 — #39 RELOCATION SUCCESSOR PLAYBOOK (k3-u6 hand-off, 2026-07-28) — clean-green-boundary exit

Operator RULED (via team-lead): COMBINE the #39 P14 command-body relocation into K3 (U6+U7+#39 = one
≥2000 fold). I (k3-u6) delivered U6 (bootstrap-critical build-engine resolve inversion) + U7 (ResolveBox
caller repoint), both GREEN + smoke-proven, then reached a REAL context wall (team-lead's endorsed exit —
"STOP at a clean green boundary, do NOT degrade quality"). The #39 relocation is a fresh multi-hour,
deploy-blast-radius, 6-file cutover where EVERY file is reverse-channel-fabric-woven. A fresh full-budget
successor executes it from here.

**LAST-GREEN STATE:** charly/sdk/candy-plugin-build all `go build` rc=0; `go test ./charly` green-except-#118;
`box build`/`generate`/`inspect` rc=0; gate RESIDUE 92 / UNOWNED 0 / STALE 0; R5 clean; charly −1571 net.
WIP durable/uncommitted. NOTHING half-relocated — U6+U7 are complete + verifiable; #39 is UNSTARTED (clean
boundary, no broken fabric).

**#39 TARGET (litmus: `ls charly/{image,remote_image,box_inspect_overlay,bundle_add_cmd}.go` → not-found; charly ≥−2000; RESIDUE COUNT drops):**

| File (LOC) | Placement (boundary-law) | How |
|---|---|---|
| `bundle_add_cmd.go` (695) | candy/plugin-bundle (command:bundle) | The `charly bundle add` deploy command body → the existing K1-witness bundle plugin. HIGHEST blast-radius (deploy path); keep behavior byte-equivalent. Its `buildkit.ResolveBox` call (U7-repointed) → plugin runs it plugin-side via loaderkit/buildkit (like plugin-build's resolveBuildEngine) or a box-ref-resolve reverse leg. |
| `remote_image.go` (111) | candy/plugin-build (build-engine family — its OWN doc, image.go:52-58) | `ResolveRemoteImage`/`RemoteImageContext.BuildImage` construct+call BuildCmd.Run directly — build-engine. Callers (build.go:328, host_build_remote_image_resolve.go:21 seam) → InvokeProvider or reverse leg. |
| `box_inspect_overlay.go` (92) | candy/plugin-box (command:box inspect family) | The `__box-inspect-overlay` reentry plugin-box dispatches (inspect_list.go:88). Move the body into plugin-box; plugin-box calls it directly instead of the core reentry. |
| `image.go` (133) | SPLIT: `BoxCmd` (grammar spine) STAYS core (move to a new `box_grammar.go` so image.go the FILE is gone); `BoxPullCmd`+`FormatCLIError` → candy/plugin-box (the `__box-pull` reentry body). | image.go the file disappears; BoxCmd content stays core under a new filename (boundary-law: it's the CLI host, can't leave core). |
| `oci_plugin.go` (104) | candy/plugin-oci OR delete | The verb:oci CLIENT; 1 charly caller (generate.go:107 `invokeOciInspectUser`, the HOST resolveUserContext for the render-seam-floor NewGenerator). Assess whether the host gen still NEEDS user-context (render-seam only reads Tags, not User — likely deletable); if not, repoint generate.go:107 → a small InvokeProvider(verb:oci) call + delete oci_plugin.go. |
| `transfer.go` (53) | candy/plugin-oci (verb:oci op) | `EnsureImage`, called by host_build_pod_config_seams.go:85 (DEPLOY seam) + build.go. Move to plugin-oci; callers InvokeProvider. Deploy blast-radius. |
| `host_build_box_ref_resolve.go` (137) | KEEP core as the thin box-ref-resolve reverse leg | The relocated plugin bodies (bundle_add, remote_image) call back through it to resolve a box ref → Registry/Name (project-coupled, K1-witness). Already exists + U7-repointed to buildkit direct. |

**PATTERN (per file):** move the body to the plugin (importing sdk ONLY); the plugin LoadUnifies itself
(loaderkit K1) + reaches host-coupled bits via reverse legs (box-ref-resolve, remote-image-resolve,
ensure-image) or InvokeProvider peer-dispatch — EXACTLY like candy/plugin-build/resolve.go (U6) +
candy/plugin-bundle/load_executor.go (K1 witness). Preserve buildkitOptsWithVocab semantics. DELETE the
core body + its reentry + prune the residueOwner/gate entry + R5-sweep the id. Verify green per-file
(box build/generate/inspect + the moved command's CLI smoke) so you stay at a clean green boundary
throughout. team-lead owns the R10 deploy roster (check-pod-overlay/check-local/check-k3s-vm) at hand-back.

**START ORDER (lowest-risk first, bank green increments):** (1) oci_plugin.go (assess-delete or plugin-oci)
→ (2) transfer.go → (3) box_inspect_overlay.go → (4) image.go split → (5) remote_image.go → (6) bundle_add_cmd.go
(largest/riskiest, LAST). Each is its own green boundary.

---

## §14 — #39 RELOCATION EXECUTION LOG (k3-combine, 2026-07-28) — 2 of 6 folds LANDED GREEN; precise map for the remaining 4

Full-budget successor to §13. Did the mandated RDD trace-spike (each of the 6 files read at source + all
callers grepped) BEFORE mass-editing. Two self-contained moves landed GREEN; the RDD trace sharpens §13's
per-file HOW and corrects two placements the source contradicts.

### LANDED GREEN (both byte-equivalent, build+vet+gofmt+`go test ./charly` green-except-#118)
1. **transfer.go DELETED → `ensureImagePresent` folded into `host_build_pod_config_seams.go`.** RECLASSIFIED
   from the brief's plugin-oci: transfer.go's OWN header classifies EnsureImage "UNTIL-K4 (deploy cone)…moves
   together with config_image.go," and its SOLE consumer is the deploy-cone `pod-config-ensure-image` host
   seam (`hostBuildPodConfigEnsureImage`). EnsureImage is deploy-cone image-ensure GLUE (kit.LocalImageExists +
   kit.TransferImage + dispatchBuildEnsure), NOT an OCI-registry client — plugin-oci is a different cone. Folded
   byte-identically into the seam (both already import kit/spec/context). transfer_test.go → ensure_image_test.go
   (TestEnsureImagePresent). Zero deploy-path behavior change (safest for a file I can't bed-verify; the
   orchestrator's check-pod-overlay bed covers it). A later plugin-oci externalization (add an `ensure-image`
   oci_op + thread an in-proc executor so plugin-oci can InvokeProvider(build:ensure)) is achievable but is a
   K4 deploy-cone item, bed-verified — not this K3 build-engine cutover.
2. **oci_plugin.go DELETED → the verb:oci adopt-user dispatch folded into `generate.go`** (its sole consumer,
   `Generator.resolveUserContext`, the render-seam-floor host). RDD finding refining §13's "likely deletable":
   the host resolveUserContext is production-DEAD (no non-test caller; the plugin owns `resolveUserContextPlugin`)
   BUT `plugin_installstep_envelope_parity_test.go` calls `gen.resolveUserContext(img)` to mirror the plugin for
   external-base fixtures (fedora, nvidia) — so it CANNOT be simply deleted without breaking parity coverage.
   Folded the ~15-line registry dispatch (collapsed invokeOci+invokeOciInspectUser into one) into generate.go
   beside its consumer; deleted oci_plugin.go (the ~44-line doc comment dropped). Boundary-law-sound: the
   render-seam-floor host's adopt-user probe belongs with it (K1-tail); the verb:oci PLUGIN still owns the actual
   go-containerregistry inspect-user. Parity test GREEN (byte-equivalent).

Both residueOwner entries pruned. **Gate: RESIDUE 90 (was 92) / UNOWNED 0 / STALE 0. charly −1640 net vs
e4678701 (was −1571; +−69).** `charly box generate` / `box build check-k8s-deploy-app` / `box inspect` still
rc=0 (unaffected — the build/inspect resolve path is untouched).

### THE ENABLING FINDING for the remaining 4 (loader-coupled moves are TRACTABLE)
`loaderkit.LoadUnified` is plugin-callable (K1 #47 landed), AND **plugin-box + plugin-bundle already carry
`hc.exec *sdk.Executor` with `InvokeProvider` + `HostBuild`** (plugin-box/box.go:121 already does
`hc.exec.InvokeProvider(ctx,"build","generate",…)`). So each moved body reaches build:ensure via
`hc.exec.InvokeProvider(ctx,"build","ensure",OpBuild,BuildEnsureRequest,…)` and LoadUnifies itself via
loaderkit — the U6 (plugin-build/resolve.go) / K1-witness (plugin-bundle/load_executor.go) pattern. No new
mechanism; the moves are real but mechanical.

### REMAINING 4 — precise per-file HOW (unstarted; clean green boundary, no broken fabric)
| File (LOC) | Placement | HOW | Wrinkle |
|---|---|---|---|
| **BoxPullCmd (image.go, ~30)** | plugin-box (dispatchPull) | Move Run body into `dispatchPull`: default path = `hc.exec.InvokeProvider("build","ensure",{Image:g.Box,Dir:cwd})`; drop the `__box-pull` reentry. Delete BoxPullCmd struct+Run+main.go `__box-pull` leaf. **image.go STAYS** (BoxCmd grammar-spine + FormatCLIError — main.go:327/331 call FormatCLIError, it MUST stay core). | The `--tag`+short-name path pre-resolves the registry ref via LoadConfig+buildkit.ResolveBox. Either (a) add a `Tag` field to spec.BuildEnsureRequest (CUE, cue:gen) so build:ensure resolves it, or (b) reach the existing `host_build_box_ref_resolve.go` seam via HostBuild. (a) is cleaner. |
| **box_inspect_overlay.go (96)** | plugin-box | tunnel/bind_mounts read the DEPLOY OVERLAY (charly.yml). bind_mounts = pure `deploykit.LoadDeployConfigForRead().Lookup().Volume` (no resolve — plugin-box imports sdk/deploykit). tunnel needs cfg+layers+resolved.Candy+boxPorts → plugin-box LoadUnifies via loaderkit + buildkit.ResolveBox + deploykit.CollectBoxPorts/ResolveTunnelConfig. Drop the `__box-inspect-overlay` reentry (inspect_list.go:88). | K5-doomed per its own header; the loaderkit resolve is the v2 end-state, not premature. |
| **remote_image.go (115)** | plugin-build | `ResolveRemoteImage` (EnsureRepoDownloaded+LoadConfig+buildkit.ResolveBox+ScanCandy) + `BuildImage`(chdir+BuildCmd.Run). plugin-build HAS this machinery (resolve.go). | **HOST CONSUMER build.go:328/333 (BuildCmd.buildRemote) calls ResolveRemoteImage+BuildImage DIRECTLY.** The FLOOR seam `host_build_remote_image_resolve.go` also wraps it. Moving the body to plugin-build means build.go's buildRemote must reach it back via the seam/InvokeProvider — cascade. Handle the build.go consumer, keep the remote-image-resolve seam pointing at the new home (or move buildRemote's `@ref` build into build:box with Dir). |
| **bundle_add_cmd.go (697)** | plugin-bundle | The `charly bundle add` DEPLOY command → the existing K1-witness bundle plugin (has load_executor). Its `buildkit.ResolveBox` call (U7-repointed) → plugin runs it plugin-side. HIGHEST blast-radius + the DOMINANT −LOC (the −2000 floor lives here: −1640 + ~697 ≫ −2000, so this alone closes the gap). Byte-equivalent deploy behavior; orchestrator's check-pod-overlay/check-local/check-k3s-vm beds verify. | LARGEST/riskiest — do LAST, as its own green boundary. |

### HAND-BACK (k3-combine): 2 of 6 folds GREEN + verified; a REAL context wall reached (skills + mandated RDD
trace + 2 moves consumed the budget). STOPPED at a clean green boundary rather than start the 697-line
deploy-path bundle_add move I could not finish+verify (a half-done 697-line deploy command = the broken-tree
worst-case). WIP durable/uncommitted. LAST-GREEN: charly −1640 net, gate 90/0/0, `go test ./charly`
green-except-#118, box build/generate/inspect rc=0, R5 clean on every deleted id. The remaining 4 are fully
mapped above (tractable via InvokeProvider(build:ensure)+loaderkit); a fresh full-budget successor (or the
orchestrator's ≤4-teammate budget) executes them from here. The −2000 floor is reached the moment bundle_add
(and/or remote_image) land — the LOC is already at −1640 with 4 files still to leave.

---

## §15 — #39 EXECUTION LOG (k3-final4, 2026-07-28) — 2 more folds LANDED GREEN (box_inspect_overlay + BoxPull); RDD refines §14 for the last 2

Full-budget successor to §14. Did the mandated RDD trace-spike (each file read at source + all callers/types grepped) BEFORE editing. Two folds landed GREEN; the trace CORRECTS §14's "plugin-box LoadUnifies via loaderkit" premise — the resolved-project envelope plugin-box already fetches carries everything both folds need, so NO loader was added to plugin-box.

### LANDED GREEN (both byte-equivalent, build+vet+gofmt+`go test ./charly` green-except-#118, live smoke)
1. **box_inspect_overlay.go DELETED → tunnel/bind_mounts render in-plugin (candy/plugin-box/inspect_list.go).** §14 said "LoadUnifies via loaderkit + buildkit.ResolveBox + CollectBoxPorts" — REFUTED by trace: `ResolvedBoxView.Ports` is projector-filled (`sdk/deploykit/resolved_project.go:108 CollectBoxPorts`), and `deploykit.ResolveTunnelConfig` IGNORES its candy-reader/candy-list args (`_`). So tunnel = pure `LoadDeployConfigForRead().Lookup()` (deploy overlay) + `ResolveTunnelConfig(overlay.Tunnel, box, "", nil, nil, {}, view.Ports)`; bind_mounts = pure overlay read. NO loader. `dispatchInspect` hoists the existing `hc.resolvedProject()` fetch and switches on tunnel/bind_mounts to two new helpers `inspectBindMounts`/`inspectTunnel`. Dropped the `__box-inspect-overlay` main.go leaf + reentry. Smoke: `box inspect <box> --format tunnel|bind_mounts|ports` rc=0.
2. **BoxPullCmd DELETED → dispatchPull runs ensure-image in-plugin (candy/plugin-box/box.go).** §14 option (a) said "add a Tag field to spec.BuildEnsureRequest (CUE+cue:gen)" — AVOIDED: the `--tag` short-name resolves its registry ref off the envelope (`kit.ResolveShellImageRef(view.Registry, view.Name, tag)` — registry/name are tag-independent), no wire change, no loader. Default path = `hc.exec.InvokeProvider(ctx,"build","ensure",OpBuild,BuildEnsureRequest{Image,Dir})` — the FIRST plugin→build:ensure peer-invoke (works; the same reverse pattern as dispatchGenerate→build:generate). **image.go STAYS** (BoxCmd grammar-spine + FormatCLIError — main.go calls FormatCLIError). Dropped the `__box-pull` main.go leaf + reentry. LIVE SMOKE (both paths, byte-equivalent): `box pull nonexistent-box-xyz` → exact ensure-image "no buildable short-name match" error; `box pull arch.arch --tag 2026.200.1200` → resolved `ghcr.io/opencharly/arch:2026.200.1200`, pull-failed→build-fallback ran.

**Gate: RESIDUE 89 (was 90) / UNOWNED 0 / STALE 0. FLOOR 102.** box_inspect_overlay dropped a residue FILE (90→89). BoxPull dropped ~50 core LOC + a reentry but LEAVES image.go (BoxCmd+FormatCLIError = pure floor now) — **RECOMMEND orchestrator reclassify `image.go` residueOwner P14 → floor (§12 ruling: BoxCmd is the CLI grammar-spine B-root; FormatCLIError is the top-level Kong error formatter).** Session core delta ≈ −151 LOC; cumulative charly ≈ −1801 net vs e4678701. R5 clean (all deleted ids swept; remaining refs are deletion-documenting or accurate-historical comments).

### REMAINING 2 — precise per-file HOW (unstarted; clean green boundary, no broken fabric)
| File (LOC) | Placement | HOW | Wrinkle (RDD-sharpened) |
|---|---|---|---|
| **remote_image.go (115)** | plugin-build (PARTIAL) | Only `BuildImage`(18) + build.go:328 `buildRemote`'s `@ref` build route to build:box (Dir=CacheDir). | **`ResolveRemoteImage` CANNOT fully leave core** — it calls `EnsureRepoDownloaded` (host git clone/cache, K1/B) and IS ALREADY the backing of the `remote-image-resolve` HostBuild seam (plugin-build/ensure.go's `remoteImageResolve` reaches it). So the FILE does NOT vanish; only the build-DRIVE half moves. Modest LOC, build-path change → needs orchestrator's build bed. Lower priority than bundle_add. |
| **bundle_add_cmd.go (697)** | plugin-bundle | The `charly bundle add` DEPLOY command → the K1-witness bundle plugin (has load_executor/compile/node_resolve/deploy_target/builder_preresolve). Dominant −LOC (closes −2000). | **The DEEP K4 deploy-cone move.** `deployAddCmd` is invoked per-node via the `deploy-node-dispatch` HostBuild seam (host_build_deploy_node_dispatch.go); its `.dispatchNode` reaches a web of core helpers — `detectHostContext`/`compileHostContext` (host-fs glibc/distro probe → needs a seam), `preresolveBuilders` (plugin-bundle HAS builder_preresolve.go), `deriveChildExecutorForPath` (pure, moveable), `ResolveDeployRef` (→ node_resolve.go), `ScanAllCandy`/`LoadConfig` (→ load_executor). Deploy blast-radius; needs check-pod-overlay/check-local/check-k3s-vm beds. A proper multi-hour cutover with its OWN budget — do LAST, its own green boundary (§14 concurs). NOT a half-start — a partial 697-line deploy move = the broken-tree worst-case. |

### HAND-BACK (k3-final4): 2 of the remaining-4 folds GREEN + live-smoked. box_inspect_overlay + BoxPull moved fully into candy/plugin-box off the resolved-project envelope (no loader — §14's loaderkit premise refined away). Tree build+test green-except-#118; gate 89/0/0; box inspect + box pull smoke green; R5 clean. WIP durable/uncommitted (no checkpoint commit — that was the post-bundle_add −2000 boundary, not reached). remaining_2: remote_image (PARTIAL — file stays, host-coupled) + bundle_add (the K4 deploy-cone −2000 closer, its own budget + deploy bed roster). Orchestrator owns the terminal bed roster (incl. the FIRST plugin→build:ensure peer-invoke from BoxPull) + the image.go P14→floor reclassify.
