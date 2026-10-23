## The kernel/plugin boundary law — what belongs in the kernel vs a plugin

This is the authoritative detail for the project rulebook **"The kernel/plugin boundary law"** pillar. It decides,
uniformly and with NO per-kind exception, whether any schema / typed shape / validation / behaviour is
KERNEL (`charly/` core + `sdk/`) or PLUGIN. Read it before adding or placing any capability.

**The law.** The kernel is a **kind-blind execution substrate**: a construct is KERNEL iff it is one of
four kind-AGNOSTIC things —

- **(E) Envelope** — a generic carrier or the reserved vocab naming its slots: `#Node`(arm-less) /
  `#Step` / `#Op` / the `#Deploy` tree / the `InstallPlan` IR / `#Context`, and the wire replies plugins
  resolve into (`VenueDescriptor`, `EmitReply`, `BuilderResolveReply`, `DeployVenue`, `StepEmitRequest`,
  the opaque `Substrate json.RawMessage`). Carries opaque, word-tagged payloads; encodes no kind.
- **(M) Mechanism** — a generic engine that transports / renders / validates / re-materializes an
  envelope, dispatched **by word against a data table, never branching on a concrete kind** — and the
  ONLY M-mechanisms that live in `charly/` are plugin loading (the provider registry + transports +
  reverse-channel legs), prescan-dispatch (the SAME registry resolve+invoke a per-node kind-decode
  reaches, e.g. `provider_kind_invoke.go`'s `runPluginKind`/`foldSubstrateKind`/`foldCandyKind` — an
  elaborated CONSUMER of this mechanism, not a distinct one), and the wire broker. The FOLD/ORCHESTRATION
  half of kind-decode MATERIALIZE — deciding what to do with a resolved-or-unresolved node, the
  not-found policy — is NOT this mechanism: it moved to `candy/plugin-loader`'s `Materializer` seam
  (K1 unit 1, mirroring `ProjectWalker`/`DocParser`), so the in-core M set is now THREE, not four. Every
  OTHER kind-blind mechanism (parse, render, resolve, walk, engine) lives in an sdk KIT consumed by plugins:
  the config PARSE is `sdk/loaderkit` (plugin-served via the `DocParser` seam); the build engine, the
  deploy walk, the CUE-unify check, and the ledger currently in `charly/` are TRACKED MIGRATION
  INVENTORY with named K-wave exits (K3/K4/K5), never permanent M-residue. Canonical illustration:
  `loaderkit.ParseDoc` stays kind-blind by reading the `loaderkit.Threaded` kind-recognition snapshot
  — clause-D DATA threaded to a kind-blind mechanism, not a registry query. A kind-blind mechanism
  reached THROUGH a seam (a reshaper, the migration op-walker) is itself a PLUGIN/sdk-lib, NOT core.
- **(B) Bootstrap** — the single irreducible root that must exist before any plugin can load: the
  `candy`⊻`box` factory (`candyIsImage`+`buildCandy`) + the provider-registry seed. It cannot be a
  plugin without a bootstrap cycle (the discovered-candy pre-check calls it directly).
- **(D) Data-not-code** — a kind-recognition fact (which words are kinds / nest members / validate
  against which value-def) loaded from CUE/config and consulted by word: the CUE-derived vocab
  (`spec.OpVerbs`/`StepKeywords`/`DocDirectives`/`ResourceKinds`), the schema-version consts, the
  `#Migration` grammar, and the canonical example — `loaderkit.Threaded`, the 4-map DATA SNAPSHOT the
  host fills from the registry BEFORE the kind-blind parse (untying the loader↔registry cycle at zero
  abstraction cost — DATA threaded to a kind-blind mechanism, never a live registry query in the
  parse). NEVER a compiled-in per-kind Go branch or map.

- **(R) Resolve-to-envelope — the DEFAULT.** Everything else — a kind's schema, typed Go shape,
  deep-validation def, render/behaviour, and produced artifact — is a PLUGIN. It reaches the kernel only
  by **resolving** its config into an E-envelope that a Mechanism consumes.

