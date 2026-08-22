// CUE schema for the COMMAND-time host↔plugin seam wire types (P10). A
// compiled-in command plugin (candy/plugin-vm's command:vm leg) owns its CLI
// handlers but cannot LoadUnified, hold the deploy ledger, or run the VM-disk
// build engine — those are core Mechanisms a plugin (a separate module importing
// only sdk) reaches over the in-proc reverse channel. The config READ (the
// "config-resolve" seam) + the ledger write (candy/plugin-vm/vm_host_persist.go)
// and the VM-disk build (candy/plugin-vm/vm_build_resolve.go) moved PLUGIN-SIDE —
// the former config-resolve / config-persist / vm-build HostBuilds are DELETED.
// Each action noun is CLASS-GENERIC (never a substrate word — the F11 uniform-API
// gate); pod (P11) + fleet (P13) reuse the same seams.
//
// Package-less; concatenated into the spec compilation unit. NOT authoring kinds
// (never in #Node/#Op) — pure generated wire types, single-sourced here so
// `task cue:gen` produces the Go structs (WIRE TYPES ARE CUE-SOURCED WITHOUT
// EXCEPTION, CLAUDE.md SDD). Fields that carry a hand-written runtime type with
// NO CUE def (*ResolvedVm, map[string]*ResolvedResource) travel as opaque
// bytes/RawBody envelopes the consumer marshals/unmarshals at the boundary (the
// #ParsedNode `body: bytes @go(...,type=RawBody)` idiom); fields whose type HAS a
// CUE def reference it directly (#Deploy is fully generated; #VmDeployState is a
// def with a hand materialization, referenced via @go(...,type=*VmDeployState) —
// the same shape #Deploy.vm_state uses). @go names match the Go field names the
// host + plugin consumers reference so the CUE-source flip is call-site-invisible.

// #PodDisposableRequest asks the host whether a per-host POD deploy overlay entry is disposable
// (K5-U2/3). This is the ONE AI-harness check-project fact the resolved-project envelope cannot
// carry: the harness's iterate sandbox is an OPERATOR-provisioned per-host deploy (`charly fleet
// add <sandbox> <ref> --disposable`), so its disposability lives in the per-host overlay
// (LoadFleetConfig → ~/.config/charly/charly.yml), NOT the project charly.yml the resolved-project
// envelope projects (Mode Purity keeps the overlay out of the build-mode projection). The overlay
// read needs the core loader a plugin cannot import, and no deploy/status provider serves it, so it
// rides this THIN retained host seam. The host returns Fleet[Name].IsDisposable() (false when the
// sandbox has no entry — the harness then skips its fresh-per-run restart). Class-generic action
// noun "pod-disposable" (F11 — never a substrate word).
#PodDisposableRequest: {
	name!: string @go(Name) // the per-host pod deploy name (the iterate sandbox)
}

// #PodDisposableReply carries the single overlay-disposability bit.
#PodDisposableReply: {
	disposable?: bool @go(Disposable)
}

// #DeployOverlayRequest asks the host for the PER-HOST deploy-config overlay (K4:
// deploykit.LoadDeployConfigForRead — the runtime ledger at ~/.config/charly/charly.yml, NOT the
// project charly.yml the resolved-project envelope projects; Mode Purity keeps the two apart, same
// distinction #PodDisposableRequest documents). Unlike #PodDisposableRequest (a single overlay
// BIT), several pod-lifecycle resolvers need CROSS-DEPLOYMENT visibility (deploykit.FleetConfig's
// GlobalEnvForImage/OccupiedHostPorts/DeployedContainerNames all read OTHER deploys' entries, not
// just the caller's own), so a single-field extraction can't serve them — the host returns the
// WHOLE marshaled *deploykit.FleetConfig and the plugin calls the SAME already-portable
// deploykit methods locally. Re-fetched on EVERY call (no caching): the ledger can change between
// invocations (an intervening `charly config`), and PrepareVenue-time data is stale by the time
// OpStart/OpStop/OpShell run much later — this is NOT threaded through the one-shot
// LifecyclePrepareInput. Class-generic action noun "deploy-overlay" (F11 — never a substrate
// word).
#DeployOverlayRequest: {
	context!: string @go(Context) // caller label for host-side diagnostics, e.g. "charly start tunnel"
}

// #DeployOverlayReply carries the marshaled per-host FleetConfig. config_json is the JSON
// encoding of *deploykit.FleetConfig (nil-safe: absent/null when no per-host overlay file
// exists yet, matching LoadDeployConfigForRead's own nil-FleetConfig contract).
#DeployOverlayReply: {
	config_json?: bytes @go(ConfigJSON,type=RawBody)
}

// #VmBuildRequest carries the `charly vm build` command flags (the former
// VmBuildCmd fields). candy/plugin-vm/vm_build_resolve.go resolves the kind:vm
// entity + the build vocabulary + the per-source-kind image refs into a #VmBuildReply
// envelope PLUGIN-SIDE (the former "vm-build" host-builder is DELETED — PREP+RESOLVE
// moved into the plugin, P8b-rest); the plugin's `charly vm build` command then runs
// the actual privileged-container / qemu-img / bootc-install / cloud-init disk-build
// ENGINE itself, exactly as candy/plugin-build's podman DRIVE runs behind
// HostBuild("buildengine-prep") (P8b) — the same inversion, applied to the VM disk-build engine.
// force skips the cloud_image content-freshness check, forcing a base-disk rebuild even when
// unchanged (P8b-rest: `--force` predates command:vm's P10 externalization but was dropped from
// this seam then — restored here since BuildCloudImage's force parameter is load-bearing).
#VmBuildRequest: {
	box!:       string @go(Box)
	size?:      string @go(Size)
	root_size?: string @go(RootSize)
	tag?:       string @go(Tag)
	type?:      string @go(Type)
	transport?: string @go(Transport)
	console?:   bool   @go(Console)
	force?:     bool   @go(Force)
}

// #VmBuildReply is the resolveVmBuild reply (P8b-rest — the former "vm-build"
// host-builder is DELETED; candy/plugin-vm/vm_build_resolve.go resolves it plugin-side):
// everything the plugin needs to run the disk-build engine without importing the loader. VmJSON is
// the resolved+validated kind:vm entity (the #Vm-shaped value resolveVmViaPlugin
// already produces — opaque bytes of the same #Vm-shaped payload convention) so the
// plugin decodes it into its own spec.Vm rather
// than re-parsing uf.VM[entity] itself (which needs LoadUnified, a core Mechanism).
// DistroJSON/BuilderJSON carry the matched *DistroDef/*BuilderDef (bootstrap source
// only) — hand-written runtime types with no CUE def, so they ride as opaque RawBody
// too (the established idiom this file documents at the top). Engine/Rootful are the
// resolved runtime settings (ResolveRuntime) the engine needs to pick `podman` vs
// `sudo podman`. BootcImageRef/BuilderImageRef are PRE-RESOLVED (and, for the builder
// image, pre-built via `charly box build`) — both need the local podman-storage +
// project-config lookup a plugin cannot do (resolveBootcImageRef / ensureBuilderImageBuilt
// stay host-side). OutputDir/VmStateDir are the resolved per-entity paths (vmshared.VmDiskDir
// is ALREADY plugin-importable, but the host still resolves+creates VmStateDir since it also
// reads the existing ledger state below). ExistingState is the entity's persisted
// VmDeployState (#VmDeployState already has a CUE def — a typed embed, not opaque) so the
// plugin reuses the same instance-id / regenerates the seed ISO idempotently.
#VmBuildReply: {
	source_kind!:       string          @go(SourceKind) // cloud_image | bootc | bootstrap
	vm_json!:           bytes           @go(VmJSON, type=RawBody)
	distro_json?:       bytes           @go(DistroJSON, type=RawBody)
	builder_json?:      bytes           @go(BuilderJSON, type=RawBody)
	engine?:            string          @go(Engine)
	rootful?:           bool            @go(Rootful)
	bootc_image_ref?:   string          @go(BootcImageRef)
	builder_image_ref?: string          @go(BuilderImageRef)
	output_dir!:        string          @go(OutputDir)
	vm_state_dir!:      string          @go(VmStateDir)
	existing_state?:    #VmDeployState  @go(ExistingState, type=*VmDeployState)
	force?:             bool            @go(Force)
}

// #DeployPluginsConnectRequest/#DeployPluginsConnectReply — the K1-LOADER RELOCATION witness (Unit
// D). candy/plugin-fleet now DRIVES loaderkit.LoadUnified ITSELF, plugin-side, over the
// reverse-channel LoaderExecutor (execLoaderExecutor → the "loader-*" host legs), to resolve the
// `charly fleet add` deploy tree — the host no longer runs a host-side merged-tree read for the walk. This seam
// is the ONE host-only PREAMBLE the plugin still needs: connect the deployment's out-of-tree plugin
// candies (loadDeployPlugins — registry-coupled, a core Mechanism) BEFORE ResolveTarget can route to
// an external substrate, and return the resolved project dir (host os.Getwd — the SAME dir
// the host loader uses) the plugin passes to loaderkit.LoadUnified. The plugin reads root-venue-ssh
// itself from the tree's stamped node.Descent (loaderkit.LoadUnified stamps it), so no host trait
// call — proving plugin-fleet → loaderkit.LoadUnified end-to-end.
#DeployPluginsConnectRequest: {
	path!:      string @go(Path) // the target dotted path (Run's targetPath == c.Name)
	add_candy?: [...string] @go(AddCandy) // CLI --add-candy, threaded into loadDeployPlugins's scan
}
#DeployPluginsConnectReply: {
	dir!: string @go(Dir) // the resolved project dir (host os.Getwd) the plugin passes to loaderkit.LoadUnified
}

// #ConstructStepRequest/#ConstructStepReply — the "construct-step" HostBuild seam (K5-A item 1,
// compile-seam ctx-threading): the ONE genuinely host-only piece of the former compileActOp —
// resolving a `run:` act op's `plugin:` word against the PROVIDER REGISTRY (a clause-M kernel
// mechanism, never reachable from a plugin directly) to decide whether it lowers into a typed
// InstallStep (TypedStepProvider.ConstructStep, an in-proc-only Go method today — no change),
// an ExternalPluginStep (an out-of-process executorInvoker verb), an ExternalStep (a class:step
// provider's declared StepContract), or the generic OpStep fallthrough. Everything ELSE
// compileActOp used to read off `layer`/`img` is ALREADY portable sdk/deploykit-side (CandyModel,
// *buildkit.ResolvedBox, deploykit.ResolveUserSpec) and never needs to cross this wire — the
// caller (deploykit.BuildDeployPlan, now ctx/exec-threaded) resolves those PORTABLE pieces
// itself and sends only the reduced scalars the registry-consult logic actually reads. The reply
// carries the constructed step as an OPAQUE #InstallStepView (StepToView/StepFromView, R3 — the
// SAME wire-view round-trip every other step-carrying seam uses) so the caller re-materializes a
// real deploykit.InstallStep without a second per-kind decode path. No new #Op selector is
// needed: the registry-consult decision is HostBuild-KIND-dispatched (string, like every other
// seam in this file), not a provider-targeted Invoke — compileActOp's logic runs UNCHANGED
// host-side inside the seam handler.
#ConstructStepRequest: {
	op!:               #Op    @go(Op)
	candy_name!:        string @go(CandyName)
	candy_source_dir?:  string @go(CandySourceDir)
	candy_vars?: {[string]: string} @go(CandyVars)
	resolved_user?:     string @go(ResolvedUser)
	pkg_format?:        string @go(PkgFormat)
	distro_tags?: [...string] @go(DistroTags)
}
#ConstructStepReply: {
	step?: #InstallStepView @go(Step, type=*InstallStepView)
}

// #RenderServiceRequest/#RenderServiceReply DIED (#55 W3 B4): the "render-service" HostBuild
// seam they carried is GONE — their "TWO registry consults a plugin cannot do itself" framing
// was stale (kind:init's OpResolve and verb:egress's OpValidate are BOTH compiled-in, reachable
// via direct InvokeProvider peer dispatch from any connected plugin). CompileServiceSteps'
// render-service call now reuses sdk/deploykit's own render_generator_from_project.go
// renderSeamCaller.renderService (already doing this exact two-InvokeProvider sequence for the
// build-time init-assembly path) instead of a host round trip.

// #DeployMembersRequest/#DeployMembersReply DIED (#55 W3 A4): the "deploy-members-up"/
// "deploy-members-down" HostBuild seam is gone — candy/plugin-fleet calls
// sdk/deploykit.BringUpMembers/TearDownMembers directly now (spec/proc + spec/hostenv +
// spec/exec fabric, no host-private state, no registry coupling).

