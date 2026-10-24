## Placement default — EXTERNAL (out-of-tree, out-of-process)

NEW plugins default to the EXTERNAL placement: they live in their own repo, are composed
into projects via the `@github.com/<org>/<repo>/candy/<name>:<ref>` candy ref, and are
connected BY WORD at runtime (host-built `./cmd/serve` + go-plugin gRPC). No charly-module
import, no `compiled_plugins:` entry, no go.mod require — the boundary-law default (R:
a plugin reaches the kernel by resolving into an envelope a mechanism consumes).
`compiled_plugins:` (in-proc baking INTO the charly binary) is a per-charly-build choice for
CURATED kernel-adjacent builtins only; every existing entry is migration inventory toward
external, so author placement-agnostic (F8) and ship external first. The `source: builtin`
linkage form on a candy manifest is LEGACY (it asserts the provider is compiled into the
binary that loads it); new candies declare the external github-ref `source:` form.

## One Provider, transport-invisible

Every reserved word — every kind, verb, deploy-target, step, builder, command, build — is served by ONE `Provider`
(`charly/provider.go`): `Reserved() string`, `Class() ProviderClass`, `Invoke(ctx, *Operation) (*Result,
error)`. A Provider is IN-PROCESS (a builtin, registered from `init()`) or OUT-OF-PROCESS (an external,
served over go-plugin gRPC). The registry (`providerRegistry`, `provider_registry.go`), the call sites, and
the bijection gate treat both identically — **the transport is invisible above the registry**. A
`check:` step's authored `<word>: <input>` verb sugar (desugared at parse time to the internal
`plugin`/`plugin_input` pair — authoring that pair directly is a hard load error) dispatches through
`runPluginVerb` → `providerRegistry.ResolveVerb(word)` → `Invoke`, whether the provider is compiled in or
out-of-process. A verb plugin declares its scalar-sugar `primary:` field both in its served capability and
in its candy manifest's `plugin:` block (`primary: {<word>: <field>}`).