**The decision procedure.** For any construct ask: generic Envelope (E)? a kind-blind Mechanism that is
plugin loading / prescan-dispatch / the wire broker (M — nothing else in core)?
the Bootstrap root (B)? kind-recognition Data (D)? If none → it is a plugin (R). Placement
is decided ONLY by this test; **difficulty NEVER enters it** (the project rulebook forbidden-excuse catalog) — a
thing stays kernel only because it is E/M/B/D, never because moving it is hard.

**The un-gameable self-test — the "incomplete seam".** The four kernel escapes are each kind-AGNOSTIC and
may never name a concrete kind in code. So a kernel `import` of a `spec.<Kind>` struct read for its
fields, a `switch` on a kind word, or a per-kind Go map is BY DEFINITION an R-item that leaked — an
**incomplete seam: a bug fixed immediately (one R10-gated cutover each), never a kept exception, never
parked as a "remaining candidate."** Those three tells are what you grep for.

**The per-kind-map tell — spelled out, because it is the one most often self-excused.** A
`map[string]string` keyed by reserved words (`deploy:<word>` / `verb:<word>` / `<class>:<word>`), or a
helper returning a hardcoded `github.com/opencharly/*/candy/*` ref, IS that third tell: a hand-written
word→provider table — clause-D kind-recognition Data encoded as kernel code, an R-item that leaked.
The exact crutch this doctrine removed: `externalDeploySubstratePlugins` (a substrate-word→candy map)
and `vmPluginCandyRef` (a single-verb candy-ref literal) both lived in `charly/`, both carried a
stays-core header claiming they "do NOT need CUE-sourcing" — a CLAIM, overruled (the enforcement
paragraph below) — and both are DELETED. The structural teeth that keep them from regrowing is
`charly/no_provider_ref_map_gate_test.go`'s `TestNoProviderRefMapInKernel`: it fails any production
(non-`_test`) `.go` file carrying a candy-ref literal or a `map[string]string` keyed by a capability
word, outside the one generated index file. Its exemption list is EXPECTED-EMPTY; an entry is a
boundary-law decision named with its reason, never a quiet allowlist.

**The generated-index precedent — where a word→provider fact actually lives.** The fact is real, but
it is clause-D Data OWNED BY THE PLUGIN REPOS, never a kernel table. Each plugin's own manifest
declares it — `plugin.providers:` (the words it serves) + `plugin.source:` (its canonical candy ref).
`charly/internal/pluginsgen` enumerates the plugin corpus and projects every manifest into ONE
generated table, `pluginProviderRefs` (`charly/plugins_refs_generated.go`, marked DO NOT EDIT), keyed
by the `<class>:<word>[:<parent>]` identity. Core reads that table as Data through the ONE
class-agnostic lookup — `canonicalProviderRef` (→ `pluginProviderRef`): a `deploy:vm` substrate and a
`verb:libvirt` verb resolve through the same table, no per-class branch, no special case. Adding a
word therefore costs a PLUGIN-REPO edit only — declare it in the plugin's own `plugin:` block, the
regenerated index carries it, and charly core changes ZERO lines. The index is OPEN by construction
(exactly the words plugins declare), so there is no closed vocabulary to keep in sync: the former
`checkDeployProviderBijection`, which asserted the reverse direction (vocabulary→ref) and so forced a
core edit the moment `spec` widened the vocabulary, is DELETED. The generator is the anti-drift
teeth — a second run is a byte-identical no-op (`charly/internal/pluginsgen/main_test.go`).

**The named trap — "host-boundary object" is never a permanence reason.** Calling a construct a
"host-boundary object" — it "can't cross the process boundary," "drives podman/ssh/flock/systemd itself,"
or fits "the P8/P10 pattern" — is NEVER grounds to keep it in core. A plugin runs on the SAME host and
drives that object itself (exactly as `candy/plugin-vm` and `candy/plugin-kube` already do), or reaches a
live venue through the reverse-channel broker (M4, the `substrateLifecycle`/`ExecutorService` legs) —
needing the broker or the host IS the whole point of that seam, never a reason the construct must stay
core.