// #DeployDelResolveRequest/#DeployDelResolveReply — resolve a `charly fleet del` target's
// FleetNode (resolveDelNode: literal "host" / "vm:"-prefix legacy forms / a charly.yml tree
// entry / a ref-based pod-artifact probe) — needs LoadUnified + the on-disk artifact probe, so
// it stays host-side; the plugin's `charly fleet del` calls this FIRST.
#DeployDelResolveRequest: {
	name!: string @go(Name)
	// tree_json is the merged project+operator deploy tree the command:fleet plugin already
	// resolved PLUGIN-SIDE (resolveTreeViaLoader, which also connects the deployment's plugins) —
	// threaded as DATA so the host resolveDelNode consumes it instead of re-loading the tree
	// host-side (#55 Cone A Unit 3a). Marshalled map[string]spec.Deploy; an absent/empty tree
	// falls through to resolveDelNode's non-tree fallbacks (vm-prefix / pod-artifact probe), exactly
	// as a nil host-tree-read result did before.
	tree_json?: bytes @go(TreeJSON, type=RawBody)
}
#DeployDelResolveReply: {
	node?: #Deploy @go(Node, type=*Deploy)
	kind?: string  @go(Kind)
}

// #DeployNodeDelDispatchRequest/#DeployNodeDelDispatchReply — the `charly fleet del` terminal
// step: ResolveTarget + target.Del, honoring the teardown gates (the live ReverseRunner is still
// never carried on the wire — a programmatic teardown needing a specific runner is resolved
// host-side during dispatch).
#DeployNodeDelDispatchRequest: {
	name!:              string @go(Name)
	node?:              #Deploy @go(Node, type=*Deploy)
	assume_yes?:        bool   @go(AssumeYes)
	keep_repo_changes?: bool   @go(KeepRepoChanges)
	keep_services?:     bool   @go(KeepServices)
	keep_image?:        bool   @go(KeepImage)
	dry_run?:           bool   @go(DryRun)
}
#DeployNodeDelDispatchReply: {}

// #DeployResolveTargetAddRequest/#DeployResolveTargetAddReply — the K4-C SHAPE-2 per-node
// terminal step: ResolveTarget + DeployContext + target.Add for ONE tree position, reached once
// per node from the plugin's own walk. UNLIKE the retired #DeployNodeDispatchRequest (which had
// the host RE-COMPILE the plans via an in-proc OpCompile round-trip — the plugin→host→plugin
// double-bounce), the plugin now COMPILES the InstallPlans IN-PROC (walk.go dispatchOne →
// compileNodePlans → compilePlansForRequest, no OpCompile hop) and ships the ALREADY-COMPILED
// plans (with deployID + AddCandies stamped plugin-side, round-tripped through InstallPlanView) as
// plans_json. The host half does ONLY the genuine floor-M residue a plugin cannot: reconstruct
// the ancestor executor chain (deriveChildExecutorForPath — registry-coupled), loadConfigForDeploy
// (LoadUnified), ResolveTarget + DeployContext + utgt.Add.
//
// ancestor_paths/ancestor_nodes let the host reconstruct the SAME parentExec chain the OLD in-core
// walk built (deriveChildExecutorForPath is pure Go over spec/kit types, re-run HOST-side) — a
// live DeployExecutor never crosses the wire. target is the plugin-classified substrate word (a
// pure ClassifyNodeTarget of node+path), carried so the host synthesizes a Target-only node when
// node is nil (a ref-based deploy with no charly.yml entry). The gate flags are the FINAL resolved
// EmitOpts values (node.InstallOpts already applied over the CLI flags plugin-side); dry_run never
// reaches this seam — a dry-run prints the compiled plans plugin-side and returns without dispatch.
#DeployResolveTargetAddRequest: {
	path!:        string @go(Path)
	deploy_name!: string @go(DeployName)
	node?:        #Deploy @go(Node, type=*Deploy)
	target!:      string @go(Target)
	dir!:         string @go(Dir)
	// plans_json is the marshalled []spec.InstallPlanView (deployID + AddCandies already stamped
	// plugin-side); the host re-materializes []*spec.InstallPlan via deploykit.PlanFromView.
	plans_json!: bytes @go(PlansJSON, type=RawBody)
	ancestor_paths?: [...string] @go(AncestorPaths)
	ancestor_nodes?: [...#Deploy] @go(AncestorNodes)
	node_only?:          bool   @go(NodeOnly)
	pull?:               bool   @go(Pull)
	verify?:             bool   @go(Verify)
	with_services?:      bool   @go(WithServices)
	allow_repo_changes?: bool   @go(AllowRepoChanges)
	allow_root_tasks?:   bool   @go(AllowRootTasks)
	skip_incompatible?:  bool   @go(SkipIncompatible)
	assume_yes?:         bool   @go(AssumeYes)
	builder_image?:      string @go(BuilderImage)
	// dev_local_pkg marks a DISPOSABLE CHECK BED's deploy, the deploy-side twin of `charly box
	// build --dev-local-pkg`. On a bed, a localpkg candy whose package source cannot be found is
	// a HARD FAILURE rather than the benign skip a normal deploy takes — a bed exists to prove
	// the in-development package builds and installs, so silently installing nothing (or an
	// older release) makes the bed assert something it never tested.
	dev_local_pkg?:      bool   @go(DevLocalPkg)
}
#DeployResolveTargetAddReply: {}

// (The `charly fleet import`/`reset` deploy-state WRITE host seam was DELETED in #55 K4 —
// command:fleet now performs the SAVE plugin-side via deploykit.SaveFleetConfig with its own
// loader-backed reader + loader-threaded Primaries, so no host seam remains for it.)

// (#AndroidEntityResolution — the kind="android" one-off wrapper the former
// #DeployEntityResolveReply.entity field carried — was DELETED in W0, ahead of the whole
// "deploy-entity-resolve" seam's own deletion in K-wave W3a A3-phase-2: it was the last per-kind
// asymmetry forcing a Go branch in the host handler. The google-play-credential thread that once
// justified it moved to a direct peer InvokeProvider(verb:credential) call (deploy-cone cutover 1)
// before the seam itself died.)

// #EphemeralRegisterRequest/#EphemeralRegisterReply — the host→command:fleet OpEphemeralRegister
// leg (FINAL/K5 unit 6a): ephemeral_lifecycle.go's cross-substrate ephemeral-instance registration
// (systemd TTL transient timer + parent-detection + charly.yml persistence) moved to
// candy/plugin-fleet, the substrate-neutral deploy-lifecycle owner (vm/pod/kubernetes all register
// through it via deploy_add_shared.go's registerEphemeralIfMarked, which STAYS host-side —
// candidate-floor sibling of fleet_add_cmd.go — and Invokes this as the FIRST action of every
// Add). Registration failure is best-effort (logged plugin-side, never fatal to the deploy) —
// the reply is empty; the host discards the returned handle (the prior in-core contract already
// did, registerEphemeralIfMarked only checked the error).
#EphemeralRegisterRequest: {
	name!: string  @go(Name)
	node!: #Deploy @go(Node, type=*Deploy)
}
#EphemeralRegisterReply: {}

// #DeployEntityResolveRequest/#DeployEntityResolveReply DELETED (K-wave W3a A3-phase-2): the
// "deploy-entity-resolve" HostBuild seam died — every kind:<word> caller self-loads the project
// plugin-side now (sdk/loaderkit.LoadUnifiedViaExecutor, unblocked by W1, plus the new
// Resolve{Kubernetes,Vm,Android}EntityViaExecutor helpers), and every deploy-tree node lookup collapsed
// to a direct in-memory map lookup on the SAME tree the caller already resolves via
// loaderkit.ResolveMergedTreeViaExecutor (the request's own tree_json field was already
// documented dead weight — "the tree already threaded via req.TreeJSON, could inline at all
// callers with zero host coupling"). See charly/host_build_deploy_entity_resolve.go's deletion
// and candy/{plugin-adb,plugin-fleet,plugin-deploy-vm,plugin-kube,plugin-vm} for the per-caller
// migration.

// #EphemeralTeardownRequest/#EphemeralTeardownReply — the OpEphemeralTeardown leg any
// substrate's own post-teardown handling can Invoke directly (recursive nested-child teardown,
// TTL timer cancel, snapshot/parent refcount decrement, charly.yml cleanup lives in
// candy/plugin-fleet's teardownEphemeral). Today's one caller is candy/plugin-deploy-vm/
// lifecycle.go's vmPostTeardown (F6 vm-lifecycle move, coneB-vmlifecycle — formerly a host-side
// pre-dispatch hook, charly/vm_lifecycle_preresolve.go's vmLifecyclePostTeardown, dispatched
// in-proc via the now-deleted charly/ephemeral_dispatch.go's TeardownEphemeralLifecycle).
#EphemeralTeardownRequest: {
	name!: string  @go(Name)
	node!: #Deploy @go(Node, type=*Deploy)
}
#EphemeralTeardownReply: {}

// #KubernetesGenerateKustomizeRequest/#KubernetesGenerateKustomizeReply — the request/reply shape
// candy/plugin-kube's materializeKustomize (materialize.go) takes/returns. The former
// Kustomize-generate HostBuild seam this type pair used to cross (FINAL/K5 unit 6a) is
// RETIRED (K5-A item 6): the egress-validated Kustomize GENERATION now runs ENTIRELY
// plugin-side — verb:k8sgen + verb:egress reached peer-to-peer via InvokeProvider, disk I/O done
// directly by the plugin, no host round trip left — so this pair now travels as a plain Go
// function signature (materializeKustomize's params/return), not a wire envelope. Both callers
// (candy/plugin-kube/preresolve.go's deploy:kubernetes preresolve, which self-loads the cluster
// template + image ref/capabilities itself now too — K-wave W3a A3-phase-2 — and
// candy/plugin-fleet/deploy_from_box.go's source-less from-box path) construct it directly.
// Cluster/Capabilities ride opaque (the established RawBody idiom this file uses throughout for
// hand-written host-side types with no CUE def — e.g. CapsJSON/ClusterJSON in this very def below).
#KubernetesGenerateKustomizeRequest: {
	name!:       string @go(Name)
	image_ref!:  string @go(ImageRef)
	node!:       #Deploy @go(Node, type=*Deploy)
	caps!:       bytes  @go(CapsJSON, type=RawBody)    // opaque *Capabilities (spec.BoxMetadata)
	cluster!:    bytes  @go(ClusterJSON, type=RawBody) // opaque *ResolvedKubernetes
	output_dir?: string @go(OutputDir)
}
#KubernetesGenerateKustomizeReply: {
	overlay_path!: string @go(OverlayPath)
	tree_root!:    string @go(TreeRoot)
}

// #PodConfigWriteRequest carries the POD config-WRITE (P11). Under Ruling C the config-WRITE
// (the quadlet/.pod/sidecar/tunnel file generation) moved to the deploy:pod plugin, while the
// RESOLVE + host side-effects (secret provisioning, saveDeployState, enc-mount, data-seed,
// systemctl) stay in the HOST `charly config` command (Q1=(a)). So this is HOST→PLUGIN: for a
// pod deploy, `charly config` resolves the full QuadletConfig + computes the exact target
// PATHS (the core filename helpers, unchanged) and PUSHES them to the plugin's config-write Op,
// which generates the file contents (deploykit.GenerateQuadlet + the pod/sidecar/tunnel
// generators) and os.WriteFiles them — byte-identical to the former core write phase (same
// paths, same content, same modes: .container/.pod/sidecar 0600, tunnel .service 0644).
//
// PodConfigJSON is the resolved deploykit.QuadletConfig — a hand-written runtime type with no
// CUE def, so it travels as an opaque RawBody envelope (the VmJSON pattern; no new CUE wire
// struct). An optional path field being SET is the host's signal to write that file kind
// (pod_path/sidecar_paths present ⇒ sidecars configured; tunnel_path present ⇒ cloudflare
// tunnel) — the host owns the write conditionals, the plugin writes what it is told.
#PodConfigWriteRequest: {
	pod_config_json!:      bytes             @go(PodConfigJSON, type=RawBody) // resolved deploykit.QuadletConfig
	container_path!:       string            @go(ContainerPath)              // full path for the .container quadlet
	pod_path?:             string            @go(PodPath)                    // full path for the .pod (set iff sidecars present)
	sidecar_paths?: {[string]: string}       @go(SidecarPaths)               // sidecar name → full .container path
	tunnel_path?:          string            @go(TunnelPath)                 // full path for the cloudflare tunnel .service
	cloudflared_cfg_path?: string            @go(CloudflaredCfgPath)         // cloudflared config path for GenerateTunnelUnit's ExecStart
}

// #PodConfigWriteReply returns the paths the plugin actually wrote (deterministic; the host
// already knows them — used for the byte-parity assertion + teardown provenance).
#PodConfigWriteReply: {
	written_paths?: [...string] @go(WrittenPaths)
}