**The `build` class (BUILD-ENGINE DISPATCH).** `ClassBuild` serves `build:box` + `build:generate`
(candy/plugin-build, COMPILED-IN): `charly box build` / `charly box generate` route through it instead of
constructing a generator host-side (the former `NewGenerator` is DELETED). The plugin's Invoke (op `sdk.OpBuild`) **OWNS the podman DRIVE** (the
build-order loop, per-image build lock via `kit.AcquireImageBuildLock`, `podman build`, push, and the
inline-merge gate) IN the candy — it imports only the sdk module. It reaches the host for what a sdk-only
candy cannot hold — `buildengine-scan-local` (local candy scan), `buildengine-connect-plugins` (registry plugin connect), and `buildengine-prep` (the render-seam-floor `renderGenCache` populate, which returns an EMPTY map; the host-fs PREP itself moved plugin-side — `runHostFSPrep` in candy/plugin-build/host_prep.go). The resolved-project envelope + drive-model come from the plugin's `resolveBuildEngine` reply (candy/plugin-build/resolve.go), NOT from `buildengine-prep`. The Containerfile
RENDER's host-coupled seams (RenderService, the builder resolves, ValidateEgress, EmitPluginOp, localpkg) via `HostBuild("render-seam", …)`, and `bake_plugin:` binary baking INLINE via `deploykit.EmitBakedPlugins` (no HostBuild). The layer merge is externalized to `verb:oci`
(candy/plugin-oci — no `HostBuild("merge")` seam). `build:box` runs the full drive; `build:generate` calls the SAME
`resolveBuildEngine` with `generateOnly=true` (a PLUGIN-SIDE parameter, NOT a `HostBuild("buildengine-prep")` argument
— that host leg takes `spec.ResolvedProjectRequest`, which has no `GenerateOnly` field), returns after the envelope
projection (no drive-model), renders the Containerfiles itself via `sdk/deploykit.Generator`
(#67 render-DRIVE move — the host no longer renders), and returns the written paths (no podman).
The host-builder KINDS (`buildengine-prep`, `render-seam`) are class-generic action nouns, never the provider
WORDS (the F11 uniform-API gate `TestNoSinglePluginAPISurface` forbids a provider word on that surface); `bake_plugin:` baking is inline (`deploykit.EmitBakedPlugins`), so it has NO host-builder kind. The
Containerfile RENDER DRIVE (`Generate` / `generateContainerfile`) is in `sdk/deploykit` (#67 — driven by
plugin-build over the envelope + the `render-seam` reverse legs); the host keeps ONLY the thin `buildengine-*` shards + `render-seam` (the loader + resolve + envelope projection + drive-model moved plugin-side — K3 build-engine migration, the host render-leg is DELETED). See `/charly-build:build` +
`/charly-build:generate`.

**In-proc reverse channel (compiled-in placement of HostBuild / the reverse channel).** A COMPILED-IN plugin
has no go-plugin broker, so it cannot dial the gRPC reverse channel an out-of-process plugin uses.
`inprocExecutorClient` (`charly/plugin_inproc_reverse.go`) implements `pb.ExecutorServiceClient` by delegating
DIRECTLY to a host-side `executorReverseServer` (no socket); `sdk.NewInProcExecutor` + `sdk.ContextWithExecutor`
thread it onto the Invoke context, and `sdk.ExecutorForInvoke(ctx, brokerID)` (ctx-first, broker-fallback) is
the ONE accessor a plugin's Invoke calls — so the plugin's reverse-channel code is byte-identical compiled-in
vs out-of-process (placement-invisible). It deliberately does NOT reuse `executorInvoker` (which stays
grpcProvider-ONLY — that interface is the discriminator routing external verbs to `ExternalPluginStep`, and a
compiled-in provider must not collide with it); the in-proc reverse channel is threaded by the build dispatch
inline. This is the general foundation the "every builtin routes through the reverse channel" direction needs;
its first consumer is the `build` class.

## Placement: builtin OR external, in-proc OR out-of-process — at deploy AND build time

Placement is a free, per-plugin choice, invisible above the registry: a provider runs EITHER compiled into
`charly` (a builtin, in-process) OR dynamically loaded from an out-of-tree candy (an external, out-of-process
over go-plugin gRPC), and the SAME provider works in either placement with ZERO authoring change. The strategic
direction is that every internal/builtin provider migrates external over time — so author every plugin
placement-agnostic from day one, and design it to work either way.

**Every class is placement-free** — kind, verb, deploy-target, step, builder, AND command are ALL
external-capable. An external (out-of-tree) COMMAND plugin contributes a `charly <word>` CLI subcommand
dispatched OUT-OF-PROCESS: the declared word is prescanned into the Kong grammar before parse, and the provider
is LAZY-connected on the first actual `charly <word>` invocation, which forwards the pass-through args via
`Invoke(OpRun)`. `builtinCommandBase.Invoke` returning "in-process only" is the BUILTIN command path ALONE (a
builtin command contributes via its static `KongCommand()` + Go `Run` handler and never serves itself
out-of-process) — NOT a class-level limit. Each class's leg differs by lifecycle phase: verb/deploy/step/
builder ride build (`OpEmit`/`OpResolve`) and/or deploy (`OpExecute`); command rides CLI invocation (`OpRun`);
a **kind** rides CONFIG LOAD (`OpLoad`) — its serving plugin is recognized + connected at config-PARSE by the
F4 prescan (`registerDeclaredKind` + `connectDeclaredKindPlugins`, re-entrancy-guarded), so a `kind: <word>`
entity whose plugin is NOT compiled in decodes via `runPluginKind` during load. Reference (out-of-process-only):
`candy/plugin-example-kind`; loader mechanism: `/charly-internals:go` (`plugin_prescan.go`).

**A plugin DECLARES its lifecycle PHASE (F9).** Beyond its class, a capability declares a `Phase` (the
`sdk.Phase*` set: `bootstrap → schema → load → build → runtime`, default `runtime`) via
`ProvidedCapability.Phase` over Describe — the ordered point at which the kernel loads/invokes it. The
**`bootstrap`** phase runs BEFORE config validation/migration: the kernel invokes a bootstrap plugin's
`OpBootstrap` on the RAW config bytes (`runBootstrapPhase`, in `LoadUnified` before the schema gate), applying
any transformed bytes it returns — so early-running capabilities (migrate, egress) can themselves be plugins.
Bootstrap plugins are **compiled-in only** (no validated config exists yet to discover an out-of-process
source). Reference: `candy/plugin-example-bootstrap` (a no-op returning the bytes unchanged). M15 (migrate) /
M16 (egress) move those in-core capabilities onto this phase machinery.

**A kind decode is FLAT or STRUCTURAL (F5).** A FLAT kind (the default) lands its `OpLoad` body OPAQUELY in
`uf.PluginKinds[disc][name]` (F4). A STRUCTURAL kind sets `ProvidedCapability.Structural = true` (the proto
`structural` field) in its Describe — its `OpLoad` returns a `spec.Deploy` (DeployNode) MEMBER TREE the host
folds into `uf.Deploy`, the SAME map the in-proc pod/candy decoders populate, so the entity participates in
deploy/check exactly like a builtin (the folded member goes through the SAME `validateDeploy`). This is the
channel that externalizes the structural kind decoders — ALL of them are now DONE: **`group` (C2-group,
candy/plugin-group)**, **the deploy-substrate kinds pod/vm/kubernetes/local/android (C2-substrate,
candy/plugin-substrate — one provider serving all of them)**, and **the LAST one, the `candy` box⊻layer factory
(C2-candy, candy/plugin-candy-kind)** — all COMPILED-IN. The substrate consumer added the TEMPLATE-map fold
arm: a substrate node in standalone-TEMPLATE shape (a bare `vm:`/`pod:` — no from:/image:, no members) folds
into the typed map `uf.Pod`/`uf.VM`/`uf.Kubernetes`/`uf.Local`/`uf.Android`, alongside the deploy-shape fold into
`uf.Deploy`; candy folds into `uf.Box` (image) / `uf.Candy` (layer). So EVERY authoring kind is plugin-served:
the `#Node` disjunction has ZERO built-in arms (`#Node: {...}` — a structural gate only) and `spec.KindWords`
is EMPTY. (candy's bootstrap-critical box⊻layer routing — candyIsImage + buildCandy — STAYS core: the
discovered-candy pre-check calls it directly, and the COMPILED-IN candy plugin registers at init before any
load, so there is no bootstrap cycle. candy is `Structural:false` — it nests no deploy members — and routes
to the host's foldCandyKind by an explicit disc branch.)

**AUTHORED-member INPUT-threading (the enabler that makes group/substrate externalization real).** A
structural kind's whole POINT is preserving the node's AUTHORED resource-member children (peers, nested
pod-in-pod, cross-member `${HOST:…}` checks) — but they CANNOT ride `op.Params`: that JSON is unified against
the plugin's CLOSED `#<Kind>Input` def, which the member subtree would violate. So the HOST pre-decodes the
authored member children via the SAME `sdk/loaderkit.BuildDeployNode` recursion the builtin path uses,
reached through the ProjectLoader seam (`loaderkit.BuildResourceMemberChildren` — ONE member-decode source of
truth, R2) and threads the decoded subtree to
`OpLoad` via `op.Env` (`spec.StructuralKindLoadEnv{Members}`). The plugin decodes only its kind-specific scalar
body from `op.Params` and ATTACHES the host-threaded members to its `spec.Deploy` reply — Members for a
targetless kind (group), Children for a workload — so the reconstructed `uf.Deploy` entry is BYTE-EQUIVALENT to
the FORMER builtin `group`'s in-proc decode — the invariant C2-group's `check-group` bed + the
`TestExternalStructKind_StructuralDecode` byte-equivalence test both prove (`${HOST:…}` refs survive as
literals, resolved later by tree position). A
FLAT kind carrying member children is a HARD error (never a silent drop). The parser admits sub-entity children
under a recognized external STRUCTURAL kind (`externalKindMayNestMembers`), core non-resource kinds stay guarded.
Reference (out-of-process-only): `candy/plugin-example-structkind` (decodes deploy-config scalars from
`op.Params`, attaches host-threaded members); the host fold is `runPluginKind` (`/charly-internals:go`); the
byte-equivalence witness is `TestExternalStructKind_StructuralDecode` + the `check-structkind` runtime bed.

**Rich-value variant (C2-substrate + C2-candy — the HOST-pre-decode+ECHO case).** The `op.Params`-decode above
works for a kind whose value is SCALAR-simple (group's `#GroupInput`). The substrate kinds
(pod/vm/kubernetes/local/android) AND the `candy` box⊻layer factory have a RICH, core-referencing value
(`#Vm`/`#Deploy`/`#LibvirtDomain`/`#Candy`/`#Box`/… with host-canonicalized shorthand like `tunnel:`/`port:`)
that a plugin CANNOT re-decode soundly from `op.Params` nor validate with a self-contained schema. So
candy/plugin-substrate AND candy/plugin-candy-kind use the `spec.StructuralKindLoadEnv.Standalone` channel: the
HOST pre-decodes the WHOLE CANONICAL node via the core loader (`buildDeployNode` deploy / `decodeNodeValue`
template for substrate; `candyIsImage` + `buildCandy` for candy — the bootstrap-critical routing that STAYS
core), validates its value host-side against the KEPT `#<Kind>Value` / `#CandyValue` def
(`validateKindValueCUE`), and threads the canonical result via `op.Env`; the plugin is a PURE ECHO
(`InputDef:""`, no `validateAuthoredPluginInput`), and the host folds the echo into `uf.Deploy` (deploy) / the
typed template map `uf.Pod`/`uf.VM`/… (template) / `uf.Box` (candy-image) / `uf.Candy` (candy-layer).
Byte-equivalence over ALL shapes: `TestSubstrateKind_BothShapesByteEquivalent` +
`TestCandyKind_BothShapesByteEquivalent` (against the direct core decode) + box validate across all repos (candy
is THE core entity) + the `check-substrate` runtime bed. References: `candy/plugin-substrate`,
`candy/plugin-candy-kind` (distinct from `candy/plugin-candy`, the `command:candy` CLI plugin).

**A kind may serve a DEEP `OpValidate` check (F7/C8).** Beyond the static CUE input-def gate the host always
runs (`validateAuthoredPluginInput` — unifies the body against the served `#<Kind>Input`), a kind that sets
`ProvidedCapability.Validates = true` (the proto `validates` field) ALSO serves `OpValidate`: at load, the host
dispatches `Invoke(OpValidate)` with the body, and the plugin returns `spec.Diagnostics` (`{Items: [{Severity,
Message, Path}]}`) — any error-severity item FAILS the load with the messages. Use it for checks CUE cannot
express (cross-field invariants, semantic rules). Reference: `candy/plugin-example-kind` (rejects the sentinel
`marker: INVALID`); dispatch is `runPluginKind` (`/charly-internals:go`).
See "Authoring an external COMMAND plugin" below.

- **The perf invariant that makes placement free.** A builtin dispatches through its typed in-proc fast path
  (`CheckVerbProvider.RunVerb` / `KindProvider.DecodeNode` / `DeployTargetProvider.ResolveTarget` /
  `StepProvider.Emit*`) and NEVER marshals the Op into the serializable `Invoke`
  envelope; the JSON envelope (`marshalJSON`) is paid ONLY out-of-process. Choosing builtin therefore costs no
  envelope tax — placement is a free build/deploy decision, not a performance trade-off. Locked by
  `TestPerfGate_BuiltinVerbsSkipEnvelope` + `BenchmarkVerbTypedDispatchFork` (0-alloc) vs
  `BenchmarkVerbEnvelopeMarshal` (`provider_bench_test.go`).
- **Plugin↔plugin + host-build (F10).** A plugin running WITH a reverse channel (deploy/step/check/build —
  any Invoke the host stands a broker up for) can call BACK to the host to invoke ANOTHER plugin or request a
  host-side build, via the `sdk.Executor`: `InvokeProvider(class, word, op, params, env)` — the host resolves
  the peer in the registry and Invokes it on the caller's behalf (threading the SAME venue executor into an
  out-of-process target over a nested broker — the host is the dispatch broker, since it owns the registry);
  and `HostBuild(kind, spec)` — the host runs the registered host-builder for `kind` — registered kinds
  today (the count drifts per cone; `git grep 'registerHostBuilder(' charly/*.go` resolving the `*BuilderKind` constants is the authoritative list). The build/render-relevant ones: the `buildengine-*` kinds (`buildengine-prep` et al. — the thin host shards the candy's plugin-side `resolveBuildEngine` reaches: the local scan, the registry plugin connect, and the render-seam-floor `renderGenCache` populate; REPLACING the former fat `build-prep`/`build_resolve_host.go` seam, DELETED, whose loader+prep+envelope+drive-model resolve all moved plugin-side), `render-seam` (the #67 render's host-coupled seams: RenderService,
  builder resolves, ValidateEgress, EmitPluginOp, localpkg). `bake_plugin:` baking is INLINE via `deploykit.EmitBakedPlugins` (the former `bake-plugins` host-builder is DELETED — no HostBuild). The rest: `overlay`
  (the pod overlay build), `step-emit` (host-coupled step fragments), `plugin-binary`
  (the F10 plugin host build), `cli` (run-any-charly-command
  reentry), `validate-project-checks`, `remote-image-resolve`, `box-fetch-resolve` (`hostprobe` DIED with doctor's plugin-side hostfacts.go — peer InvokeProvider to verb:gpu/verb:credential; `retention-defaults` DIED at K-wave 2 cone R6 — the defaults resolve plugin-side via loaderkit.ResolveRetentionDefaultsViaExecutor; `feature` DIED at K-wave 2 cone R6 — the enumeration loads the project plugin-side via loaderkit; `render-service` DIED in the W3 B4 InvokeProvider move; `config-resolve` DIED at K-wave 2 cone R2 bank D — the consumers self-serve via loaderkit)
  (config-persist is DELETED — persist moved plugin-side to candy/plugin-vm/vm_host_persist.go), the `deploy-*-resolve` / `resolve-target-add` / `deploy-node-del-dispatch` / `deploy-plugins-connect` / `deploy-from-box` family (`deploy-members-*` DIED, K-wave W3a A4 — candy/plugin-fleet calls sdk/deploykit.BringUpMembers/TearDownMembers directly), the `loader-*` and `pod-*` (lifecycle verbs) and `pod-config-*` families, `arbiter-bracket-acquire` + `-release`, `construct-step`, `check-load-plugins`,
  `check-bed-gpu-prereq` (the ONE narrow seam surviving `check-bed`'s full dissolution, K-wave W3 B2-full — every other former responsibility moved into candy/plugin-check/bed_session.go), `check-run`. The build engine is in core TODAY — K3 build-engine
  migration inventory, not permanent core — the box-build podman DRIVE moved to candy/plugin-build in
  P8b, and the Containerfile RENDER DRIVE moved to `sdk/deploykit` (#67, driven by plugin-build over the
  envelope + the `render-seam` reverse legs; the host render-leg is DELETED). This is the shared-capability
  seam: a SHARED plugin (egress, k8s-gen, arbiter) is "a plugin others invoke", never "kept in core".
  Reference: `candy/plugin-example-dispatch`; mechanism: `/charly-internals:install-plan`
  (`plugin_dispatch_reverse.go`).
- **Deploy time.** An external deploy-target provider runs its full Add/Test/Update/Del lifecycle over the
  host-served executor reverse channel — the plugin applies the deployment's ops on the real venue it cannot
  hold across the process boundary (`OpExecute`), and the host records the returned teardown ops to the ledger.
  A bed/deploy that uses an external deploy SUBSTRATE word is recognized at config-PARSE time (before the
  provider connects) and routed host-side by the shared check classifier. **A substrate may ALSO bring its OWN
  host-side venue LIFECYCLE + PRERESOLVE (F6):** a `class:deploy` capability declaring `Lifecycle=true` /
  `Preresolve=true` is read at plugin-connect into plain `hasLifecycle`/`hasPreresolve` booleans threaded
  through `pluginDeployTarget` (`charly/unified_targets.go`, S3b) and `candy/plugin-fleet`'s generic
  `Invoke(OpDeployDispatch)` — there is no longer a SEPARATE wire-backed `substrateLifecycle`/
  `deployPreresolver` object registered at plugin-load (both interfaces, and the core files that implemented
  them, `charly/substrate_lifecycle_grpc.go` + `charly/deploy_preresolve.go`, are DELETED). Instead
  `candy/plugin-fleet`'s `lifecycleInvoke`/`preresolveSubstrate` (`deploy_target.go`) reach the substrate's
  `OpPrepareVenue`/`OpStart`/`OpStop`/`OpStatus`/`OpRebuild`/`OpPreresolve` via its OWN
  `sdk.Executor.InvokeProvider(class:"deploy", word, op, …)` (S1) — the SAME PLUGIN↔PLUGIN dispatch every
  other peer-invoke uses. `OpPrepareVenue` still returns a `spec.VenueDescriptor` the host re-materializes
  into a real executor (the live executor never crosses the wire), and `OpPreresolve` still returns the
  opaque `DeployVenue.Substrate` payload, generalizing the in-core kubernetes/android preresolvers. Reference
  (out-of-process-only): `candy/plugin-example-lifecycle`; mechanism: `/charly-internals:install-plan`
  (`plugin-fleet/candy/plugin-fleet/deploy_target.go`). This is the channel M4 reuses to externalize the pod/vm lifecycles. An external **`run:` plugin verb /
  step** composed INSIDE a deploy (a `local:`/`vm:` target, where the install runs ON the target, not baked
  into an image) likewise EXECUTES at deploy: it lowers to an `ExternalPluginStep` IR node which the external
  `local:`/`vm:` deploy walk reaches as a host-engine step over `RunHostStep`, where the shared `invokeExternalStep`
  dispatch (`charly/plugin_executor_reverse.go`, S4/R2) `Invoke(OpExecute)`s over the PLUGIN↔PLUGIN
  `InvokeProvider` leg (a nested reverse channel delegating to the SAME venue executor), so the plugin runs
  its deploy-context effect on the target and RETURNS its teardown `ReverseOp`s, which the host records to
  the ledger and replays at `charly deploy del` (record-and-replay, the SAME `spec.DeployReply` wire the
  deploy-substrate dispatch uses — R2). Only an EXTERNAL provider is routed there (the `executorInvoker` discriminator,
  satisfied SOLELY by the out-of-process `grpcProvider`); a builtin `ProvisionActor` verb keeps its in-proc
  shell path. So the verb/step class is external-capable at BOTH build (`OpEmit`, next bullet) AND deploy
  (`OpExecute`), placement-agnostic. Detail → `/charly-internals:install-plan` (the `pluginDeployTarget`
  lifecycle + the `ExternalPluginStep` IR kind + the `OpExecute` reverse channel).
- **Build time.** `charly box build` / `charly box generate` connect the project's external plugin candies during
  image generation, so a plugin EXECUTES at build to emit its Containerfile contribution, placement-agnostically
  (a builtin in-proc, an external over gRPC) — and BOTH the verb/step leg AND the builder leg ride the SAME
  connect seam, class-agnostically:
  - a `run:` plugin **verb / step** returns a Containerfile fragment via `OpEmit` → `spec.EmitReply.Fragment`,
    spliced verbatim into the Containerfile (egress-validated).
  - a **builder** (`ClassBuilder`) returns a multi-stage build via `Invoke(OpResolve)` → `spec.BuilderResolveReply`:
    its `Stage` (a `FROM <ref> AS <name>` block) is spliced PRE-main-FROM, its `CopyArtifacts`+`CopyBinary`
    (`COPY --from=<stage> …`) POST-main-FROM, and an INLINE builder's `InlineFragment` in-candy. This serves BOTH
    the four DETECTION-builders (pixi/npm/aur/cargo — selected by a candy's detect files, rendered via the shared
    `sdk/kit.BuilderResolve`, C10) AND an out-of-tree builder a candy selects with `external_builder: <word>`.
    This is the build-time BUILDER leg — the multi-stage counterpart of the verb/step OpEmit leg, so `builder` is an
    external-capable class at build too (alongside verb/kind/deploy/step). The `command` class has no build-time
    leg — a command dispatches at CLI invocation, not at build — and is external-capable THERE via `Invoke(OpRun)`
    (the Placement paragraph above), so EVERY class (kind/verb/deploy/step/builder/command) is external-capable.
  This is operator-authorized build-time execution of host-built plugin code: a project's composed external
  plugins run as host code during its image builds. Detail → `/charly-build:generate` + `/charly-internals:generate-source`.

## The retired in-charly-module builtin path

There is NO LONGER an "in-charly-module builtin" path — a Provider whose Go lived under the charly
module's former `plugin/builtins/<name>/` subtree and registered from `package main`'s `init()` via
`RegisterBuiltinPluginUnit`. It was retired as each builtin relocated into a candy: that
subtree is GONE, and the last unit, `examplerunverb`, is now the compiled-in
kit candy `candy/plugin-examplerunverb/` (guarded by `charly/plugin_examplerunverb_relocated_test.go`).
The `source: builtin` candy-linkage form is likewise gone (exampleprobe is a real candy module).

Every builtin today is ONE of the two CANDY forms below — a candy COMPILED IN via `compiled_plugins:`
("Authoring a COMPILED-IN plugin candy") or a HOST-COUPLED check-verb KIT candy ("Authoring a
HOST-COUPLED check-verb candy"). Author new builtins as one of those; there is no in-charly-module
builtins subtree to mirror.

## Authoring an EXTERNAL plugin (out-of-tree git repo)

The candy IS its own Go module — its `go.mod` carries `require github.com/opencharly/sdk` at
the shared pinned version (the require IS the resolution — the sdk contract module resolves
from the module proxy at that version; there is no local checkout and no `replace`, tag scheme
`v0.<YYYYDDD>.<HHMM leading-zeros-stripped>`, e.g. superproject `v2026.185.0751` ⇄ sdk `v0.2026185.751` —
the superproject's own `vYYYY.DDD.HHMM` tags are not valid Go module versions). A plugin module imports
ONLY the sdk module, never charly core. Mirror `candy/plugin-example-external/`:

- `schema/<name>.cue` — the self-contained def (same shape as a builtin's).
- `params/cue_types_gen.go` — generated the same way (the gen loop also covers the in-repo example).
- `main.go` — `func main() { sdk.Serve(&provider{}, &meta{}) }`; the provider decodes `plugin_input` into the
  generated `params.<Word>Input`; `meta.Describe` returns `sdk.BuildCapabilities(calver, []sdk.ProvidedCapability{…},
  schemaFS, "schema")` (it `//go:embed`s `schema/*.cue`). `BuildCapabilities` concatenates + compiles the
  schema STANDALONE (failing loudly before serving) and ships the source in `schema_cue`.
- The candy's `plugin.source` is the `github.com/org/repo/candy/<name>` ref. charly's loader fetches the repo
  (the same `@github` resolver candies use), `go build`s the provider binary ON THE HOST, and connects
  out-of-process via `LocalTransport`. The host build runs with `GOWORK=off` so a repo-root `go.work` cannot
  reject a non-member candy dir — the out-of-process build is always standalone in the candy's own module.

## Authoring a COMPILED-IN plugin candy (the candy compiled INTO charly)

The SAME out-of-tree candy can be COMPILED INTO charly — the in-proc placement of a candy, selected
per-charly-build by `charly.yml` `compiled_plugins:`. This ships a plugin candy INSIDE the binary
WITHOUT its Go living in charly's module (it rides `go.work`). `candy/plugin-example-external/` is the
reference: it is BOTH the out-of-process example above AND the compiled-in example — one provider, two
placements, ZERO authoring change.

- The provider lives in the candy's IMPORTABLE root package (`NewProvider() pb.ProviderServer` +
  `NewMeta() pb.PluginMetaServer` + `//go:embed schema/*.cue`), NOT `package main`. `main.go` moves to
  `cmd/serve/` as a 3-line `sdk.Serve(<pkg>.NewProvider(), <pkg>.NewMeta())` shim (the out-of-process
  entrypoint, host-built only when the candy is NOT compiled in).
- List the candy in `compiled_plugins:` (the embedded `charly/charly.yml` for the default binary; a
  consumer's own `charly.yml` for a custom footprint — "which plugins are in the binary" is a normal
  candy-inclusion choice).
- `pluginsgen` (`charly/internal/pluginsgen`, run by `scripts/bootstrap-charly.sh` before `go build`, `GOWORK=off`,
  stdlib+yaml only) reads `compiled_plugins:` and emits `charly/plugins_generated.go` (one
  `registerCompiledPlugin(<pkg>.NewProvider(), <pkg>.NewMeta())` per candy) + the repo-root `go.work`
  (`use ./charly` + a `use ./candy/<name>` per candy, so `go build ./charly` resolves the
  candy imports; the sdk + spec contract modules resolve from the module proxy at the
  pinned require versions — no submodule, no checkout). Both are COMMITTED + reproducibility-gated (`TestPluginsGenReproducible`).
- `registerCompiledPlugin` drives `InProcServedTransport`: it calls the candy's `Describe` IN-PROCESS, runs
  the SAME `buildUnit` capability-lift + schema gate an external goes through (so the compiled-in schema
  enters the SAME `loadBuiltinPluginUnits` gate), and registers each capability wrapped in an
  `inprocProvider` (origin `"builtin"`) — the in-proc twin of `grpcProvider`. Dispatch is registry-routed,
  transport-invisible.
- COEXIST: a candy compiled in (origin `"builtin"`) is SKIPPED by `loadProjectPlugins` (the out-of-process
  host build is redundant); a candy NOT in `compiled_plugins:` still host-builds + connects out-of-process
  when a plan references its word. Placement is a per-charly-build choice, invisible above the registry.
- PERF: a compiled-in pb CANDY dispatches through the pb `Invoke` envelope IN-PROCESS (no socket) via
  `inprocProvider`, whereas a HOST-COUPLED KIT candy (next section) uses its typed fast path
  (`RunVerb`/`DecodeNode`, no envelope). The pb-candy path pays the JSON envelope but not the gRPC transport.
- TESTING a compiled-in candy against an UNMERGED checkout: it resolves from the module proxy at its pinned
  require, so a stock binary runs the PUBLISHED candy and no bed can fail on your edit.
  `scripts/bootstrap-charly.sh --dev-plugin <candy-name>=<repo-checkout>` (repeatable) makes `pluginsgen`
  read that repo from the checkout instead and emits a SEPARATE `go.work.dev` selected only under an
  explicit `GOWORK=` (the committed `go.work` stays byte-identical — it has ONE member, whose `go.sum`
  carries every hash, so the workspace has no checksum lock of its own; a later build WITHOUT the
  flag deletes it, and `--install` is refused). It validates the name is in `compiled_plugins:` and that
  `candy/<name>/go.mod` declares module `github.com/opencharly/<name>/candy/<name>`. Then prove the changed
  path EXECUTED — `go version -m` (`(devel)` vs the module-proxy tag) is necessary but not sufficient; a
  symbol-set difference over `go tool nm` beats an anchored `grep`, which misses every method. Standard 8
  of the `check` skill's `beds-and-r10` reference carries the full rule.

## Authoring a HOST-COUPLED check-verb candy (the kit — dual-placement via the reverse channel)

A check verb whose logic needs the LIVE check engine — exec-in-container, host-vantage HTTP, host TCP
dial (`file`/`http`/`port`/`command`/`service`/…) — implements **`sdk/kit`**, the importable
contract for the check engine. It runs in EITHER placement, invisibly above the registry: COMPILED-IN
(charly passes the live `*Runner` as the `kit.CheckContext`) OR OUT-OF-PROCESS (the CheckContext legs are
served back over the host's reverse channel — ExecutorService for `cc.Exec()` + CheckContextService for
`cc.HTTPDo`/`cc.AddBackground`, F2 — while the scalar legs `Mode`/`Box`/`Instance`/`Distros`/`DialTimeout`
ride the env_json snapshot). `candy/plugin-port` + `candy/plugin-http` are the reference (both served
OUT-OF-PROCESS in the default build); the remaining kit candies are compiled-in by default until M1 adds
their `cmd/serve` shim. Shape:

- The candy's importable root package implements `kit.CheckVerbProvider` (`Reserved()` + `RunVerb(ctx,
  cc kit.CheckContext, op *spec.Op) kit.Result`) — the verb's logic (formerly the `r.runX` *Runner
  method) lives HERE, reaching the deployment through `cc.Exec().RunCapture` / `cc.Mode()` /
  `cc.DialTimeout()` / `cc.HTTPDo(...)` / `cc.Distros()`. It exports `NewCheckVerb() kit.CheckVerbProvider`
  + the raw schema `embed.FS` (`SchemaFS` + `SchemaDir`) + `InputDefs`.
- COMPILED-IN: charly's `registerCompiledCheckVerb` (generated into `plugins_generated.go` by pluginsgen,
  which detects the kit shape by the exported `NewCheckVerb`) wraps it in a `kitVerbAdapter` (a package-main
  `CheckVerbProvider` that passes the live `*Runner` as a `kit.CheckContext` via `runnerCheckContext` and
  converts `kit.Result`→`CheckResult`), concatenates the candy's schema (via the public
  `spec/schemaconcat`), and registers through the SAME `RegisterBuiltinPluginUnit` gate (origin
  `"builtin"`). Dispatch is the SAME `runOne`→`CheckVerbProvider.RunVerb` path a compiled-in candy verb
  uses — full typed fast path, the real `*Runner`, no envelope.
- OUT-OF-PROCESS: a `cmd/serve/main.go` shim calls `sdk.ServeCheckVerb(pkg.NewCheckVerb(), calver,
  pkg.SchemaFS, pkg.SchemaDir, pkg.InputDefs)`, which wraps the kit verb in a pb.ProviderServer whose Invoke
  reconstructs a `sdkCheckContext` from the InvokeRequest broker (ExecutorService + CheckContextService) +
  the env_json snapshot, then runs `RunVerb`. The host go-builds `./cmd/serve` + connects via
  `LocalTransport` when the candy is NOT in `compiled_plugins`; `invokeVerbProvider` (provider_checkenv.go)
  serves BOTH reverse services on one broker id. The verdict round-trips as the same `{status,message}`
  every out-of-process verb returns.
- `kit` imports the stdlib + `spec/spec` + `sdk/vmshared` + the pinned external helpers (`gopkg.in/yaml.v3`, `golang.org/x/sys/unix`) — never the `sdk` root module; the candy imports `kit` + `sdk` + `spec` + its `params`.

A kit candy keeps the verb's logic (RunVerb on `kit.CheckContext`) OUTSIDE charly's module while preserving
the typed fast path — runnable in-proc (compiled-in, the real `*Runner`, no envelope) OR out-of-process (the
reverse channel) with ZERO authoring change.

The SDK module (`github.com/opencharly/sdk`) is the ONLY module a plugin imports — root package
`sdk` (`Serve`, `ServeCheckVerb` (the kit-verb out-of-process serve entry), `Handshake`,
`BuildCapabilities`, `ProvidedCapability`, `Conn`, plus the shared out-of-process check-verb helpers
`ResultJSON` (the `{status,message}` reply) / `CheckRequiredModifiers` (the required-modifier check) +
the `*Executor` venue methods `VenueCapture`/`VenueHasTool`/`VenueRunSilent`), `sdk/kit` (the
pure-helper package, stdlib + `spec/spec` only — `ShellQuote`, `TrimPreview`, `MethodSpec`,
`WalkPlans`, …; the SDK root imports it too), `spec/spec`, and `spec/proto`. `schemaconcat` is the
public `spec/schemaconcat` package (the SDK uses it internally).

## Authoring an external COMMAND plugin (a `charly <word>` subcommand)

A `command:<word>` provider is an external plugin (authored exactly like the verb-class one above — its own Go
module, self-contained `schema/<name>.cue`, generated `params/`, `sdk.Serve` + `BuildCapabilities`) that
contributes a TOP-LEVEL `charly <word>` CLI subcommand. Mirror `candy/plugin-example-command/`:

- `plugin.providers: [command:<word>]` + `source: github.com/org/repo/candy/<name>` in the candy's `plugin:`
  block; `Describe` returns the capability with `Class: "command", Word: "<word>"`.
- `Invoke` handles `OpRun` (the command-run selector): the host forwards the user's pass-through CLI tokens
  from `charly <word> <args…>` as `op.Params = {"args": [...]}` marshalled DIRECTLY — NOT wrapped in the
  `plugin_input` envelope a verb CHECK step uses. The provider decodes that into its generated typed params
  struct (e.g. `params.<Word>Input` with an `Args []string` field) and does its effect.

The discovery → grammar → dispatch flow (host side, owned by `charly/plugin_command_prescan.go` +
`provider_command_external.go`): when the candy is discovered in the project, a byte-gated prescan
(`prescanProjectCommandWords`, run in `main` BEFORE `kong.Parse`) registers the declared word so `charly <word>`
PARSES; `collectExternalCommandPlugins` builds a Kong grammar holder for it with the provider UNconnected. The
host build + gRPC connect is LAZY — paid ONLY when the user actually runs the command: `dispatchExternalCommand`
calls `connectCommandPlugin` (scoped to the one word) then `Invoke(OpRun, {"args":[…]})`. So every `charly`
invocation that is NOT `charly <word>` is byte-for-byte unaffected. (A builtin command, by contrast, contributes
via its compiled-in `CommandProvider.KongCommand()` + Go `Run` handler — `provider_command.go`.)

**A command candy can ALSO be COMPILED IN (F8) — placement-invisible like every other class.** List its candy
in `compiled_plugins:` and it registers in-proc (an `inprocProvider`, Class command); the host builds the SAME
dynamic Kong grammar (`externalCommandHolder`, pass-through `Args`) and `dispatchCommand` routes it IN-PROC via
`Invoke(OpRun)` (`dispatchInProcCommand`) instead of `syscall.Exec` — the candy's `Invoke(OpRun)` handler runs in
charly's own process (native stdio/TTY). So author a command candy DUAL-PLACEMENT: an importable provider
package (`NewProvider`/`NewMeta`, `Invoke(OpRun)` runs the effect, `Describe` advertises `command:<word>`) + a
`cmd/serve` `sdk.Main(..., CliMain)` shim for the out-of-process path, both calling ONE shared effect (mirror
`candy/plugin-example-command`). This is the command half of compile-in-for-all-six-classes; the M-series moves
the dedicated builtin commands into candies on this surface.