**Enforcement — the trap is checked, not just named.** A `// … STAYS CORE` / "cannot cross the process
boundary" header comment is a CLAIM, never a verdict on its own: a mover reports where each externalized
piece LANDED (never a bare "moved" claim) and gives any REMAINDER its own E/M/B/D justification, never one
inherited from the moved majority; a validator REJECTS a remainder whose only justification is a
stays-core header, demanding call-chain evidence first; and the orchestrator audits every stays-claim
against this boundary law with that evidence, never rubber-stamping the header. Precedent:
the former `charly/host_build_deploy_add.go` (the `deploy-add` HostBuild seam, DELETED, K-wave 2)
documented that the `charly deploy add` CLI moved to `command:deploy`
(candy/plugin-fleet, P13) while the deploy KERNEL it drives "STAYS CORE" on exactly this "cannot cross
the process boundary" claim — overruled as a boundary-law violation (the deploy-dispatch kernel is
tracked K-wave residue, not permanent core). Full three-role breakdown: `/charly-internals:agents`
"Enforcement — 'host-coupled' is never a permanence reason".

**The defines-vs-calls test — the concrete grep that makes E/M/B/D un-mis-applicable.** To decide whether
a file is a genuine M-mechanism (kernel) or an R-item (moves), grep for where the mechanism is DEFINED —
the registry in `provider_registry.go`, the resolve+dispatch invocation in `provider_kind_invoke.go`, the
reverse-channel broker/`InvokeProvider` in `plugin_inproc*.go`/`plugin_dispatch_reverse.go`. A file that
only CALLS one of those — dispatches through `providerRegistry.ResolveVerb`, composes a live executor,
reads resolved config — is a CAPABILITY that USES the mechanism, not the mechanism itself: an R-item, it
moves. **This is the exact test that caught `materialize.go`'s own former "stays core, clause M"
self-classification (K1 unit 1): it never DEFINED the registry/dispatch mechanism, it only CALLED
`providerRegistry.ResolveKind` transitively — an R-item that self-misclassified. Its per-node NOT-FOUND
policy moved to `candy/plugin-loader`'s `Materializer` seam; only the TRUE mechanism it called
(`provider_kind_invoke.go`) stays core. A capability that uses M1–M4 is not thereby M1–M4.**

**Alias-is-always-residue — an alias is NEVER permanent, regardless of what it aliases.** A `type X =
spec.X` / `var y = spec.Y` re-export in `charly/` is ALWAYS an R-item under the ZERO-ALIASES v2 target,
REGARDLESS of what it aliases — even a genuine E-envelope type. The alias is not "permanent because it's
an E-type"; the alias IS the mislocated call site, the whole point of ZERO-ALIASES. Classify every
`charly/*_aliases.go` (and stray `_alias.go`) entry as residue: delete it and repoint its callers to
`spec.*` (or the owning kit) directly. Failure mode: an auditor counts `labels.go`'s `BoxMetadata =
spec.BoxMetadata` alias and `install_plan.go`'s `Scope`/`Venue`/`Phase` aliases (all grep-verified genuine
`spec.*` E-types) as "permanent E," then self-corrected — an alias's TARGET being E doesn't make the alias
itself permanent.

**A concrete kind's typed shape is R, not E.** The (E) envelope bucket is ONLY kind-AGNOSTIC carriers —
`InstallPlan`, `VenueDescriptor`, `#Node`/`#Step`/`#Op`, the wire replies. A struct NAMED after a concrete
kind, with kind-specific fields and accessors, is that kind's TYPED SHAPE: an R-item, moving to its
owning kit/plugin. `ResolvedBox` (now `spec.ResolvedBox`, CUE-generated) is the already-relocated
precedent. "Carries data" is not the E test; kind-AGNOSTIC is. `layers.go` holds only kind-blind scan/parse
functions; the typed shape lives in the `spec.CandyModel`/`spec.CandyView` pair.

**The practical audit framing — invert the default.** Every file is an R-item (it moves to a plugin)
UNLESS it is LITERALLY one of the tiny kernel whitelist: the three in-core M-mechanisms (plugin loading /
prescan-dispatch / the wire broker), the B bootstrap root, D kind-recognition
data, or an E generic envelope. Auditing a cone by asking "is this hard to move" instead of "is this one
of E/M/B/D" is the exact failure this framing forecloses: the bed runner, the check/ADE harness, the build
engine, the deploy walk, and the VM engine are ALL R-items (the v2 ledger already names them plugins) —
none of them earns kernel status by being awkward to relocate. Failure mode: auditors mis-classify the deploy + check cones as
~90% permanent using exactly the "host-boundary/gather-engine"
excuse; the true kernel in those cones is ~0.3%.