// #PodLifecyclePlan is the pod-lifecycle carrier (the K4 deep-body move). Formerly host-resolved
// and threaded on OpStart/OpStop; candy/plugin-deploy-pod now SELF-RESOLVES it (resolve.go's
// resolveStartQuadlet/resolveStopPlan, resolve_direct.go's resolveStartDirect) from the deploy key +
// the raw CLI opts (#PodStartOpts/#PodStopOpts), reaching the host only for genuinely host-only
// mechanisms (the deploy-overlay HostBuild seam, verb:credential, verb:enc, verb:tunnel) — this type
// is now built AND consumed entirely within the plugin process. It EXECUTES it — running the
// container start/stop over the served host executor and composing enc + tunnel via
// InvokeProvider(verb:enc/verb:tunnel), so the former podCli("start"/"stop"/…) `charly`-reentries
// are DELETED (bodies, not shells). The pre-built enc
// verb input (spec.EncExecInput — a hand-written wire type with no CUE def) rides as an opaque
// RawBody envelope (empty ⇒ that leg is skipped, the common plain-pod case) with its Method set
// per-op host-side; tunnel references the CUE-def'd #TunnelConfig directly and the plugin infers
// start-vs-stop from the op. The ARBITER claim is NOT threaded here — its CHARLY_PREEMPT_LEASE
// machinery is host-PROCESS state a placement-agnostic plugin cannot own, so the host proxy BRACKETS
// the plugin op (acquire before OpStart; release after OpStop + on the failure path).
// #PodExecReply is the reply from the pod plugin's OpShell CAPTURED-exec leg (the K4 `charly service`
// move — an in-container init-mgmt exec, non-interactive). The plugin RunCaptures the argv over the
// served executor and returns the combined Output + the exact ExitCode; the host reprints Output
// (placement-agnostic: an out-of-process plugin's stdout is NOT charly's) and propagates a non-zero
// ExitCode as *sdk.ExitCodeError so `charly service` preserves the container command's exit code
// exactly (the passthrough→capture semantics change the ruling requires be exit-code-faithful).
#PodExecReply: {
	output?:    string @go(Output)
	exit_code?: int    @go(ExitCode,type=int)
}

#PodLifecyclePlan: {
	mode!:           "quadlet" | "direct" @go(Mode)           // runQuadlet (systemctl) vs runDirect (podman run)
	svc_name?:       string               @go(SvcName)        // serviceNameInstance — quadlet unit
	container_name!: string               @go(ContainerName)  // containerNameInstance — engine target
	run_argv?: [...string] @go(RunArgv)                        // buildStartArgs output — direct mode `podman run -d`
	direct_deploy?:  bool                 @go(DirectDeploy)    // IsDirectDeploy — quadlet-absent `podman start` fallback
	engine_bin!:     string               @go(EngineBin)      // EngineBinary(resolved engine)
	unmount?:        bool                 @go(Unmount)        // `charly stop --unmount` — enc FUSE teardown
	enc?:     bytes @go(Enc, type=RawBody) // pre-built spec.EncExecInput (Method ensure@start / unmount@stop)
	tunnel?:  #TunnelConfig @go(Tunnel,optional=nillable) // resolved tunnel config (nil ⇒ no tunnel) — driven via podTunnelOp(ctx,exec,"start",...)@start / podTunnelOp(ctx,exec,"stop",...)@stop, both verb:tunnel over InvokeProvider
}

// #PodStartOpts carries `charly start`'s direct-mode CLI extras (K4 inversion, quadlet-mode
// first): the plugin now SELF-RESOLVES the #PodLifecyclePlan (over the deploy-overlay HostBuild
// seam + the already-portable sdk resolvers) using these opts + the deploy key already on
// lifecycleParams.Name — replacing the former host-side resolvePodStartPlan. The quadlet path
// ignores every field (mirrors the pre-inversion contract — CLI extras apply only to direct mode).
#PodStartOpts: {
	env?:            [...string] @go(Env)
	env_file?:       string      @go(EnvFile)
	port?:           [...string] @go(Port)
	volume_flag?:    [...string] @go(VolumeFlag)
	bind?:           [...string] @go(Bind)
	no_auto_detect?: bool        @go(NoAutoDetect)
}

// #PodStopOpts carries `charly stop --unmount` (K4 inversion): the plugin self-resolves the STOP
// #PodLifecyclePlan using this + the deploy key, replacing the former host-side resolvePodStopPlan.
#PodStopOpts: {
	unmount?: bool @go(Unmount)
}

// #PodLiveStdioPlan is the F12 LIVE-STDIO carrier — ONE carrier for shell + cmd + logs (identical
// shape, R3; the op + the executor method distinguish them). Formerly host-resolved and threaded on
// OpAttach/OpLogs op.Params; candy/plugin-deploy-pod now SELF-RESOLVES it (resolve_f12.go's
// resolveAttachPlan/resolveShellPlan/resolveCmdPlan/resolveLogsPlan) from the deploy key +
// #PodAttachOpts/#PodLogsOpts, so this type is now built AND consumed entirely within the plugin
// process — no wire crossing for the plan itself, only for the opts that drive it. The plugin
// EXECUTES it over the served host executor via exec.RunInteractive (OpAttach — inherited LIVE
// stdin/stdout/stderr; the child `podman exec -it`/`-i` owns the PTY + resize + Ctrl-C) /
// exec.RunStream (OpLogs — inherited LIVE stdout/stderr, no stdin). UNARY: the host reverse-server
// runs IN the charly process, so os.Stdin/os.Stdout = the operator's terminal — stdio NEVER crosses
// the wire, only the session exit code (the hostBuildCli doctrine). This takes the F12 exit for the
// shell/cmd/logs-follow rows of the #57 M-core register: the former inline `charly shell`/`cmd`
// bodies + the podCli("logs") reentry are DELETED (bodies, not shells).
#PodLiveStdioPlan: {
	// resolved venue command:
	//   shell → `podman exec -it charly-<box> bash [-c cmd]` OR the ephemeral `podman run --rm -it … bash`
	//   cmd   → `<engine> exec [-e env] charly-<box>[-<sidecar>] sh -c <command>` (no -t; stdin piped)
	//   logs  → `<engine> logs [-f] [-n N] charly-<box>` OR quadlet `journalctl --user -u <svc> [-f] [-n N]`
	script!: string @go(Script)
}

// #PodShellOpts carries `charly shell`'s per-invocation CLI extras (K4/F12 inversion): the plugin
// self-resolves the #PodLiveStdioPlan using these + the deploy key, replacing the former host-side
// resolvePodShellPlan/buildShellArgs/buildExecArgs. interactive/wrap_pty are HOST-RESOLVED booleans
// (interactive = force_tty || isTerminal(); wrap_pty = force_tty && !isTerminal()) — the plugin is a
// subprocess whose own stdout is not the operator's terminal, so the tty check MUST happen host-side
// at the moment of the real CLI invocation and cross the wire as data, never be re-derived
// plugin-side.
#PodShellOpts: {
	tag?:            string      @go(Tag)
	env_file?:       string      @go(EnvFile)
	env?:            [...string] @go(Env)
	volume_flag?:    [...string] @go(VolumeFlag)
	bind?:           [...string] @go(Bind)
	no_auto_detect?: bool        @go(NoAutoDetect)
	interactive!:    bool        @go(Interactive)
	wrap_pty!:       bool        @go(WrapPTY)
}

// #PodCmdOpts carries `charly cmd`'s per-invocation extra (--sidecar), replacing the former
// host-side resolvePodCmdPlan.
#PodCmdOpts: {
	sidecar?: string @go(Sidecar)
}

// #PodAttachOpts carries the F12 Attach op's parameters (K4/F12 inversion): tty selects the shell
// resolver (interactive `charly shell`) vs the cmd resolver (`charly cmd`'s non-interactive `-i`
// exec) — mirrors the former host-side resolvePodAttachPlan dispatch, now run plugin-side.
#PodAttachOpts: {
	cmd?:      [...string]    @go(Cmd)
	tty!:      bool           @go(Tty)
	shell?:    #PodShellOpts  @go(Shell)
	cmd_opts?: #PodCmdOpts    @go(CmdOpts)
}

// #PodLogsOpts carries `charly logs [-f]`'s parameters (K4/F12 inversion), replacing the former
// host-side resolvePodLogsPlan. Mirrors spec.DeployTargetLogsOpts (Follow/Tail/Sidecar) — the
// plugin-side wire twin for the pod logs payload, distinct from the dispatch-envelope
// DeployTargetLogsOpts (K-wave 2 cone R5: the charly-core LogsOpts type is DELETED).
#PodLogsOpts: {
	follow?:  bool   @go(Follow)
	tail?:    int    @go(Tail,type=int)
	sidecar?: string @go(Sidecar)
}

// #CheckRunRequest is the check-run dispatch envelope (P12). command:check
// (candy/plugin-check) owns the `charly check` CLI + output formatting AND runs every
// mode itself (hostCheckRunCtx's per-mode bodies — the former "check-run" HostBuild
// kind is DELETED, K-wave 2 cone R4): the venue→executor construction + OCI-label plan
// extraction are plugin-side (loaderkit / deploykit), while the plan-walk's verb
// dispatch resolves through the provider registry (in-proc, or the out-of-process
// check-context reverse channel for the live-container verbs). The action
// noun "check-run" is class-generic (F11).
//
// Mode selects the run shape (discriminated union): "box" — a pure-box run against a
// disposable container built from Image (RunModeBox, build-scope steps only, the CheckBoxCmd
// engine); "live" — a full-stack run against a running deployment resolved by Name (the plugin
// classifies vm/pod/local/group via its own venue.go), applying the
// Instance/Section/Filter selectors; "feature-box" / "feature-live" — the ADE acceptance run
// (SkipDeterministicRun) over Image (build scope) or the live deployment Name (deploy scope,
// the plugin-side agent grader wiring, gated by NoAgent/Agent/Timeout), scoped by Tag/Strict.
// Dir is the project dir (empty → the plugin uses its own cwd), matching LoadUnified(dir).
// `format` is deliberately NOT a field — the plugin formats the returned Steps itself.
// run-bed + iterate are NOT seam modes: the plugin drives them over HostBuild("cli").
//
// The REPLY is CUE-sourced: #CheckRunReply / #StepResult / #StepPass (checkresult.cue) —
// generated to spec.CheckRunReply / spec.StepResult / spec.StepPass, byte-identical JSON to
// the former hand-written kit types. sdk/kit aliases each (kit.CheckRunReply = spec.CheckRunReply
// etc., sdk/kit/checkrun_seam.go) so the plugin reuses the kit formatters (FormatStepResults*)
// with byte-parity across every --format. A live `cue exp gengotypes` spike (P12) proved
// kit.CheckResult AS A WHOLE is genuinely inexpressible in CUE — its engine-internal
// `DeadlineExceeded bool json:"-"` field has no gengotypes construct — but confirmed the REST
// of the type (Op/Verb/Status/Message/Elapsed/Attempts/TotalElapsed/CapturedValue) generates
// faithfully. FLOOR-SLIM Unit 4 acted on that finding: #CheckResult (checkresult.cue) is the
// CUE-sourced base (→ spec.CheckResult), and kit.CheckResult is now `struct { spec.CheckResult;
// DeadlineExceeded bool json:"-" }` — an EMBEDDING wrapper, not a hand-duplicated type. The SDD
// sweep in THIS cutover extended CUE-sourcing to the reply envelope too: StepResult /
// CheckRunReply / StepPass are plain carriers (no json:"-"), so gengotypes expresses them
// faithfully — the `optional=nillable` marker emits the Passthrough / Score pointers. The
// exception the wire mandate's spike-proven path authorizes is now narrowed to EXACTLY the one
// kit.CheckResult field that forced it, not the whole type nor the reply envelope.
//
// P12 Wave-2: the "score" mode adds Plan — a substituted, nonce-carrying scoring plan
// candy/plugin-check's pluginRunCheckLive walks (NOT the OCI-baked plan the "live" mode
// extracts; walked plugin-side directly since K1-unblock wave arm 3, no host round-trip). Its
// per-step scoring verdicts ride the kit.CheckRunReply.Score field (a *CheckRunResults, below).
#CheckRunRequest: {
	mode!:     string @go(Mode)
	name?:     string @go(Name)
	image?:    string @go(Image)
	instance?: string @go(Instance)
	dir?:      string @go(Dir)
	section?:  string @go(Section)
	filter?: [...string] @go(Filter)
	tag?:      string @go(Tag)
	strict?:   bool @go(Strict)
	agent?:    string @go(Agent)
	timeout?:  string @go(Timeout)
	no_agent?: bool @go(NoAgent)
	plan?: [...#Step] @go(Plan) // "score" mode: the substituted scoring plan pluginRunCheckLive walks
}

// #CheckRunResults / #StepScore / #ScoreSummary — the AI-harness SCORING result model
// (originally P12 Wave-2; the mode's walk moved plugin-side in K1-unblock wave arm 3).
// pluginRunCheckLive returns a *CheckRunResults (the scored check:/agent-check: verdicts, keyed
// by step id for plateau tracking); it doubles as the `charly check box --format yaml` payload
// the harness scorer parses (ParseCharlyTestOutput). These are plain structs — the gengotypes
// workhorse — CUE-sourced so the "score"-mode reply's Score field and the plugin scorer that
// produces AND consumes it import ONE definition (SDD; no alias). Every
// field mirrors the former hand-written Go tag set: required (!) fields carry no json-omitempty
// (json wire byte-identical for the seam reply); optional (?) fields carry it. The retag pass
// adds ,omitempty to every YAML tag uniformly — inert here since ID/Status are always set and a
// zero Summary block only elides on an empty (0-step) result ParseCharlyTestOutput tolerates.
#CheckRunResults: {
	box?:     string @go(Box)
	mode?:    string @go(Mode) // "box" | "run"
	step?: [...#StepScore] @go(Step)
	summary!: #ScoreSummary @go(Summary)
}

// #StepScore — the scorer's verdict for one check:/agent-check: step, keyed by step id.
#StepScore: {
	id!:             string @go(ID)
	origin?:         string @go(Origin)
	text?:           string @go(Text)
	tag?: [...string] @go(Tag)
	keyword?:        string @go(Keyword)
	verb?:           string @go(Verb)
	status!:         string @go(Status)        // "pass" | "fail" | "skip" | "skipped"
	skipped_reason?: string @go(SkippedReason) // set when status=="skipped": "dep-unmet: <id>"
}

// #ScoreSummary — the pass/fail/skip tally block (the former hand-written TestRunSummary). The
// counts are Go `int` (type=int override — the former hand type; CUE `int` defaults to int64),
// so every existing ++/compare call site compiles unchanged.
#ScoreSummary: {
	total!: int @go(Total, type=int)
	pass!:  int @go(Pass, type=int)
	fail!:  int @go(Fail, type=int)
	skip!:  int @go(Skip, type=int)
}

// #CheckVenueResolveRequest asks plugin-check to CLASSIFY a check target's venue — the kind-decode
// (checkVmTarget/checkLocalTarget branch on the stamped venue trait) that is CHECK CAPABILITY LOGIC,
// not kernel fabric (#118 boundary-law self-test: a floor file switching on a kind is a leaked
// R-item). The host's floor reverse-legs (endpoint / graphics / gRPC exec-attach) call this INSTEAD
// of an in-core classifier duplicate, then re-materialize the returned generic #VenueDescriptor via
// kit.VenueFromDescriptor (single-hop) — or, for a nested target the flat descriptor cannot express,
// rebuild the N-hop chain host-side via the kind-blind deploykit.ResolveDeployChain. plugin-check
// reaches the merged deploy tree via its OWN InvokeProvider("build","project", OpResolve)
// peer-dispatch (the former HostBuild("resolved-project") seam is DELETED), so this seam carries
// only name+instance. Class-generic action noun (F11).
#CheckVenueResolveRequest: {
	name!:     string @go(Name)
	instance?: string @go(Instance)
}

// #CheckVenueResolveReply is the wire-safe projection of the plugin-side CheckVenue: the generic
// #VenueDescriptor (the host re-materializes it into a live DeployExecutor — a live executor never
// crosses the wire) plus the scalar venue facts the host legs consume (kind for the vm-vs-not
// graphics branch + the IsContainer var-resolution gate; engine/name for container port mapping +
// image-label reads; vm_name for the VM graphics leg; nested marks a genuinely multi-hop target the
// single-hop descriptor degrades to zero for, so the host rebuilds its chain via ResolveDeployChain).
#CheckVenueResolveReply: {
	descriptor!: #VenueDescriptor @go(Descriptor)
	kind!:       string @go(Kind) // "container" | "vm" | "host"
	engine?:     string @go(Engine)
	name?:       string @go(Name)
	vm_name?:    string @go(VMName)
	nested?:     bool @go(Nested)
}

// #CheckEndpointResolveRequest/#CheckEndpointResolveReply — the resolution BODY behind the
// fixed CheckContext.ResolveEndpoint reverse-RPC every out-of-process live-container verb
// (cdp/wl/vnc/dbus/mcp) dials back into (#55 W3 B7). The reverse-RPC SERVICE surface itself
// stays core (charly/check_endpoint_resolve.go's hostVerbResolver wraps the core-private
// verb-dispatch registry no out-of-process caller can bypass) — only the RESOLUTION WORK
// relocates, compiled-in-REQUIRED placement class (bed_session.go's precedent, #55 W3
// B2-full): the venue-classify leg it calls was ALREADY plugin-native
// (#CheckVenueResolveRequest above), and the downstream resolution (spec/checkhost's
// EndpointForVenue) has zero core-private dependency of its own. Any ssh -L forward it opens
// is tracked in the plugin's OWN per-process pending-cleanup state, never on the wire (a live
// cleanup closure cannot cross ANY Invoke — compiled-in or not, the wire is JSON bytes only)
// — see #CheckDrainEndpointCleanupsRequest for the close-it-now signal.
#CheckEndpointResolveRequest: {
	box!:      string @go(Box)
	instance?: string @go(Instance)
	mode!:     string @go(Mode)
	port!:     int    @go(Port, type=int)
}
#CheckEndpointResolveReply: {
	addr?: string @go(Addr)
}

// #CheckImageLabelResolveRequest/#CheckImageLabelResolveReply — the resolution BODY behind
// the fixed CheckContext.ResolveImageLabel reverse-RPC (#55 W3 B7), the sibling of
// #CheckEndpointResolveRequest above (same placement class; no live resource to track — a
// raw OCI label read is a pure podman-inspect computation).
#CheckImageLabelResolveRequest: {
	box!:      string @go(Box)
	instance?: string @go(Instance)
	mode!:     string @go(Mode)
	label!:    string @go(Label)
}
#CheckImageLabelResolveReply: {
	value?: string @go(Value)
}

// #CheckDrainEndpointCleanupsRequest signals plugin-check to close every forward its
// #CheckEndpointResolveRequest handler opened since the last drain (LIFO) — the plugin-side
// twin of the former core-side hostVerbResolver.runEndpointCleanups (#55 W3 B7). Carries no
// fields: the plugin's pending-cleanup list IS the state, reset+drained per single-verb
// Invoke — the SAME sequential per-Invoke lifecycle guarantee the former core-side
// h.endpointCleanups = nil / defer h.runEndpointCleanups() bracket relied on, now owned by
// the plugin instead.
#CheckDrainEndpointCleanupsRequest: {}

// #CheckLoadPluginsRequest asks the host to connect the out-of-process plugin candies a check
// plan's verb words reference (K1-unblock wave — the "live" check-run arm). Verb dispatch itself
// crosses the wire generically via InvokeProvider (S1 — command:check's pluginVerbResolver), but
// InvokeProvider only resolves an ALREADY-CONNECTED provider (or a compiled-in one); connecting an
// out-of-process candy is the plugin-loading M-mechanism (the kernel/plugin boundary law's clause
// M — plugin discovery/loading/connect stays core), so this seam is the entry point a plugin calls
// BEFORE dispatching a plan whose verbs may need an out-of-process candy connected. The host runs
// the UNCHANGED core engine (LoadConfig + resolveCheckRunnerContext: ScanAllCandyWithConfigOpts +
// collectReferencedPluginWords + loadProjectPlugins) as a pure SIDE EFFECT on its own
// providerRegistry — every subsequent InvokeProvider call in this same `charly check run`
// invocation then resolves. Class-generic action noun "check-load-plugins" (F11 — never a
// substrate/provider word).
#CheckLoadPluginsRequest: {
	name!: string @go(Name) // the deploy/bed name whose plan drives the reference scan
	dir?:  string @go(Dir)  // project dir (empty -> host cwd), matching LoadUnified(dir)
}

// #CheckLoadPluginsReply is empty on success — connect failures are best-effort WARNINGS on the
// host (mirroring resolveCheckRunnerContext's existing behavior: an unresolvable plugin fails
// loudly later, at actual verb dispatch, never here).
#CheckLoadPluginsReply: {}

// #CheckBedRequest DIED (#55 W3 B2-full): the "check-bed" op-discriminated HostBuild seam it
// carried is gone. The compiled-in plugin-check now self-orchestrates the whole bed session
// itself — flock via spec/lock, the repo-override via spec/proc, the arbiter lease via a direct
// InvokeProvider(verb,"arbiter") call (the vm_arbiter_shim precedent) — exactly the K5 death this
// type's own header already predicted. See candy/plugin-check/bed_session.go.
//
// #CheckBedGpuPrereqRequest/#CheckBedGpuPrereqReply is the ONE narrow seam that SURVIVES: GPU
// host-DETECTION (gpu_allocate.go's bedGPUPrereqMissing, DetectVFIO) is the project's explicitly
// operator-dropped exception (no hardware to verify against; fenced from every K-wave cutover,
// including this one — see gpu_shim.go's own header). Threads just the claimant's resource
// tokens out and the GPU-unsatisfiable verdict back, so the fenced core logic stays completely
// unchanged.
#CheckBedGpuPrereqRequest: {
	tokens?: [...string] @go(Tokens) // spec.DedupeNonEmpty(RequiredExclusive ++ RequiredShared), pre-computed plugin-side
}
#CheckBedGpuPrereqReply: {
	missing?: bool   @go(Missing)
	token?:   string @go(Token)
	vendor?:  string @go(Vendor)
}

// #CheckBedReply is now a plain plugin-internal DESCRIPTOR VALUE TYPE (#55 W3 B2-full), never a
// wire reply — candy/plugin-check/bed_session.go constructs it directly (no HostBuild round-trip;
// the type stays CUE-sourced per SDD, since it is still a useful named shape the plugin builds and
// consumes internally). PrereqSkip set ⇒ the bed is a clean SKIP (exit 3): the plugin writes the
// prereq-skip summary + returns CheckSkippedError, running NO other setup step (not even teardown
// — nothing was acquired on the skip path).
#CheckBedReply: {
	calver?:      string            @go(Calver)                    // logDir calver (.check/<bed>/<calver>)
	log_dir?:     string            @go(LogDir)                    // host-relative; the plugin writes step logs here
	prereq_skip?: #CheckBedPrereqSkip @go(PrereqSkip, optional=nillable)
	// BedDescriptor — the substrate classification + refs the plugin drives from.
	is_vm?:       bool   @go(IsVM)
	is_local?:    bool   @go(IsLocal)
	is_group?:    bool   @go(IsGroup)
	is_external?: bool   @go(IsExternal) // in-place external (fleet-del teardown)
	// node_json is the bed ROOT FleetNode (spec.Deploy) serialized — including its nested
	// Members peer map (each member's full FleetNode, with stamped Descent) — so the plugin
	// bed runner can call deploykit.PersistBedDeployOverrides PLUGIN-SIDE for the bed root AND
	// each member (#55 coneC-dsh β1 — the bed-root + member persist relocate off the host seam;
	// the host-side persistBedDeployOverrides wrapper + its deploykit import shed). The plugin
	// supplies its own loader-threaded marshalNode + reader (deployMarshalNode/deployConfigReader
	// pattern); externalInPlace for the root is IsExternal above, for a member it is derivable from
	// the member's stamped Descent (Venue parent/none → in-place). Empty for a VM root (the host
	// seam's !isVM guard — a VM bed runs no `charly config`, so no root persist).
	node_json?: bytes @go(NodeJSON, type=RawBody)
	image?:       string @go(Image)      // pod bed box ref ("" for vm/local/group)
	has_add_candy?: bool @go(HasAddCandy) // node.AddCandy is non-empty (a pod's add_candy: overlay
	// bed) — bed_run.go skips --tag at the config/start steps for such a bed: the FRESH artifact to
	// verify is the overlay fleet-add just built + persisted (resolved via plugin-deploy-pod's
	// resolveDeployRefLocal resolved_image overlay preference), not the base
	// image's own --tag build ref.
	vm_template?: string @go(VMTemplate) // node.From for a vm bed (the ENTITY — `charly vm build` builds off it)
	bed_domain?:  string @go(BedDomain)  // per-deploy live domain identity (`charly vm create/destroy/start … --domain <this>`, post-P33)
	image_tag?:   string @go(ImageTag)   // per-RUN bed-scoped image tag (<bed-root>-<runCalver>); every `charly box build` + deploy in the run passes it as --tag, so concurrent beds building the SAME fixture image name never collide on the store-global short-name→newest-local-CalVer resolution (#75 — the tag analogue of bed_domain=deploy-name)
	local_ref?:   string @go(LocalRef)   // node.From for a local bed
	vm_domains?: [...string] @go(VMDomains)      // charly-<domain> set locked by setup (per-deploy, post-P33)
	check_live_refs?: [...string] @go(CheckLiveRefs) // bed + nested-child refs
	child_keys?: [...string] @go(ChildKeys)      // sortedNestedKeys(node.Children) — ALL nested children (pod path)
	// local_child_keys is the HOST-ROOTED (kind:local) subset of child_keys, in the same order. A VM
	// root deploys ONLY these host-side (mirroring the core deployNestedLocalChildren): a VM's
	// nested CONTAINER children are deployed in-guest by plugin-deploy-vm's PostApply, so a host-side
	// re-deploy would be wrong. The pod path uses child_keys (all); the vm path uses local_child_keys.
	local_child_keys?: [...string] @go(LocalChildKeys)
	// members carries each sibling member's build coordinates so a GROUP bed's plugin can drive the
	// per-member image build loop (`charly vm build <from>` / `charly box build <image>` + check box)
	// BEFORE the host `members-up` op deploys them (bringUpMembers assumes pre-built images).
	members?: [...#CheckBedMember] @go(Members)
	run_build?:   bool @go(RunBuild)   // check_level ≥ build
	run_runtime?: bool @go(RunRuntime) // check_level ≥ noagent
	run_agent?:   bool @go(RunAgent)   // check_level == agent
}