**The canonical shape every kind copies — resolve-to-envelope.** `builder` is the reference: its plugin
serves its own `#BuilderInput` schema, decodes into its own params, validates its own input, and RESOLVES
via `Invoke(OpResolve)` → `spec.BuilderResolveReply` (`Stage`/`CopyArtifacts`/`InlineFragment`); the build
engine splices the generic fragment and never imports a builder struct. Every concrete kind reaches the
kernel the same way — a distro resolves to install/phase/localpkg fragments, a substrate to a
`VenueDescriptor` + `InstallPlan`, a sidecar to a peer-`Deploy` fragment, a resource to a device fragment.
The kernel consumes the generic envelope; the plugin owns the schema, the validation (`OpValidate`), and
the resolution. The build-time `OpEmit` (step/verb fragment), the F6 lifecycle/preresolve legs a substrate
provider serves over its own `InvokeProvider(class:"deploy", word, OpPrepareVenue/OpPreresolve/…)`
(`VenueDescriptor`; S3b generalized this off the former dedicated `grpcSubstrateLifecycle` proxy into the ONE
generic `candy/plugin-fleet` `OpDeployDispatch` dispatch every substrate now shares), and the
`HostArbiter`/`HostBuild`/`InvokeProvider`/`RunHostStep` reverse legs are the
existing seams a de-typing rides — no new seam is usually needed, only the consumer stops re-typing.

**Every K-wave move is a generalization, not a mechanical relocation.** Moving a capability's call site
into its owning plugin is R2 ("no duplication; generic, reusable solutions over ad-hoc patches") and
Prioritize Clean Architecture applied to the migration itself (see the project rulebook — not restated here). In
practice: reach for an EXISTING generic seam first (`HostBuild`, `InvokeProvider`, `OpResolve`/`OpEmit`,
the substrate lifecycle/preresolve `InvokeProvider` legs, `OpDeployDispatch`) or build ONE generic, F11-reviewed, class-generic action noun — never a
per-word or per-case one; COLLAPSE per-case branches into a single word-keyed data-driven mechanism as
part of the move (per-substrate status collectors + a `switch node.Target` incomplete-seam become ONE
generic `OpStatusCollect`; per-kind `cue_kind` branches become one family); and DELETE every duplicate and
transitional alias the move obsoletes — never re-export it. Rule of thumb: reuse an existing generic seam
or delete the duplicate — rarely invent a new one; most seams the boundary law needs already exist.

**Realized architecture (present state).** The SDK boundary (`github.com/opencharly/sdk`) is extracted;
every verb / kind / step / builder / command, all five deploy substrates (local/pod/vm/kubernetes/android) with
their pod + vm venue lifecycles, and egress / k8sgen / gpu / arbiter / secrets / enc / tunnel are plugin
candies over the generic seams (`HostBuild("overlay"/"cli"/"step-emit")`, the `candy/plugin-fleet`
`OpDeployDispatch` orchestration + each substrate's own `InvokeProvider` lifecycle/preresolve legs (S3b,
replacing the former dedicated `grpcSubstrateLifecycle` proxy), the
`ExecutorService` reverse legs, the opaque `Substrate`/`DeployVenue.Substrate` payloads). Where a concrete
kind's TYPED shape is still consumed in core — a `spec.<Kind>` field-read, a `kindValueDef`-style per-kind
map, a substrate-word `switch` — that is a known **incomplete seam** being closed one cutover at a time
under this law; the active inventory + sequence live in the cutover plan + each repo's `CHANGELOG/`, never
as a snapshot here.

**Whole subsystems obey the same law.** A generic subsystem is kernel ONLY as one of the three in-core
M-mechanisms (plugin loading / prescan-dispatch / the wire broker); every other
kind-blind mechanism is an sdk kit consumed by plugins. The subsystems still carrying non-trivial core
weight — the build engine, the deploy walk, the OCI `registry.go`+`merge.go` (go-containerregistry), the
status subsystem, the LoadUnified orchestration, every `*_aliases.go` — are v2 migration INVENTORY, each
with a named K-wave exit (K1/K3/K4/K5), never permanent residue: where the boundary test says plugin, it
is an incomplete seam fixed as its own cutover with a named exit, never an indefinite candidate.