// #CheckBedMember — one sibling member's build coordinates (the group-bed member build loop).
#CheckBedMember: {
	key!:   string @go(Key)
	is_vm?: bool   @go(IsVM)  // vm member — build its disk via `charly vm build <from>` (bringUpMembers does vm create)
	image?: string @go(Image) // pod member box ref ("" for a vm member)
	from?:  string @go(From)  // vm member kind:vm entity (the build/spec source; entity-scoped, NOT --domain)
}

// #CheckBedPrereqSkip — a bed the host skips for an absent HOST prerequisite (a GPU resource
// whose vendor has no matching card): a clean SKIP (exit 3), not a failure.
#CheckBedPrereqSkip: {
	token!:  string @go(Token)
	vendor!: string @go(Vendor)
	reason!: string @go(Reason)
}

// #DeployCompileRequest is the per-node COMPILE seam (K4-B / K4 unit B): the host asks the
// command:fleet plugin's OpCompile handler to compile, in one of THREE selection SHAPES (a
// discriminated set, not three Ops — R3). The plugin fetches the resolved-project envelope
// itself via InvokeProvider("build","project", OpResolve) peer-dispatch (the former
// HostBuild("resolved-project") seam is DELETED — it does NOT receive the whole project in the
// request), loops deploykit.BuildDeployPlan over the resolved order, and
// returns []InstallPlanView. The host re-materializes []*InstallPlan from the views via
// deploykit.PlanFromView.
//
//   - BOX-VIEW selection (an already-resolved base image, ctx!=nil): box_view + order are
//     POPULATED host-side (the host projects an ALREADY-RESOLVED base image via
//     projectResolvedBox) and the plugin trusts them as sent. Retained for any caller that still
//     resolves the base image host-side; the add_candy-on-pod/kubernetes path itself now uses the
//     ADD-CANDY-ON-BOX shape below.
//   - ADD-CANDY-ON-BOX selection (compileCandyOnBoxSelection, the add_candy-on-pod/kubernetes shape, K4
//     box-half completion): candy_ref (the add_candy overlay ref) AND base_box_ref (the primary
//     pod/kubernetes base image name) are BOTH set — the plugin reads rp.Boxes[base_box_ref] (the SAME
//     ResolvedBoxView the primary BOX-REF shape reads, R3) as the COMPILE CONTEXT, resolves the
//     add_candy's OWN topo order from rp.CandyModels (deploykit.ResolveCandyOrder over
//     {BareRef(candy_ref)}, widened by extra_candy_refs for a remote overlay), prunes
//     container-init-for-systemd, and compiles that order against the base image — replacing the
//     former host-side buildkit.ResolveBox(baseImg) + scanCandiesForRef path.
//   - CANDY selection (compileStandaloneCandySelection, the target:local/vm standalone-candy
//     shape, K4 unit B candy-half): candy_ref is set — the plugin resolves the candy key + topo
//     order itself from its own envelope (deploykit.ResolveCandyOrder over rp.CandyModels,
//     already pure) and builds ITS OWN synthetic box (vmshared.DetectHostDistro/DetectHostGlibc
//     for a host target — already sdk-portable; the kind:vm provider's own OpResolve leg for a
//     vm target, mirroring the kind:local OpResolve reuse in node_resolve.go's
//     lookupLocalTemplate, K4 unit A).
//   - BOX-REF selection (compileBoxSelection, the primary pod/kubernetes image shape, K4 unit B
//     box-half): box_ref is set — the plugin reads rp.Boxes[box_ref] (the SAME ResolvedBoxView
//     hostBuildResolvedProject already computed to BUILD the envelope in the first place — no
//     re-derivation, R3) directly via deploykit.NewSpecResolvedBox, and resolves the candy topo
//     order itself from img.Candy over rp.CandyModels (deploykit.ResolveCandyOrder, same as the
//     CANDY shape).
//
// Exactly one of {box_view, box_ref, candy_ref-alone} is set, OR the ADD-CANDY-ON-BOX pair
// (candy_ref + base_box_ref) together. Neither CANDY nor BOX-REF needs
// LoadUnified or the provider-CONNECT registry (verified live, K4 unit B: candy/plugin-fleet's
// own ALREADY-EXISTING preresolveBuilderContexts, called unconditionally for every OpCompile,
// already S2-lazy-connects any externalized builder the resolved order+img trigger via
// exec.InvokeProvider — an exhaustive repo grep found zero target:local/vm or pod/kubernetes deploy
// anywhere needing a builder plugin outside the calling project's own candy closure, the one
// edge S2's Pass-1 project-scan can't cover), so neither needs a new HostBuild kind.
//
// HostContextJSON is the marshalled spec.HostContext — the 4 host-computed WIRE scalars
// (MachineVenue/Distro/GlibcVersion/BuilderImage) — a hand-written spec type with no CUE def
// (the gengotypes spike cannot express its json:"-" in-process fields), so it rides as an opaque
// RawBody envelope (the VmJSON/PodConfigJSON idiom; the plugin unmarshals into spec.HostContext,
// which sdk/deploykit re-exports as deploykit.HostContext). ONLY the 4 scalars cross: the plugin
// populates the json:"-" BuilderContext (preresolveBuilderContexts over the reverse channel) +
// ActiveInit (off the resolved-project envelope's rp.Init) IN-PROCESS after the decode — never on
// the wire. Tag is the image CalVer pin (for the plan Version field when set). Dir is the project
// dir the plugin threads into its InvokeProvider("build","project") call (empty → plugin cwd).
#DeployCompileRequest: {
	dir!:          string           @go(Dir)
	box_view?:     #ResolvedBoxView @go(BoxView)
	order?:        [...string]      @go(Order)
	host_context!: bytes            @go(HostContextJSON, type=RawBody)
	tag?:          string           @go(Tag)
	// The add_candy:/--add-candy ref(s) (if any) this compile call's own candy set was widened
	// with host-side (scanCandiesForRef's synthetic-augmented scan, for a REMOTE ref) — threaded
	// into the plugin's OWN InvokeProvider("build","project") re-fetch (as its extra_candy_refs) so
	// the envelope's candy map ALSO carries them (RCA'd K1-alpha regression: the two scans were
	// independent, so a remote add-candy resolved host-side never reached the envelope).
	extra_candy_refs?: [...string] @go(ExtraCandyRefs)
	// candy_ref selects the CANDY shape above (K4 unit B): the authored ref string (bare local
	// name OR a `@github…` remote ref) the plugin resolves via BareRef against its own
	// rp.CandyModels/rp.Candies (widened, when remote, by extra_candy_refs carrying the SAME raw
	// ref — mirroring compileStandaloneCandySelection's ExtraCandyRefs: []string{ref.Raw}
	// widening). Absent for the other shapes.
	candy_ref?: string @go(CandyRef)
	// vm_entity selects the CANDY shape's synthetic-box kind, TOLERANTLY (mirrors the OLD host
	// syntheticVmBox call site exactly — never a hard requirement): the plugin tries vm_entity
	// against its own rp.Templates.VM; a HIT resolves it via the kind:vm provider's OpResolve leg
	// for a guest-tuned box; a MISS (including vm_entity=="") falls through to a plain host-adhoc
	// box via vmshared.DetectHostDistro. A non-vm deploy's node.From (e.g. a `local:` node's
	// kind:local template ref) is ALSO threaded into vm_entity upstream (resolveVmEntity returns
	// node.From unconditionally, not only for a real vm cross-ref) — so a miss here is the
	// COMMON case, not an error condition. Ignored for the other shapes.
	vm_entity?: string @go(VmEntity)
	// box_ref selects the BOX-REF shape above (K4 unit B box-half): the box's own name (never a
	// remote ref — compileRefSelection already rejects a remote image ref before this request is
	// built). The plugin's own InvokeProvider("build","project") re-fetch is asked to include_disabled
	// so an explicitly-named `enabled: false` box still resolves (mirrors the OLD host
	// ResolveBox(cfg, ref.Name, …) call, which never checked IsEnabled at all — enabled-filtering
	// is a ResolveAllBox/listing concern, not a by-name-resolve one; zero disabled boxes exist
	// repo-wide today, so this is a zero-cost future-proofing widening, not a live behavior
	// change). Absent for the other shapes.
	box_ref?: string @go(BoxRef)
	// base_box_ref selects the ADD-CANDY-ON-BOX shape (K4 box-half completion) WHEN set alongside
	// candy_ref: the primary pod/kubernetes base image's own name, read from rp.Boxes[base_box_ref] as the
	// COMPILE CONTEXT the candy_ref overlay compiles against — replacing the former host-side
	// buildkit.ResolveBox(baseImg) + scanCandiesForRef. Absent for every other shape (a standalone
	// candy_ref with NO base_box_ref stays the CANDY shape, synthetic-box compiled).
	base_box_ref?: string @go(BaseBoxRef)
}

// #DeployCompileReply is the OpCompile reply: the compiled plans as marshalled
// []spec.InstallPlanView (a hand-written sdk/spec wire type with no CUE def → opaque
// RawBody envelope; the host unmarshals into []spec.InstallPlanView and re-materializes
// []*spec.InstallPlan via deploykit.PlanFromView), plus the base identity (box name) and
// the resolved candy set (the order, for deployID + overlay-candy propagation).
#DeployCompileReply: {
	plans!:     bytes     @go(PlansJSON, type=RawBody)
	base?:      string    @go(Base)
	candy_set?: [...string] @go(CandySet)
}

// #PodLifecycleRequest is the ONE discriminated request every pod-lifecycle HostBuild op
// (start/stop/shell/logs/service/cmd/update/remove) sends over the single "pod-lifecycle"
// HostBuild kind (#55 W3 A10b). Converges the seam on the codebase's own established
// op-discriminated wire idiom — #ArbiterInvokeInput's flat action-multiplexed shape,
// charly/provider.go's own Operation.Params json.RawMessage envelope — which the former
// 8-per-verb #PodXRequest family (one CUE type + one HostBuild kind string per verb, each
// redeclaring box/instance/node) was the last outlier against. op selects which #PodXPayload
// type `payload` unmarshals into (host_build_pod_lifecycle_dispatch.go's hostBuildPodLifecycle
// switch); box/instance/node — common to nearly every op — hoisted OUT of the per-op payloads
// into this shared envelope (R3).
#PodLifecycleRequest: {
	op!:       string @go(Op)
	box!:      string @go(Box)
	instance?: string @go(Instance)
	// node is the per-host deploy overlay entry the command:pod / command:cmd plugin ALREADY
	// resolved plugin-side (loaderkit.ResolveLifecycleDeployNodeViaExecutor, the cycle-free
	// plugin-side overlay read) and threads as DATA — so the host's dispatchLifecycleTarget
	// operates on the passed *spec.Deploy instead of re-reading the per-host config itself (the
	// config-READ is a plugin loading capability, not a host M — #55 K4 seam-completion). Seven of
	// the eight ops carry it (start/stop/shell/logs/service/cmd + update — which resolves the
	// deploy node from the merged tree plugin-side since K-wave 2 cone CONTESTED) and remove
	// needs no node at all (it only releases the arbiter claim) — absent for that one.
	node?: #Deploy @go(Node, type=*Deploy)
	// payload is the op-specific #PodXPayload, JSON-marshalled by the calling command plugin and
	// re-decoded host-side once op is known (mirrors the plugin wire protocol's own
	// Operation.Params json.RawMessage design — there is no parallel envelope-vs-payload type
	// system, R3).
	payload?: bytes @go(Payload, type=RawBody)
}

// #PodLifecycleReply is the "pod-lifecycle" host-builder reply. exit_code is populated only for
// op="cmd" — the container command's own exit code, so `charly cmd`'s process exit propagates it
// (the plugin reconstructs an *sdk.ExitCodeError from it) — it cannot ride the HostBuild ERROR
// return, which stringifies the typed error; it must ride a reply FIELD, exactly as the former
// __cmd/CliReply.ExitCode path did. Every other op's reply is empty; op-specific progress prints
// host-side (the compiled-in plugin's HostBuild runs in charly's own process) and failure signals
// via the error return.
#PodLifecycleReply: {
	exit_code?: int @go(ExitCode,type=int)
}

// #PodStartPayload — see #PodLifecycleRequest's header; op="start". The former StartCmd's
// authored fields (DEPLOY-wave CLI-struct port): the command:pod plugin owns the CLI GRAMMAR but
// cannot drive the LifecycleTarget dispatch (ResolveTarget, the plugin loader — core Mechanisms),
// so `charly start`'s command is THIN — it forwards these flags, and the host runs the existing
// startViaLifecycle orchestration VERBATIM, exactly as `charly fleet add` stayed core behind
// HostBuild("resolve-target-add").
#PodStartPayload: {
	tag?:             string @go(Tag)
	build?:           bool   @go(Build)
	env?: [...string] @go(Env)
	env_file?:        string @go(EnvFile)
	port?: [...string] @go(Port)
	volume_flag?: [...string] @go(VolumeFlag)
	bind?: [...string] @go(Bind)
	no_autodetect?:   bool @go(NoAutoDetect)
}

// #PodStopPayload — see #PodLifecycleRequest's header; op="stop". The former StopCmd's authored
// fields.
#PodStopPayload: {
	unmount?: bool @go(Unmount)
}

// #PodLogsPayload — see #PodLifecycleRequest's header; op="logs". The former LogsCmd's authored
// fields (F12 — the host resolves the journalctl/`<engine> logs` stream command, the owning
// plugin streams it live to the operator's stdio).
#PodLogsPayload: {
	follow?:  bool   @go(Follow)
	sidecar?: string @go(Sidecar)
}

// #PodRemovePayload — see #PodLifecycleRequest's header; op="remove". The former RemoveCmd's
// authored fields — the host orchestration this ONE op still performs is just the
// arbiter-release bracket (host_build_pod_lifecycle_dispatch.go's hostBuildPodRemove); the rest
// of remove's orchestration (quadlet/companion-service teardown, pre_remove hooks, purge,
// deploy-entry cleanup) runs entirely in candy/plugin-pod now.
#PodRemovePayload: {
	purge?:       bool @go(Purge)
	keep_deploy?: bool @go(KeepDeploy)
	env?: [...string] @go(Env)
}

// #PodShellPayload — see #PodLifecycleRequest's header; op="shell". The former ShellCmd's
// authored fields (F12 — the host resolves the venue command, the owning plugin runs it over the
// served venue executor via RunInteractive, stdio host-held).
#PodShellPayload: {
	tag?:             string @go(Tag)
	command?:         string @go(Command)
	build?:           bool   @go(Build)
	tty?:             bool   @go(TTY)
	env?: [...string] @go(Env)
	env_file?:        string @go(EnvFile)
	volume_flag?: [...string] @go(VolumeFlag)
	bind?: [...string] @go(Bind)
	no_autodetect?:   bool @go(NoAutoDetect)
}

// #PodServicePayload — see #PodLifecycleRequest's header; op="service". Carries the FULLY
// plugin-resolved argv for `charly service start/stop/status/restart` (Cutover B unit 2
// completion): the plugin now performs resolveServiceInit/validateServiceName/execInitCommand's
// argv-building itself (all portable — spec.ResolvedInit is already an sdk alias,
// buildkit.RenderTemplate is sdk-portable) and sends the FINAL `<engine> exec <container> <tool>
// <op> [svc]` argv; the host does ONLY the irreducible dispatchLifecycleTarget +
// LifecycleTarget.Shell step.
#PodServicePayload: {
	argv!: [...string] @go(Argv)
}

// #PodCmdPayload — see #PodLifecycleRequest's header; op="cmd". Carries `charly cmd <box>
// <command>`'s per-invocation fields: the host does ONLY the irreducible
// dispatchLifecycleTarget("cmd") + LifecycleTarget.Attach step, mirroring op="shell" exactly —
// the interactive `-i` exec runs over the SAME host-held exec.RunInteractive leg (stdio never
// crosses the wire). The plugin owns the CLI grammar + the completion notification itself.
#PodCmdPayload: {
	command?: string @go(Command)
	sidecar?: string @go(Sidecar)
}

// #PodConfigSetupRequest carries the `charly config [setup]` command flags (the former
// BoxConfigSetupCmd's authored fields, PLUS explicit_ref — from_box_pod.go's
// programmatically-set source-less-deploy field, below). P13-KERNEL direction-flip: the
// deploy:pod plugin's sdk.OpConfigSetup handler receives it VERBATIM as Params. The former
// HostBuild("pod-config-setup") forwarder is DELETED (K-wave 2 cone R3) — candy/plugin-pod's
// ConfigSetupCmd (and plugin-fleet's from_box_pod.go) dispatch the op peer-to-peer via
// InvokeProvider; the plugin RUNS the former runConfig orchestration
// (candy/plugin-deploy-pod/config_setup.go).
#PodConfigSetupRequest: {
	box?:              string @go(Box)
	tag?:              string @go(Tag)
	build?:            bool   @go(Build)
	env?: [...string] @go(Env)
	clean?:            bool   @go(Clean)
	env_file?:         string @go(EnvFile)
	instance?:         string @go(Instance)
	port?: [...string] @go(Port)
	keep_mounted?:     bool   @go(KeepMounted)
	password?:         string @go(Password)
	refresh_secret?: [...string] @go(RefreshSecret)
	volume_flag?: [...string] @go(VolumeFlag)
	bind?: [...string] @go(Bind)
	encrypt?: [...string] @go(Encrypt)
	memory_max?:       string @go(MemoryMax)
	memory_high?:      string @go(MemoryHigh)
	memory_swap_max?:  string @go(MemorySwapMax)
	cpus?:             string @go(Cpus)
	seed?:             bool   @go(Seed)
	force_seed?:       bool   @go(ForceSeed)
	data_from?:        string @go(DataFrom)
	update_all?:       bool   @go(UpdateAll)
	ssh_key?:          string @go(SshKey)
	sidecar?: [...string] @go(Sidecar)
	list_sidecars?:    bool   @go(ListSidecars)
	no_autodetect?:    bool   @go(NoAutoDetect)
	// explicit_ref is set programmatically (never authored) by `charly fleet from-box`'s
	// source-less deploy path (from_box_pod.go) — the P13-KERNEL direction-flip carries
	// it across the wire now that the ORCHESTRATION (formerly reading the kong:"-" Go field
	// directly) moved into the plugin.
	explicit_ref?: string @go(ExplicitRef)
	// host_env_json is the marshalled #HostEnv (CharlyBin/Home/Version) — the SAME R10
	// bed-found bug class #DeployTargetDispatchRequest's own host_env_json field documents
	// (unified_targets.go's hostEnvJSON(): os.Executable() resolves correctly to the charly
	// binary only when called IN CORE — a plugin's os.Executable() resolves to the PLUGIN
	// binary, wrong for an out-of-process placement). Setup's quadlet emission needs the REAL
	// charly binary path for the encrypted-mount ExecStartPre line
	// (deploykit.QuadletConfig.CharlyBin); the bug was dormant here until a deploy actually had
	// an encrypted volume to mount (check-enc-pod's R10 first exercised it once the
	// project-declared-volume fallback started resolving one). The HOST computes it (core's
	// hostEnvJSON(), R3 — one host-identity helper) and threads it as DATA on the OpRun dispatch
	// envelope; candy/plugin-pod forwards it verbatim into this field (the former
	// hostBuildPodConfigSetup forwarder is DELETED, K-wave 2 cone R3).
	host_env_json?: bytes @go(HostEnvJSON, type=RawBody)
}

// #PodConfigSetupReply is the OpConfigSetup handler's reply — empty for the normal setup path,
// mirroring #PodLifecycleReply's empty-for-every-op-but-cmd shape. The `--list-sidecars` leaf
// (r.list_sidecars) fills sidecar_list instead: the plugin's own embedded template library
// (sidecar_embedded.go — the "pod-config-list-sidecars" seam is DELETED, K-wave 2 cone R3), so
// candy/plugin-pod prints it in the charly CLI's own stdio (the out-of-process plugin's stdout
// is go-plugin-discarded).
#PodConfigSetupReply: {
	sidecar_list?: #SidecarList @go(SidecarList)
}

// #SidecarList is the sidecar-template-library introspection list — the `charly config
// --list-sidecars` payload (names + one-line descriptions), returned inside #PodConfigSetupReply.
#SidecarList: {
	names?: [...string] @go(Names)
	descriptions?: {[string]: string} @go(Descriptions)
}

// #PodConfigStatusRequest/#PodConfigMountRequest/#PodConfigUnmountRequest/#PodConfigPasswdRequest
// (+ their empty Reply siblings) — the former HostBuild("pod-config-status"/"-mount"/"-unmount"/
// "-passwd") seam wire forms — are DELETED (wave γ): those four `charly config` leaves now
// dispatch verb:enc/verb:credential DIRECTLY from candy/plugin-pod (enc_cmd.go) via
// InvokeProvider, the same ALREADY-LIVE pattern candy/plugin-deploy-pod/lifecycle.go proves for
// the start/stop path — no host-builder seam left to carry.

// #PodConfigRemoveRequest carries `charly config remove`'s flags (the former
// BoxConfigRemoveCmd's authored fields — distinct from `charly remove`/#PodLifecycleRequest
// op="remove"+#PodRemovePayload, which tears down the whole deploy; this removes only the
// quadlet + disables the service). Dispatched to the deploy:pod plugin's OpConfigRemove handler
// VERBATIM as Params, peer-to-peer from candy/plugin-pod's ConfigRemoveCmd (the former
// HostBuild("pod-config-remove") forwarder is DELETED, K-wave 2 cone R3).
#PodConfigRemoveRequest: {
	box!:      string @go(Box)
	instance?: string @go(Instance)
}

// #PodConfigRemoveReply is the OpConfigRemove handler's reply — empty.
#PodConfigRemoveReply: {}

// P13-KERNEL step-4 direction-flip: BoxConfigSetupCmd/BoxConfigRemoveCmd's BODY (the former
// runConfig orchestration + updateAllDeployedQuadlets + the config_secret_migration.go pair)
// moved OUT of charly core INTO candy/plugin-deploy-pod (Ops sdk.OpConfigSetup/OpConfigRemove on
// the deploy:pod provider's Invoke — dispatched peer-to-peer from candy/plugin-pod's config
// leaves via InvokeProvider; the former host_build_pod_config.go hostBuildPodConfigSetup/Remove
// forwarders are DELETED, K-wave 2 cone R3). The plugin runs the ported logic; the
// detect-devices + list-sidecars HostBuild seams it used to call back are ALSO DELETED (the GPU
// probe is a peer InvokeProvider verb:gpu dispatch and the sidecar embed moves into this
// plugin's own go:embed, K-wave 2 cone R3). The former
// "FINAL/K5 IOU REGISTER" credential-store/enc.go deferral for BoxConfigStatusCmd/MountCmd/
// UnmountCmd/PasswdCmd was CLOSED (wave γ): those four leaves moved wholesale to
// candy/plugin-pod (enc_cmd.go) — they dispatch verb:enc/verb:credential DIRECTLY via
// InvokeProvider (the plugin already holds a real reverse-channel executor), so no
// "pod-config-status/-mount/-unmount/-passwd" seam remains to wrap them.

// #PodConfigEnsureImageRequest/Reply DELETED (K-wave W3a B6): the "pod-config-ensure-image" seam
// died — candy/plugin-deploy-pod drives podman + build:ensure itself now (spec/container was
// already portable; build:ensure reached via the generic InvokeProvider peer-dispatch leg). This
// type's own header claim ("a plugin cannot resolve the local podman image store namespace
// itself") was refuted, not just superseded — see candy/plugin-deploy-pod/image_ensure.go.

// #PodConfigLoadDeployRequest/Reply DELETED (K-wave 2 cone R3): the last "pod-config-*" seam
// using them (the list-sidecars handler's request type) is gone — the loader-coupled
// load/save legs died earlier (#55 coneC-dsh, K4); the plugin reads/writes the per-host overlay
// via loaderkit/deploykit directly.


// #PodConfigSaveFleetRequest / Reply: saveFleetConfigNodeForm(dc) — persists a (plugin-mutated)
// *deploykit.FleetConfig back through the SAME loader-coupled seam.
#PodConfigSaveFleetRequest: {
	config_json!: bytes @go(ConfigJSON, type=RawBody)
}
#PodConfigSaveFleetReply: {}