## Target architecture (v2) — the direction the boundary law drives toward

The boundary law above is the rule; this is the end-state it converges on. The project rulebook carries the mandate ("Core is a PLUGIN HOST"); this is its operationalization.

**The architecture, one sentence.** `charly/` core is a plugin host and nothing else — it loads plugins,
dispatches to plugins, and brokers the wire; it does not parse config, resolve, build, deploy, or check,
it does not consume the sdk mechanism libraries, and it contains zero aliases/shims.

**Three rules make this mechanically enforceable (the P16 triple gate checks all three).**

1. **Everything is a plugin.** Every capability — including the project loader, the deploy walk, the
   build engine, and the bed runner — lives in a plugin candy. Core's only jobs: discover/load plugins
   (compiled-in registry + go-plugin gRPC), prescan the CLI grammar from plugin-declared words, dispatch
   words→plugins (including the per-node kind-decode resolve+invoke a plugin's `Materializer` seam calls
   back into — the fold/not-found policy itself lives in `candy/plugin-loader`), and broker
   the reverse channel (venue executors + `InvokeProvider`). → P16 gate (a): the import-surface assertion (charly/import_purity_test.go — every charly/ file imports only spec/* + the proto/plugin-api contract + vetted third-party; the per-file allowlist is retired).
2. **Core does not import the sdk mechanism layer.** Core imports only the protocol contract — `spec/spec`
   (wire types) + the proto/go-plugin packages + the Provider/Op vocabulary. `sdk/{kit,deploykit,
   buildkit,loaderkit,vmshared,…}` are for plugins. → P16 gate (b): import-purity (`charly/` has ZERO
   `github.com/opencharly/sdk` references — prod, test, and go.mod; the #55 terminus closed the last tracked residuals).
3. **Zero aliases/shims.** Every `charly/*_aliases.go` (`type X = deploykit.X`, `var y = kit.Y`) is a
   mid-cutover crutch that keeps a capability call site in core; the fix is never an alias — it is moving
   the call site into its owning plugin. → P16 gate (c): the `charly/*_aliases.go` glob is empty.

**Why the seams die.** Today's oci-inspect seam (the config-resolve / config-persist seams are
ALREADY DELETED — the consumers self-serve via `sdk/loaderkit`, K-wave 2 cone R2 bank D) exists
only because plugins could not load the project or touch the store. Once the loader is
`sdk/loaderkit` (the kind-blind parse) and state is the flock'd,
any-process-safe `sdk/kit` state family (`filelock.go` + `install_ledger.go` + `deployconfig.go`), a
plugin just loads the project itself — same filesystem, same library — and the seam
families collapse. The reverse channel then shrinks to the two things that genuinely cannot cross a process
boundary: **live venue executors** (re-materialized from `VenueDescriptor`) and **`InvokeProvider`**
(peer-plugin dispatch through the host's registry), plus plugin-binary/cli reentry. Everything else,
plugins do directly via sdk libs.

**Target kernel — the honest floor.** Roughly 3.5–4.5k LOC: `main()` bootstrap + the compiled-in plugin
registry + plugin cache/loading (~1k); the provider registry + in-proc/gRPC transports + prescan (CLI
words, kinds) + the per-node kind-decode resolve+invoke (~2k); the reverse-channel broker (executor
re-materialization + `InvokeProvider`, ~1–1.5k). Everything else in today's LOC total moves to sdk
mechanism libraries (loaderkit / enginekit + the existing kit / deploykit / buildkit) consumed by plugin
candies (plugin-project, plugin-build, plugin-fleet, plugin-check, plugin-box, plugin-status, plugin-oci,
the deploy-substrate plugins, the command plugins).

The active migration-wave sequencing and each wave's tracked residue exit live in the cutover plan and
each repo's `CHANGELOG/`, never as a snapshot here — a wave-by-wave ledger drifts the moment a wave lands.
P16 lands last, with all three gates green. GPU host-detection legs are the operator-dropped exception
(revisitable on hardware), not a K-wave item.