// #PodConfigMigrateSecretsRequest / Reply: MigratePlaintextEnvSecret(dc, meta, box, instance) —
// the one-time plaintext-env → credential-store migration (file backup + DefaultCredentialStore
// + saveFleetConfigNodeForm, all FINAL/K5-deferred registry-coupled inventory per the ledger).
// config_json carries the ALREADY-LOADED dc (the plugin's own loaded overlay — the former
// #PodConfigLoadDeployRequest is deleted, K-wave 2 cone R3) so the host mutates + re-saves the
// SAME loaded structure the plugin is mid-flow with, never a stale reload.
#PodConfigMigrateSecretsRequest: {
	config_json!: bytes  @go(ConfigJSON, type=RawBody)
	meta_json!:   bytes  @go(MetaJSON, type=RawBody)
	box!:         string @go(Box)
	instance?:    string @go(Instance)
}
#PodConfigMigrateSecretsReply: {
	config_json!: bytes @go(ConfigJSON, type=RawBody) // the (possibly) updated dc
	migrated?:    int   @go(Migrated, type=int)
}

// #PodConfigScrubCliEnvRequest / Reply: scrubSecretCLIEnv(cliEnv, meta) — the credential-store
// Set() pre-scrub for `-e NAME=VAL` flags declared secret_accepts/secret_requires.
#PodConfigScrubCliEnvRequest: {
	cli_env?:   [...string] @go(CliEnv)
	meta_json!: bytes       @go(MetaJSON, type=RawBody)
}
#PodConfigScrubCliEnvReply: {
	cleaned?:  [...string] @go(Cleaned)
	imported?: int         @go(Imported, type=int)
}

// #PodConfigDetectDevicesRequest/Reply DELETED (K-wave 2 cone R3): the "pod-config-detect-devices"
// seam is gone — candy/plugin-deploy-pod probes the host peer-to-peer via
// InvokeProvider(verb:gpu) (detect_devices.go, the same dispatch the core DetectHostDevices/
// EnsureCDI shims made); the plugin's own go:embed carries the sidecar template library.


// #PodConfigTunnelResolveRequest / Reply: TunnelConfigFromMetadata(meta) — resolves the tunnel
// config (charly.yml overlay applied) from image labels.
#PodConfigTunnelResolveRequest: {
	meta_json!: bytes @go(MetaJSON, type=RawBody)
}
#PodConfigTunnelResolveReply: {
	tunnel_json?: bytes @go(TunnelJSON, type=RawBody) // marshalled *TunnelConfig; absent ⇒ nil
}

// (The terminal per-deploy persist WRITE seam — its request/reply wire types — was DELETED in
// #55 K4: candy/plugin-fleet AND candy/plugin-deploy-pod now call deploykit.SaveDeployState
// directly, plugin-side, with their own loader-backed reader + loader-threaded Primaries, so no
// host seam carries the SaveDeployStateInput across the wire anymore.)

// #PodConfigCleanDeployEntryRequest / Reply: deploykit.CleanDeployEntry(box, instance,
// marshalDeployNode) — the `charly remove` deploy-entry cleanup (Cutover B unit 2 remove-verb
// completion). Follows the {box!, instance?} → {} host-owns-load+lock+mutate+save shape —
// deliberately NOT the plugin-side deploykit.SaveDeployState/SaveFleetConfig write (fleet
// import/reset + deploy-state persist, #55 K4 — no host seam),
// which persists an ALREADY-LOADED, already-mutated whole FleetConfig with no internal
// load/lock/entry-removal logic — a genuinely different, narrower operation CleanDeployEntry's own
// internal file-lock + entry-removal + provides-cleanup + empty-file-delete logic cannot be
// reduced to.
#PodConfigCleanDeployEntryRequest: {
	box!:      string @go(Box)
	instance?: string @go(Instance)
}
#PodConfigCleanDeployEntryReply: {}

// #PodConfigEncEnsurePlanRequest/Reply and #PodConfigEncUnmountPlanRequest/Reply (the former
// pod lifecycle's resolvePodEncEnsure/resolvePodEncUnmount seam wire forms) are DELETED (wave γ):
// candy/plugin-deploy-pod's start/stop plan resolution now builds its own enc-ensure/enc-unmount
// plan locally (enc_tunnel_resolve.go, via deploykit.EncPlanForConfig) instead of round-tripping
// through core — no seam left to carry.

// #PodConfigContainerTunnelRequest / Reply: reads the RUNNING container's baked image ref
// (containerImage), extracts + merges its metadata, and resolves the tunnel config. Distinct
// from #PodConfigTunnelResolveRequest (which takes an already-resolved MetaJSON) — this seam
// resolves the image/metadata itself from a container name. candy/plugin-deploy-pod's start/stop
// path builds its own tunnel plan locally now (enc_tunnel_resolve.go, wave γ); this seam STAYS
// registered because candy/plugin-pod's `charly remove` teardown path (remove_tunnel.go) is a
// separate, still-live caller.
#PodConfigContainerTunnelRequest: {
	box!:      string @go(Box)
	instance?: string @go(Instance)
}
#PodConfigContainerTunnelReply: {
	tunnel_json?: bytes @go(TunnelJSON, type=RawBody)
}

// #PodConfigBoxEngineRequest / Reply: ResolveBoxEngineForDeploy(box,instance,globalEngine) — reads
// the per-host deploy config's Engine override. A thin wrapper distinct from the
// (now-deleted) load-deploy seam since callers here want only the resolved engine string, not the
// whole FleetConfig.
#PodConfigBoxEngineRequest: {
	box!:           string @go(Box)
	instance?:      string @go(Instance)
	global_engine!: string @go(GlobalEngine)
}
#PodConfigBoxEngineReply: {
	engine!: string @go(Engine)
}

// #PodConfigSSHKeyRequest/Reply DELETED (K-wave W3a B6): the "pod-config-ssh-key" seam died —
// candy/plugin-deploy-pod reads the host SSH-key FS itself now (spec/sshx.ContainerSSHKeyDir /
// ResolveSSHPubKey were already fully portable). This type's own header claim ("kept as a narrow
// host seam rather than adding a crypto dependency to the plugin") is superseded: the plugin
// already carries the golang.org/x/crypto transitive dependency via spec/sshx, so avoiding it was
// never actually achievable — see candy/plugin-deploy-pod/sshkey_resolve.go.

// #PodConfigListSidecarsReply DELETED (K-wave 2 cone R3): the "pod-config-list-sidecars" seam is
// gone — the sidecar template library MOVED INTO candy/plugin-deploy-pod's own go:embed
// (sidecar_embedded.go), so no host seam carries it; the `--list-sidecars` payload rides
// #PodConfigSetupReply.sidecar_list (above) instead.

// sdk.OpConfigSetup / sdk.OpConfigRemove (the two config-BODY Ops the deploy:pod plugin's Invoke
// dispatches for the direction-flip) reuse #PodConfigSetupRequest / #PodConfigRemoveRequest
// VERBATIM as op.Params — no new outer envelope needed; candy/plugin-pod's config leaves dispatch
// them peer-to-peer via InvokeProvider (the former host_build_pod_config.go forwarders are
// deleted, K-wave 2 cone R3).

// #PodUpdatePayload — see #PodLifecycleRequest's header; op="update". Since K-wave 2 cone
// CONTESTED the resolved deploy node rides the #PodLifecycleRequest.node field (the plugin
// resolves it plugin-side from the merged tree); the former tree_json field is DELETED.
#PodUpdatePayload: {
	tag?:        string @go(Tag)
	build?:      bool   @go(Build)
	seed?:       bool   @go(Seed)
	force_seed?: bool   @go(ForceSeed)
	data_from?:  string @go(DataFrom)
}

// #DeployTargetStatus is the live-runtime state for the "status" deploy op — formerly the
// charly-core StatusInfo, now CUE-sourced because it crosses the plugin boundary; the
// UnifiedDeployTarget contract (spec/spec/deploy_target_unified.go) uses this type directly.
#DeployTargetStatus: {
	state?:   string          @go(State)
	healthy?: bool            @go(Healthy)
	details?: {[string]: string} @go(Details)
}

// #DeployTargetDelOpts is the `charly fleet del` opts type — formerly the charly-core DelOpts,
// now CUE-sourced (the UnifiedDeployTarget contract lives in spec/spec/deploy_target_unified.go).
// The three teardown gates (KeepRepoChanges/KeepServices/KeepImage) were folded into DelOpts
// proper in S3b, replacing the pre-S3b type-assertion in host_build_deploy_node_del_dispatch.go;
// the del dispatcher now passes them as plain fields.
#DeployTargetDelOpts: {
	dry_run?:             bool @go(DryRun)
	assume_yes?:          bool @go(AssumeYes)
	keep_ledger?:         bool @go(KeepLedger)
	remove_volumes?:      bool @go(RemoveVolumes)
	keep_repo_changes?:   bool @go(KeepRepoChanges)
	keep_services?:       bool @go(KeepServices)
	keep_image?:          bool @go(KeepImage)
}

// #DeployTargetTestOpts (formerly mirroring charly-core's TestOpts, `charly check live`) is
// DELETED (#55 W3 B3 remainder): it was ALREADY unreferenced wire vocabulary before this cutover
// (its own comment said "former" — never wired to any op) and TestOpts itself is now gone too
// (Test()'s zero-callers precheck, see #VerifyChecksRequest's header).

// (Removed, R10 bed-found bug fix, S3b): a prior discriminated Update-opts shape retired in
// favor of Update's OptsJSON marshaling the SAME #LifecycleOpts (CUE-sourced,
// schema/seam.cue) that Add's does — mirroring the pre-move Update path exactly, which
// built a plain deploykit.EmitOpts from the retired shape's fields and passed it into the SAME
// shared apply() body Add used, rather than a separate wire shape. RebuildImage is NEVER read by
// the apply body (it belongs to Rebuild's own #DeployTargetRebuildOpts) — the divergence the
// retired shape introduced silently dropped it before it could ever matter, but the REAL bug it
// masked was the Add path decoding a wire-incompatible raw EmitOpts (carrying the live
// ParentExec/ParentNode interface fields), which crashed the moment a nested-child deploy
// (ParentExec non-nil) tried to Add — proven on the check-sidecar-pod R10 bed. Full narrative:
// this repo's CHANGELOG/2026.203.0212.md.

// #DeployTargetLogsOpts is the `charly logs` opts type — formerly the charly-core LogsOpts, now
// CUE-sourced (the UnifiedDeployTarget contract lives in spec/spec/deploy_target_unified.go).
#DeployTargetLogsOpts: {
	follow?:  bool   @go(Follow)
	tail?:    int    @go(Tail)
	sidecar?: string @go(Sidecar)
}

// #DeployTargetRebuildOpts is the `charly update` rebuild-path opts type — formerly the
// charly-core RebuildOpts, now CUE-sourced (the UnifiedDeployTarget contract lives in
// spec/spec/deploy_target_unified.go). `tag` pins the image the rebuild re-deploys to
// (the `charly update --tag` flag); empty = newest local CalVer resolved via the
// ai.opencharly.version OCI label.
#DeployTargetRebuildOpts: {
	rebuild_image?: bool   @go(RebuildImage)
	assume_yes?:    bool   @go(AssumeYes)
	dry_run?:       bool   @go(DryRun)
	tag?:           string @go(Tag)
}

// #DeployTargetDispatchRequest (S3b) is the ONE generic host→command:fleet envelope every
// UnifiedDeployTarget/LifecycleTarget method dispatches through, discriminated by `op` (the
// project rulebook's "generic over ad-hoc" — one wire shape, not eleven). Core's thin
// ResolveTarget proxy (unified_targets.go) constructs this per call from data alone — it never
// holds a *grpcProvider or any core-private registry object, so the type is free to live
// entirely on the wire. `word` is the resolved deploy-substrate provider word (e.g. "pod"/"vm"/
// "local"/"kubernetes"/"android") the plugin dispatches the ACTUAL substrate leg to, via its own
// sdk.Executor.InvokeProvider — core never talks to the substrate provider directly once this
// lands. `has_lifecycle` is the ONE piece of substrate metadata core must resolve itself (the
// registered-provider's own `lifecycle` flag lives on the core-private *grpcProvider) — it gates
// whether Start/Stop/Status/Logs/Shell/Attach/Rebuild are even valid for this substrate
// (mirroring the former ErrNotSupportedOnExternal branches) AND whether the Q1 arbiter bracket
// applies to Start/Stop — `has_plan` is that DIFFERENT, narrower boolean (K4-exit, FLOOR-SLIM-
// proper Unit-8): core still computes it (lifecycleStartPlanHooks[word]/lifecycleStopPlanHooks[word]
// presence, pod_lifecycle_dispatch.go, unmoved) and now THREADS it on the wire instead of bracketing
// the dispatch call itself — command:fleet's handleLifecycleSimple owns the bracket call, by
// InvokeProvider("verb","arbiter") peer dispatch (the "arbiter-bracket-*" HostBuild seams are
// DELETED, K-wave 2 cone R2 bank E; the CHARLY_PREEMPT_LEASE os.Setenv/os.Getenv
// nested-subprocess-inheritance property lives in candy/plugin-preempt's invokeArbiter + the
// plugin-fleet bracket, both compiled-in sharing the host process). `node` is the dispatch-merged FleetNode
// (nil for a ref-based deploy with no charly.yml entry) — required when has_plan is true (the claim
// fields live on it). `plans_json` carries []InstallPlanView
// for Add/Update. `opts_json` carries the op-specific opts struct as an opaque envelope (the
// zero-value-safe pattern every #Pod*Opts request already uses) — kept opaque rather than one
// field per opts type so a NEW deploy verb never needs a NEW CUE field, only a new decode
// branch plugin-side.
#DeployTargetDispatchRequest: {
	op!:              "add" | "update" | "del" | "start" | "stop" | "status" | "logs" | "shell" | "attach" | "rebuild"
	name!:            string      @go(Name)
	word!:            string      @go(Word)
	has_lifecycle?:   bool        @go(HasLifecycle)
	has_preresolve?:  bool        @go(HasPreresolve)
	has_plan?:        bool        @go(HasPlan)
	node?:            #Deploy     @go(Node, type=*Deploy)
	dir?:             string      @go(Dir)
	node_only?:       bool        @go(NodeOnly)
	// ledger_root OPTIONALLY overrides the ledger root directory (kit.LedgerPaths.Root) — the
	// pre-S3b externalDeployTarget carried a settable `paths *kit.LedgerPaths` field a TEST could
	// redirect to a temp dir instead of the operator's real ~/.config/opencharly/installed/; this
	// is the wire-safe equivalent (a bare root string, the plugin derives Deploys/Candies/LockFile
	// from it exactly like kit.DefaultLedgerPaths does). Empty (the default) — kit.DefaultLedgerPaths().
	ledger_root?:     string      @go(LedgerRoot)
	plans_json?:      bytes       @go(PlansJSON, type=RawBody)
	opts_json?:       bytes       @go(OptsJSON, type=RawBody)
	checks_json?:     bytes       @go(ChecksJSON, type=RawBody)
	cmd?:             [...string] @go(Cmd)
	tty?:             bool        @go(TTY)
	// venue_json is the ALREADY-MATERIALIZED spec.VenueDescriptor for this deploy. Two distinct
	// producers set it: (a) core, when this dispatch is a NESTED non-lifecycle child (a
	// `local:`/`android:`/`kubernetes:` deploy under a vm/pod, tree position) — Add flattens the
	// live ancestor executor (EmitOpts.ParentExec) into this field via kit.DescriptorFromExecutor
	// BEFORE the very first "add" dispatch, since that live value cannot itself cross the wire
	// (FIX ROUND, S3b follow-up — its absence on "add" was the R10 bed regression: every nested
	// child silently applied on the operator's host instead of the parent venue); (b) core again,
	// carrying FORWARD a prior dispatch's reported venue for the SAME target's lifetime
	// (Update/Del/Start/Stop/Status/Logs/Shell/Attach/Rebuild, after a lifecycle substrate's "add"
	// already ran PrepareVenue once). Either way the plugin re-materializes it via
	// kit.VenueFromDescriptor instead of re-deriving from the node or re-running PrepareVenue.
	// Absent on "add" for a ROOT (non-nested) OR lifecycle (vm/pod) target — those derive their
	// own venue fresh (root: RootExecutorForDeployNode(node); lifecycle: PrepareVenue).
	venue_json?: bytes @go(VenueJSON, type=RawBody)
	// distro_cfg_json is the marshalled *buildkit.DistroConfig ("add"/"update" only) — recordDeploy's
	// FillReverseUninstallCmds needs it to render an aur-builder ReverseOpPackageRemove's
	// UninstallCmd host-side-equivalent, now plugin-side since buildkit.DistroConfig is a plain sdk
	// type with no core-only coupling (unlike the core-only buildEngineContext wrapper it used to
	// travel inside).
	distro_cfg_json?: bytes @go(DistroCfgJSON, type=RawBody)
	// host_env_json is the marshalled spec.HostEnv (CharlyBin/Home/Version) — the R10 bed-found
	// fifth bug in this cluster's move: every lifecycle Op the deleted substrate_lifecycle_grpc.go
	// sent used its OWN hostEnvJSON() helper, computed HOST-side (os.Executable() resolves to the
	// charly binary only when called IN CORE — a plugin's os.Executable() resolves to the PLUGIN
	// binary, wrong for an out-of-process placement even though today's compiled-in placement
	// happened not to crash on it, since a bare `spec.HostEnv{}` zero-value was marshalled instead
	// of ever actually calling it, plugin-side, in the S3b port). Core (unified_targets.go's
	// dispatch, the ONLY place that reliably knows its OWN binary regardless of the substrate's or
	// command:fleet's own placement) now computes it ONCE per dispatch call and threads it here;
	// candy/plugin-fleet forwards it verbatim to every lifecycle Op instead of computing its own.
	host_env_json?: bytes @go(HostEnvJSON, type=RawBody)
}

// #DeployTargetDispatchReply carries whatever the dispatched op produces — Status for "status",
// Output/ExitCode for "shell"/"attach" (the F12 non-zero-exit propagation via
// *sdk.ExitCodeError), Venue for "add"/"update" (the spec.VenueDescriptor PrepareVenue produced or
// re-confirmed, threaded back to core so a substrate WITH a lifecycle hook — pod's overlay
// container, vm's guest — hands core the SAME live executor for --verify and every subsequent
// dispatch on this target, mirroring the pre-S3b `t.exec = exec` reassignment inside apply()),
// nothing for the rest (success is "no error").
#DeployTargetDispatchReply: {
	status?:      #DeployTargetStatus @go(Status)
	output?:      string             @go(Output)
	exit_code?:   int                @go(ExitCode)
	venue_json?:  bytes              @go(VenueJSON, type=RawBody)
	artifact_key?: string            @go(ArtifactKey)
}

// ---------------------------------------------------------------------------
// Substrate LIFECYCLE wire (M4; SDD conversion of the former deploy_wire.go's
// lifecycle section, per the standing operator directive: a hand-written wire
// struct not yet CUE-sourced is conversion-in-progress, never a sanctioned
// exception) — the host↔plugin envelope for the pod/vm deploy lifecycle Ops.
// All ride Provider.Invoke params/env/reply JSON. Plain structs — gengotypes
// generates them faithfully, no disjunction needed.

// #LifecycleOpts is the serializable subset of the host's EmitOpts shipped in
// a lifecycle Op's params. The two LIVE EmitOpts fields (ParentExec,
// ParentNode) cannot cross the []byte wire — they re-attach host-side via the
// reverse channel's live host-build inputs, never serialized.
// LifecycleOptsFromEmit (spec/deploy_methods.go) is the ONE hand-written
// converter — a pure function, not a type, so it stays hand-written.
#LifecycleOpts: {
	dry_run?:                bool   @go(DryRun)
	allow_repo_changes?:     bool   @go(AllowRepoChanges)
	allow_root_tasks?:       bool   @go(AllowRootTasks)
	with_services?:          bool   @go(WithServices)
	assume_yes?:             bool   @go(AssumeYes)
	verify?:                 bool   @go(Verify)
	pull?:                   bool   @go(Pull)
	skip_incompatible?:      bool   @go(SkipIncompatible)
	builder_image_override?: string @go(BuilderImageOverride)
}

// #HostEnv is the generic host identity a lifecycle plugin (running ON the
// host) needs but cannot derive: the host charly binary path and the host
// home.
#HostEnv: {
	charly_bin?: string @go(CharlyBin)
	home?:       string @go(Home)
	// version is the host charly's CalVer (CharlyVersion()) — the
	// delivery-decision authority for EnsureCharlyInGuest.
	version?: string @go(Version)
}

// #LifecyclePrepareInput is the host-resolved DATA a vm substrate's
// OpPrepareVenue needs but cannot derive itself.
#LifecyclePrepareInput: {
	entity!: string @go(Entity) // the kind:vm ENTITY = disk/spec source (node.From-resolved)
	vm?:     #ResolvedVm @go(VM,optional=nillable) // the resolved vm value envelope (uf.VM[entity] via the plugin)
	ssh_user!:        string @go(SSHUser)        // resolveVmSshUser(spec)
	ssh_port!:        int    @go(SSHPort,type=int) // deploykit.ResolveVmSshPort(spec, domainIdentity) — per-deploy auto-alloc + persisted-port idempotency
	alias!:           string @go(Alias)          // VmSshAlias(domainIdentity) = charly-<deploy>
	ssh_key_path!:    string @go(SSHKeyPath)     // <stateDir>/id_ed25519
	known_hosts_path!: string @go(KnownHostsPath) // <stateDir>/known_hosts
	state_dir!:       string @go(StateDir)       // ~/.local/share/charly/vm/charly-<domainIdentity>
	prior_state?: #VmDeployState @go(PriorState,type=*VmDeployState) // the persisted VmDeployState (nil on first apply)
}

// #PrepareVenueReply is the OpPrepareVenue reply. Venue is re-materialized
// host-side into a live DeployExecutor (the live executor never crosses the
// wire); State is an opaque deploy-entry patch the host persists; Notes are
// human-facing lines the host prints.
#PrepareVenueReply: {
	venue!: #VenueDescriptor @go(Venue)
	state?: bytes @go(State,type=RawBody)
	notes?: [...string] @go(Notes)
}

// #PostTeardownReply is the OpPostTeardown reply: the host removes each named
// charly.yml deploy-entry key AFTER the plugin's teardown.
#PostTeardownReply: {
	remove_entries?: [...string] @go(RemoveEntries)
}

// #CliRequest is the "cli" host-builder envelope (M4): a lifecycle plugin
// asks the HOST to run a `charly <argv>` subcommand.
#CliRequest: {
	argv!: [...string] @go(Argv)
	capture?:     bool @go(Capture)
	combined?:    bool @go(Combined)
	best_effort?: bool @go(BestEffort)
}

// #CliReply is the "cli" host-builder reply: captured stdout (Capture=true),
// the exit code, and an error string on a non-zero exit that was not
// BestEffort-swallowed.
#CliReply: {
	stdout?:    string @go(Stdout)
	exit_code?: int    @go(ExitCode,type=int)
	error?:     string @go(Error)
}

// #LoaderWalkRequest is the "loader-walk" host-builder envelope (K1-LOADER RELOCATION,
// Unit B/D): a plugin driving loaderkit.LoadUnified plugin-side asks the HOST to run the
// kind-blind import/discover/namespace walk for a project dir over the ALREADY
// bootstrap-transformed root bytes. RootData is the raw (bootstrap-phase-transformed) YAML,
// carried as base64-over-JSON []byte (NOT RawBody — it is YAML, not JSON). The reply is a
// spec.LoadedProject marshalled directly (no reply envelope needed). Every OTHER loader leg
// carries an existing type verbatim over the []byte wire: loader-bootstrap ([]byte→[]byte),
// loader-threaded (∅→spec.Threaded), loader-materialize (spec.LoadedProject→loaderkit.UnifiedFile).
// The former loader-android-validate / loader-preempt-validate legs dissolved — a plugin-side loader
// now self-serves those validators over InvokeProvider(kind, OpResolve).
#LoaderWalkRequest: {
	dir!:       string @go(Dir)
	root_data?: bytes  @go(RootData)
}

// (The "deploy-candy-secrets" + "deploy-artifacts-retrieve" HostBuild seams — their request/reply
// wire types — were DELETED in #55 K4: command:fleet's add path resolves candy secrets +
// retrieves artifacts PLUGIN-SIDE (candy/plugin-fleet/secrets_artifacts.go) from the candy set it
// already holds in the resolved-project envelope + the shared verb:credential CredentialAccess +
// deploykit.RetrieveCandyArtifacts over a host ShellExecutor, so no host seam carries them.)

// #BoxFetchResolveRequest/#BoxFetchResolveReply — the "box-fetch-resolve" HostBuild seam behind
// candy/plugin-authoring's command:fetch/command:refresh (K3 build-tail tail, coneB-buildremnant):
// the former hidden core `__box-fetch`/`__box-refresh` reentries (charly/box_fetch_reentry.go,
// DELETED) are replaced by ONE generic host-builder wrapping the SAME host-coupled repo resolver
// (ResolveProjectRepo → EnsureRepoDownloaded: CHARLY_REPO_OVERRIDE + the registered refs-backend
// download dispatch + the command:migrate auto-migration) — none of which an sdk-only plugin can
// run itself. refresh=true additionally force-removes the spec's cache entry before resolving
// (the former BoxRefreshCmd body) so a stale cache re-clones.
#BoxFetchResolveRequest: {
	spec!:    string @go(Spec)
	refresh?: bool   @go(Refresh)
}
#BoxFetchResolveReply: {
	path?: string @go(Path)
}
