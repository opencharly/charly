// CUE schema for the `deploy` AND `check` kinds. Both validate ONE
// FleetNode (charly/deploy.go): a `deploy:` map entry, or a `kind: check`
// bed (disposable:true + usually iterate:/plan:). #Deploy is the base node;
// #Check narrows it to the bed invariants. CLOSED. Shared defs REFERENCED, not
// redefined (R3): #Step/#Op/#Security/#InstallOpts/#Duration/#CalVer/
// #EntityRef/#PortPin/#VmSize/#Sidecar/#ShellSpec live in _common.cue / sidecar.cue.

// #DeployTraits is a SUBSTRATE kind's DECLARED deploy behaviour (P9): a substrate plugin
// advertises it per word over Describe (ProvidedCapability.deploy_traits), and kit.StampDescent
// stamps it onto every node's #DescentDescriptor. It is the SINGLE plugin-declared source for
// "how does this substrate behave in the deploy chain", so the kernel consults the traits off
// node.Descent BY TRAIT — never by switching on the substrate kind word (the boundary law).
// Canonical table: pod=container+image_backed+image_context; vm=ssh+machine_venue+exclusive_venue;
// local=shell+machine_venue; kubernetes=shell+image_context+leaf_only; android=parent; zero value =
// external-in-place. Not authored — charly-written state stamped at load.
#DeployTraits: {
	// venue: how commands physically execute in this substrate's venue:
	//   container — podman/docker exec into the container by name (pod).
	//   ssh       — an ssh hop into the guest (vm).
	//   shell     — the substrate's own root executor runs on the host (local; kubernetes host-side).
	//   parent    — reached via the parent's venue, no own executor (android).
	//   none      — external-in-place (zero value / group).
	venue?: ("container" | "ssh" | "shell" | "parent" | "none") @go(Venue, type=string)
	// image_backed: the substrate runs a baked OCI image (pod).
	image_backed?: bool @go(ImageBacked)
	// image_context: the substrate composes over an image build context (pod overlay, kubernetes manifests).
	image_context?: bool @go(ImageContext)
	// machine_venue: the substrate is a full machine with a system init (host/vm/local) — its
	// services render as systemd units, not a container init.
	machine_venue?: bool @go(MachineVenue)
	// exclusive_venue: the substrate holds an exclusive host-resource lease boundary (vm).
	exclusive_venue?: bool @go(ExclusiveVenue)
	// leaf_only: the substrate is a deploy-chain LEAF — it cannot be descended into (kubernetes).
	leaf_only?: bool @go(LeafOnly)
	// bracketed_lifecycle: the substrate's Start/Stop accept direct-mode CLI opts (env/port/
	// volume/bind/no-auto-detect on Start, unmount on Stop) AND need the Q1 resource-arbiter
	// claim bracketed around the dispatch (pod). A substrate that manages its own venue
	// lifecycle + resource claim (vm, via `charly vm start`/`stop`) leaves this false.
	bracketed_lifecycle?: bool @go(BracketedLifecycle)
	// bed_target: the substrate is a valid `kind:check` bed target — pod/vm/local/android run a
	// disposable check bed; kubernetes (leaf_only) does not. validateCheckBeds reads this instead of
	// switching on the substrate word (the boundary-law incomplete-seam gate).
	bed_target?: bool @go(BedTarget)
	// supports_ephemeral: the substrate's Add/Del path wires the ephemeral register/teardown seam
	// (TTL timer + charly.yml persistence) — vm only today; pod/kubernetes reject `ephemeral: true` until
	// their Add/Del wire the same seam. validateEphemeral reads this instead of a substrate switch.
	supports_ephemeral?: bool @go(SupportsEphemeral)
	// supports_from_snapshot: the substrate has a backing chain `from_snapshot:` restores from — vm
	// only (pod/kubernetes have no backing chains). validateEphemeral reads this instead of a word switch.
	supports_from_snapshot?: bool @go(SupportsFromSnapshot)
}

// #DescentDescriptor is the loader-DERIVED venue-hop descriptor (Cutover H + P9).
// candy/plugin-substrate declares its per-word #DeployTraits over Describe, and kit.StampDescent
// stamps them here so the deploy chain descends generically by TRANSPORT and every consult site
// reads the substrate behaviour off the stamped TRAITS — never by switching on the substrate
// kind word. transport/host_rooted are the DERIVED nesting view (from the traits); the closed
// transport set is the kernel's nesting-boundary vocabulary.
#DescentDescriptor: {
	// transport: how the deploy chain descends INTO this node's substrate (DERIVED from the
	// declared venue + leaf_only by kit.DescentFromTraits):
	//   none           — shares the parent venue; no hop (local, android).
	//   container-exec — enter the container by name (pod; podman/docker per engine).
	//   ssh            — an ssh hop into the guest (vm).
	//   reject         — unreachable via the deploy chain (kubernetes → use kubectl).
	transport: "none" | "container-exec" | "ssh" | "reject"
	// host_rooted: the substrate's own ROOT executor runs directly on the host
	// (local), so the check runner uses rootExecutorForDeployNode, not a container chain.
	host_rooted?: bool @go(HostRooted)
	// the plugin-DECLARED substrate traits (P9), stamped in by kit.StampDescent — the consult
	// sites read these instead of switching on the substrate kind word.
	#DeployTraits
}

#Deploy: {
	version?:     #CalVer
	description?: string & !=""

	// target is DERIVED from the node's discriminator kind + cross-ref at load
	// (buildFleetNode/inferFleetTarget) — NOT authored in node-form. Optional
	// here so #Check's arms can pin it; the #DeployValue arm (node.cue) rejects an
	// authored `target:` outright. The former default `*"pod"` is dropped (Go's
	// classifyTarget supplies the empty→pod default). Generated as a plain Go
	// `string` (the loader stamps it; the CUE enum still validates a pinned value).
	target?: ("pod" | "vm" | "kubernetes" | "local" | "android") @go(Target,type=string) // loader-DERIVED (yaml:"-")

	// member_of + inside are loader-DERIVED runtime fields (never authored;
	// rejected by #DeployValue): member_of marks a folded sibling-member entry,
	// inside names the venue a nested resource deploys into. Generated for the Go
	// tree-walker, forbidden in authoring.
	member_of?: string @go(MemberOf)
	inside?:    string @go(Inside)

	// agent_provisioned marks a resource member/child the AI deploys at run time
	// (the iterate-benchmark contract): image-less (no box:), not folded to a
	// top-level entry, exempt from the box-required validators. See deploy.go.
	agent_provisioned?: bool @go(AgentProvisioned)

	// descent is the loader-DERIVED venue-hop descriptor (the descent de-type,
	// Cutover H): the substrate plugin stamps it at OpLoad (via kit.StampDescent) so
	// the deploy chain (AppendHopForFlatPath) descends generically BY TRANSPORT,
	// never by switching on the substrate kind word. charly-written state, never
	// authored (rejected by #DeployValue).
	descent?: #DescentDescriptor @go(Descent,optional=nillable)

	// EDGE-INHERIT cutover B: the substrate kind is the EDGE discriminator (pod:/vm:/
	// kubernetes:/local:/android:/group:), so the deploy carries only NON-kind cross-refs:
	//   from  — inherit a SAME-kind template by name (vm/kubernetes/local/android deploys).
	//   image — the box/OCI artifact a pod/kubernetes/android RUNS (the former `box:`).
	// Per-substrate validity (image⊻from, source⊻from) is enforced in Go
	// (classifyTarget / validateDeploy), not CUE, so a `vm:` node is a VmSpec template
	// (source:) OR a deploy (from:) under ONE arm — the disjunction #Vm|#Deploy.
	from?:  #EntityRef
	image?: string & !=""

	kind?:     "service" | "daemon" | "batch" | "scheduled" | "oneshot"
	replica?:  int & >=0 @go(,type=int)
	restart?:  "always" | "on-failure" | "never"
	schedule?: string & !=""

	tunnel?:     #Tunnel       @go(Tunnel,type=*TunnelYAML)
	dns?:        string & !="" @go(DNS)
	acme_email?: string & !="" @go(AcmeEmail)
	port?: [...#PortPin]
	resolved_port?: [...#PortPin] @go(ResolvedPort)
	// resolved_image: the concrete image ref the pod deploy's add_candy: overlay
	// build produced (`<deploy-key>-overlay:<hash>`), persisted by PrepareVenue so
	// config/start deploy EXACTLY that overlay (carrying the add_candy layers)
	// instead of re-resolving the base image: short-name by a CalVer sort that the
	// overlay alias can lose to the base on a same-minute build. charly-written
	// state (like resolved_port), never authored; empty for a plain pod.
	resolved_image?: string & !="" @go(ResolvedImage)
	env?: {PATH?: _|_, [string]: #StrVal} @go(Env,type=map[string]string)
	env_file?: string & !="" @go(EnvFile)
	network?:  string & !=""
	engine?:   "podman" | "docker"
	security?: #Security @go(Security,optional=nillable)
	secret?: [...#DeploySecret]
	volume?: [...#DeployVolume]
	// volume_project_checked marks that this deploy's PROJECT-declared `volume:` override has
	// already been consulted ONCE (true regardless of whether the project declared one), so
	// resolveDeployVolumes (candy/plugin-deploy-pod) never re-queries the project on a
	// subsequent `charly config`/`charly update` merely because the per-host overlay's Volume
	// field happens to be empty (that emptiness is ALSO produced by an unrelated overlay write —
	// e.g. the port-resolution block — that never touched volumes at all, so key-existence alone
	// cannot distinguish "checked, found nothing" from "not checked yet"). charly-written state
	// (like resolved_port/resolved_image), never authored.
	volume_project_checked?: bool @go(VolumeProjectChecked)
	// The Go type is OPAQUE (map[string]json.RawMessage): CUE still validates each
	// per-deploy sidecar override against #Sidecar, but the kernel stores it as
	// opaque bodies — ALL sidecar business logic lives in candy/plugin-sidecar's
	// OpResolve (the sidecar de-type, Cutover D). The host never reads Sidecar fields.
	sidecar?: {[string]: #Sidecar} @go(Sidecar,type=map[string]RawBody)
	forward_gpg_agent?: bool @go(ForwardGpgAgent,type=*bool)
	forward_ssh_agent?: bool @go(ForwardSshAgent,type=*bool)

	plan?: [...#Step]
	iterate?: #Iterate @go(Iterate,optional=nillable)

	add_candy?: [...(string & !="")] @go(AddCandy)
	install_opts?: #InstallOpts @go(InstallOpts,optional=nillable)

	host?: string & !=""
	user?: string & !=""
	ssh_arg?: [...string] @go(SSHArgs)

	cpus?:      int & >=1 @go(,type=int)
	ram?:       #VmSize
	disk_size?: #VmSize @go(DiskSize)

	deploy?: #KubernetesDeploy @go(Deploy,optional=nillable)

	resources?: #DeployResources @go(Resources,optional=nillable)
	expose?:    #DeployExpose    @go(Expose,optional=nillable)
	storage?: [...#DeployStorage]
	probes?: #DeployProbes @go(Probes,optional=nillable)

	from_snapshot?:    string & !=""  @go(FromSnapshot)
	cloud_init_clean?: bool           @go(CloudInitClean)
	vm_state?:         #VmDeployState @go(VmState,type=*VmDeployState)

	disposable?:  bool @go(,type=*bool)
	lifecycle?:   "scratch" | "dev" | "test" | "qa" | "staging" | "prod" | "custom"
	ephemeral?:   #Ephemeral   @go(Ephemeral,type=*EphemeralLifetime)
	preemptible?: #Preemptible @go(Preemptible,type=*PreemptibleConfig)
	requires_exclusive?: [...(string & !="")] @go(RequiresExclusive)
	requires_shared?: [...(string & !="")] @go(RequiresShared)

	// nested/peer map keys carry no dots (validateDeploymentName). Loader-built
	// runtime tree maps (Children = nested-inside venue; Members = brought-up
	// alongside on the shared network) — gengotypes can't express a pattern-keyed
	// self-referential map, so the Go type is pinned explicitly.
	nested?: {[=~"^[^.]+$"]: #Deploy} @go(Children,type=map[string]*Deploy)
	peer?: {[=~"^[^.]+$"]: #Deploy} @go(Members,type=map[string]*Deploy)
}

// #Check — a kind:check bed. Structurally IDENTICAL to #Deploy (same FleetNode
// Go struct), so it is a plain reference (R3 — no field duplication; stays CLOSED
// because #Deploy is closed). The bed-mode invariants the former `& (A|B|C)`
// disjunction expressed — disposable required + bed-legal target ∈ {pod,vm,local,
// android} for the deterministic/ephemeral modes, the iterate AI-benchmark mode,
// and the ephemeral⇒disposable promotion — are enforced in GO at load time
// (loaderkit.ValidateCheckBeds + the ephemeral validator beside it), which is the
// SINGLE source of truth for the actual fleet-form beds (a node-form check bed
// is a `fleet:` node validated via #DeployValue=#Deploy, so the disjunction was
// only ever applied to the legacy root-shape `check:` collection). Relaxing it to
// the alias removes that divergent parallel spec and lets gengotypes emit a real
// Check struct instead of an empty `struct{}`.
#Check: #Deploy

#Tunnel: (("tailscale" | "cloudflare") | {
		provider: "tailscale" | "cloudflare"
		tunnel?:  string & !=""
		public?:  #PortScope
		private?: #PortScope
}) @go(-) // gengotypes: hand TunnelYAML (spec/union_types.go)

// PortScope (tunnel.go): "all" scalar | a list of container ports | a
// port→hostname map (PortMap). Ports are ints; hostnames strings.
#PortScope: ("all" | [...int] | {[=~"^[0-9]+$"]: string}) @go(-) // gengotypes: hand PortScope

// DeployProbes (deploy.go) — each probe is an inline Op (the check verb vocab).
#DeployProbes: {
	liveness?:  #Op @go(Liveness,optional=nillable)
	readiness?: #Op @go(Readiness,optional=nillable)
	startup?:   #Op @go(Startup,optional=nillable)
}

// VmDeployState (deploy.go) — MACHINE-WRITTEN runtime state, never authored.
// Forward-evolving; the open `...` tail is the justified hatch for a state
// record — which makes gengotypes degrade the whole def to `map[string]any`.
// The hand Go field is a CONCRETE *VmDeployState struct (with its own nested
// state sub-types), so @go(-) suppresses the lossy map type and the faithful
// VmDeployState is hand-written in spec/hand_state_types.go (the field references it
// via @go(VmState,type=*VmDeployState)). Runtime ingress validation still uses
// this open #VmDeployState — @go(-) only affects the generated Go type.
#VmDeployState: {
	instance_id?:                string
	disk_path?:                  string
	seed_iso?:                   string
	ssh_port?:                   int
	ssh_user?:                   string
	backend?:                    "auto" | "qemu" | "libvirt" // "auto" persisted pre-resolution (the vm deploy lifecycle hook)
	cloud_init_rendered_digest?: string
	charly_install_strategy?:    "auto" | "scp" | "url" | "skip"
	// port_forwards persists the auto-allocated host ports for `auto:<guest>`
	// network.port_forwards entries (guest-port → allocated host port), so the
	// allocation is reused across the vm-create → deploy-add sequence — the
	// sibling of ssh_port. Validation-only (the Go type is hand-mirrored, @go(-)).
	port_forwards?: {[string]: int}
	// ephemeral persists the FINAL/K5 unit 6a cross-substrate ephemeral-instance lifecycle
	// state (candy/plugin-fleet/ephemeral.go's RegisterEphemeralLifecycle /
	// persistEphemeralRuntime) — machine-written, so EVERY field is optional to tolerate
	// legacy/partial entries (an ephemeral registered before a field existed, or interrupted
	// mid-write never has a required-field gap to violate). Mirrors spec.EphemeralRuntime
	// (sdk/spec/hand_state_types.go) field-for-field. Validation-only (the Go type is
	// hand-mirrored, @go(-), matching #VmDeployState's own @go(-) at the top level).
	ephemeral?: {
		id?:               string
		parent_vm?:        string
		parent_snapshot?:  string
		parent_ephemeral?: string
		child_refcount?:   int
		timer_unit?:       string
		ttl_deadline?:     string
		status?:           string
		instance_name?:    string
		// deploy_address is the CLI-addressable deploy identity (the dotted tree path for a
		// nested deploy — `charly fleet del <deploy_address>`), DISTINCT from the dc.Fleet
		// map key this entry is stored under (a dot-sanitized "vm:<domain-id>" form — see
		// spec.ValidateDeploymentName + sdk/vmshared.VmDomainIdentity). RCA #2,
		// FINAL/K5 unit 6a.
		deploy_address?: string
	}
	...
} @go(-)

#DeployVolume: {
	name:         string & !=""
	type?:        "volume" | "bind" | "encrypted"
	host?:        string & !=""
	path?:        string & =~"^/"
	data_seeded?: bool          @go(DataSeeded)
	data_source?: string & !="" @go(DataSource)
}
#DeploySecret: {
	name:    string & !=""
	source?: string & !=""
}
#DeployResources: {
	cpu_request?:    string & !="" @go(CPURequest)
	memory_request?: string & !="" @go(MemoryRequest)
}
#DeployExpose: {
	host?: string & !=""
	path?: string & =~"^/"
	tls?:  bool @go(TLS)
	port?: string & !=""
}
#DeployStorage: {
	name:        string & !=""
	size?:       string & !=""
	class_hint?: "fast" | "cheap" | "encrypted" | "default" @go(ClassHint)
	access?:     "single-writer" | "many-readers" | "many-writers"
	path?:       string & =~"^/"
}
#KubernetesDeploy: {
	namespace?: #EntityRef
	workload?:  "Deployment" | "StatefulSet" | "DaemonSet" | "Pod" | "Job" | "CronJob"
	patches?: [...#KubernetesPatch]
	raw?: [...string]
	// helm_charts — chart releases the kustomize/helm venues install alongside the
	// generated workload. The k8sgen emitter translates each entry into a kustomize
	// `helmCharts:` overlay entry (chart→name, release→releaseName, values_files→
	// valuesFile); the vm/pod/local venues install the same releases via
	// `step:helm-release`. Field set confirmed by the Unit 1 schema spike.
	helm_charts?: [...#HelmChart]
}
#HelmChart: {
	repo?:         string
	chart!:        string
	version?:      string
	release!:      string
	namespace?:    string
	values_files?: [...string]
}
#KubernetesPatch: {
	target: {
		kind?:      string & !=""
		name?:      string & !=""
		namespace?: string & !=""
	}
	patch: string & !=""
}

#Ephemeral: (true | {
		ttl?:             #Duration
		keep_on_failure?: bool
		naming_pattern?:  string & !=""
}) @go(-) // gengotypes: hand EphemeralLifetime (spec/union_types.go)

#Preemptible: ([...(string & !="")] | {
	holds?: [...(string & !="")]
		stop?:    "shutdown"
		restore?: "always" | "on-success"
}) @go(-) // gengotypes: hand PreemptibleConfig

// ---------------------------------------------------------------------------
// Deploy IR wire (SDD conversion of the former deploy_wire.go, per the
// standing operator directive: a hand-written wire struct not yet CUE-sourced
// is conversion-in-progress, never a sanctioned exception). Shared between
// charly's core (package main) and the plugin SDK / out-of-tree plugins across
// the go-plugin process boundary on an OpExecute Invoke. Scope (an int enum +
// String() method) and ReverseOpKind (a string enum) are NAMED Go TYPES with
// behavior gengotypes cannot generate — they stay hand-written in
// spec/deploy_consts.go (mirrors the #CheckResult status.cue Status split);
// the CUE fields referencing them (`scope`/`target_scope`/`kind` below) are
// plain int/string, typed via @go(Name,type=Scope) / @go(Name,type=ReverseOpKind).
// Plain structs otherwise — gengotypes generates them faithfully, no
// disjunction needed.

// #ReverseOp is a single teardown action. Serialized into the ledger so
// uninstall can reverse a deploy without re-reading the candy manifest.
#ReverseOp: {
	kind!:  string @go(Kind,type=ReverseOpKind)
	format?: string @go(Format) // package format for package-remove (rpm/deb/pac)
	targets?: [...string] @go(Targets) // package names, file paths, env names, …
	scope?: int    @go(Scope,type=Scope) // system vs user for disambiguation
	extra?: {[string]: string} @go(Extra) // op-specific details (e.g. unit name, layer name, plugin-script body)
	// uninstall_cmd is the rendered host-venue package-removal command for a
	// ReverseOpPackageRemove op, filled at record time from the format's
	// uninstall_template by fillReverseUninstallCmds.
	uninstall_cmd?: string @go(UninstallCmd)
}

// #InstallPlanView is the JSON-roundtrippable wire VIEW of an InstallPlan.
#InstallPlanView: {
	deploy_id?:        string @go(DeployID)
	box?:              string @go(Box)
	version?:          string @go(Version)
	distro?:           string @go(Distro)
	candy?:            string @go(Candy)
	candies_included?: [...string] @go(CandiesIncluded)
	add_candies?: [...string] @go(AddCandies)
	builder_image?: string @go(BuilderImage)
	meta?: {[string]: string} @go(Meta)
	// steps is the serializable per-step IR — the ordered InstallStep sequence
	// the in-core InstallPlan carries, projected onto the wire union below.
	steps?: [...#InstallStepView] @go(Steps)
}

// #CacheMountView is the wire mirror of package main's CacheMountSpec.
#CacheMountView: {
	dst?:     string @go(Dst)
	sharing?: string @go(Sharing)
}

// #ArtifactView is the wire mirror of package main's ArtifactRef.
#ArtifactView: {
	container_path?: string @go(ContainerPath)
	host_path?:      string @go(HostPath)
	chown?:          bool   @go(Chown)
}

// #InstallStepView is the JSON-roundtrippable wire form of ONE InstallStep — a
// SUPERSET struct: each kind populates the subset of fields it carries, all
// optional so the wire stays compact.
#InstallStepView: {
	kind!: string @go(Kind) // the StepKind discriminator ("File","ShellHook","Op",…)

	// Derived ADVISORY fields (Scope()/Venue()/RequiresGate() results).
	scope?: int    @go(Scope,type=Scope)
	venue?: int    @go(Venue,type=int)
	gate?:  string @go(Gate)

	// payload is the OPAQUE per-kind input for an EXTERNAL (plugin-contributed)
	// step kind.
	payload?: bytes @go(Payload,type=RawBody)

	// reverse_ops is the step's host-computed teardown ops.
	reverse_ops?: [...#ReverseOp] @go(ReverseOps)

	// Shared identity / provenance.
	candy_name?: string @go(CandyName) // every kind
	candy_dir?:  string @go(CandyDir)  // Builder / Op / ApkInstall / LocalPkgInstall

	// SystemPackagesStep + RepoChangeStep + LocalPkgInstallStep.
	format?: string @go(Format)
	// SystemPackagesStep + BuilderStep three-phase tag (int Phase: prepare/install/cleanup).
	phase?: int @go(Phase,type=int)

	// SystemPackagesStep.
	packages?: [...string] @go(Packages)
	repos?: [...{...}] @go(Repos) // each = a RepoSpec.Raw map
	options?: [...string] @go(Options)
	copr?: [...string] @go(Copr)
	modules?: [...string] @go(Modules)
	exclude?: [...string] @go(Exclude)
	keys?: [...string] @go(Keys)
	cache_mount?: [...#CacheMountView] @go(CacheMount)
	raw_install_context?: {...} @go(RawInstallContext,type=map[string]any)

	// BuilderStep.
	builder?:       string         @go(Builder)
	builder_image?: string         @go(BuilderImage)
	artifacts?: [...#ArtifactView] @go(Artifacts)
	raw_stage_context?: {...} @go(RawStageContext,type=map[string]any)
	builder_def?: #Builder @go(BuilderDef,type=*BuilderDef)
	// local_pkg is shared by BuilderStep (aur) + LocalPkgInstallStep.
	local_pkg?: #LocalPkg @go(LocalPkg,optional=nillable)

	// OpStep + ExternalPluginStep (Op + the shared user/ctx/distro fields).
	op?:            #Op    @go(Op,type=*Op)
	ctx_path?:      string @go(CtxPath)
	resolved_user?: string @go(ResolvedUser)
	to?:            string @go(To)
	candy_vars?: {[string]: string} @go(CandyVars)
	distros?: [...string] @go(Distros)

	// FileStep.
	source?: string @go(Source)
	dest?:   string @go(Dest)
	mode?:   int    @go(Mode,type=uint32) // os.FileMode underlying value
	owner?:  string @go(Owner)

	// ServicePackagedStep + ServiceCustomStep.
	unit?:           string @go(Unit)           // packaged unit name
	name?:           string @go(Name)           // custom service name
	target_scope?:   int    @go(TargetScope,type=Scope) // both
	enable?:         bool   @go(Enable)          // both
	overrides_text?: string @go(OverridesText)   // packaged drop-in
	overrides_path?: string @go(OverridesPath)   // packaged drop-in
	prior_enabled?:  bool   @go(PriorEnabled)    // packaged
	unit_text?:      string @go(UnitText)        // custom unit body
	unit_path?:      string @go(UnitPath)        // custom unit path

	// ShellHookStep.
	env_vars?: {[string]: string} @go(EnvVars)
	path_add?: [...string] @go(PathAdd)
	env_file?: string @go(EnvFile)

	// ShellSnippetStep.
	origin?: string @go(Origin)
	shell?:  string @go(Shell)
	snippet?: string @go(Snippet)
	path_append?: [...string] @go(PathAppend)
	destination?: string @go(Destination)
	marker?:      string @go(Marker)
	use_dropin?:  bool   @go(UseDropin)
	priority?:    int    @go(Priority,type=int)

	// RepoChangeStep.
	file?:     string @go(File)
	content?:  string @go(Content)
	checksum?: string @go(Checksum)

	// ApkInstallStep.
	apk_packages?: [...#CandyApk] @go(ApkPackages,type=[]ApkPackageSpec)

	// LocalPkgInstallStep — the deploy-time executor downloads the published
	// package from the distro repo (the download leg) instead of building from
	// source, so the IR carries the package reference (name + version) rather
	// than the old source-dir anchors (pkgbuild_ref/project_dir).
	package_name?: string @go(PackageName)
	version?:      string @go(Version)

	// ExtractStep — a candy's `extract:` entry materialized onto a machine venue.
	extract_source?: string @go(ExtractSource) // OCI image ref to extract from
	extract_path?:   string @go(ExtractPath)   // path in the source image (file or dir)
	extract_dest?:   string @go(ExtractDest)   // absolute destination path on the venue
}

// #DeployVenue is the venue descriptor the host puts in op.Env for an external
// deploy Invoke: the deploy's name plus the merged deploy-node env (KEY=VALUE
// lines flattened to a map).
#DeployVenue: {
	deploy_name!: string @go(DeployName)
	env?: {[string]: string} @go(Env)
	substrate?: bytes @go(Substrate,type=RawBody)
}

// #VenueDescriptor is the SELF-CONTAINED, serializable description of a
// deploy venue's executor that a substrate LIFECYCLE plugin's
// OpPrepareVenue / OpTeardownExecutor returns (F6). K1-unblock W3 Unit B added the
// "container" kind (engine/container_name) so a plugin-constructed
// deploykit.ContainerChain venue — a single-hop *NestedExecutor{Parent:ShellExecutor{},
// Jump:{Kind:JumpPodmanExec|JumpDockerExec}}, the MOST COMMON check-runner venue — round-trips
// through kit.DescriptorFromExecutor/VenueFromDescriptor exactly like "shell"/"ssh" already do.
// This does NOT generalize to arbitrary N-hop composition (a genuinely multi-hop NestedExecutor
// still degrades to the zero descriptor, unchanged) — it closes the one enumerable, well-known
// single-hop shape ContainerChain always produces.
#VenueDescriptor: {
	kind!: string @go(Kind) // "shell" | "ssh" | "container"
	user?: string @go(User)
	host?: string @go(Host)
	port?: int    @go(Port,type=int)
	args?: [...string] @go(Args)
	connect_timeout?: int @go(ConnectTimeout,type=int)
	// engine/container_name are set ONLY for kind "container": the container engine
	// ("podman"/"docker", selecting JumpPodmanExec vs JumpDockerExec) and the target container
	// name ContainerChain jumps into.
	engine?:         string @go(Engine)
	container_name?: string @go(ContainerName)
}

// #Diagnostic is one finding from a plugin kind's deep OpValidate check (F7/C8).
#Diagnostic: {
	severity?: string @go(Severity) // "error" | "warning" (empty → error)
	message!:  string @go(Message)
	path?:     string @go(Path)
}

// #Diagnostics is the OpValidate reply. HasErrors() is a pure Go METHOD — CUE
// cannot express it — and stays hand-written in spec/deploy_methods.go.
#Diagnostics: {
	items?: [...#Diagnostic] @go(Items)
}

// #StructuralKindLoadEnv is the OpLoad invocation context (op.Env) the host
// threads to a STRUCTURAL class:kind plugin (F5 authored-member input-threading).
#StructuralKindLoadEnv: {
	members?: {[string]: #Deploy} @go(Members,type=map[string]*Deploy)
	// standalone is the host-pre-decoded CANONICAL node channel (candy/plugin-substrate,
	// candy/plugin-candy).
	standalone?: #StandaloneLoad @go(Standalone,optional=nillable)
}

// #StandaloneLoad carries a structural kind's host-pre-decoded canonical node.
// Exactly one of Deploy / Template / Box / Candy is set, matching Shape.
#StandaloneLoad: {
	shape!: string @go(Shape) // "deploy" | "template" | "candy-image" | "candy-layer"
	deploy?:   #Deploy @go(Deploy,optional=nillable)   // Shape=="deploy": the full pre-decoded FleetNode
	template?: bytes   @go(Template,type=RawBody)      // Shape=="template": the pre-decoded typed template value's JSON
	box?:      #Box    @go(Box,optional=nillable)      // Shape=="candy-image": the pre-decoded IMAGE (spec.Box)
	candy?:    #Candy  @go(Candy,optional=nillable)    // Shape=="candy-layer": the pre-decoded LAYER (spec.Candy)
}

// #AndroidDeployVenue is the preresolved deploy:android substrate payload the
// host's android deploy preresolver produces (in DeployVenue.Substrate) and
// the candy/plugin-adb deploy:android provider decodes.
#AndroidDeployVenue: {
	adb_addr!: string @go(AdbAddr)
	engine?:    string @go(Engine)
	container?: string @go(Container)
	serial?:    string @go(Serial)
	google_email?: string @go(GoogleEmail)
	google_token?: string @go(GoogleToken)
	installs?: [...#CandyApk] @go(Installs,type=[]ApkPackageSpec)
	// boot_timeout / install_deadline / install_interval are the readiness +
	// install-retry windows the host ships (no magic numbers in the plugin).
	boot_timeout?:     string @go(BootTimeout)
	install_deadline?: string @go(InstallDeadline)
	install_interval?: string @go(InstallInterval)
}

// #KubernetesDeployVenue is the preresolved deploy:kubernetes substrate payload the host's
// kubernetes deploy preresolver produces in DeployVenue.Substrate and the
// candy/plugin-kube deploy:kubernetes provider decodes.
#KubernetesDeployVenue: {
	overlay_path!: string @go(OverlayPath) // <root>/overlays/<inst> — the `kubectl apply -k` argument
	tree_root?:    string @go(TreeRoot)    // <root> = .opencharly/k8s/<name> — removed at teardown
	kube_context?: string @go(KubeContext) // kind:kubernetes template's kubeconfig_context → `kubectl --context` (empty → current-context)
	deploy_name?:  string @go(DeployName)  // for plugin-side log messages
}

// #DeployReply is the structured result an external deploy provider returns
// from an OpExecute Invoke: the teardown ops the host records into the
// ledger, plus a provenance record.
#DeployReply: {
	reverse_ops?: [...#ReverseOp] @go(ReverseOps)
	record!: #DeployReplyRecord @go(Record)
}

// #DeployReplyRecord names the ledger CandyRecord the host writes for an
// external deploy: the logical candy whose ReverseOps drive teardown, plus
// its version.
#DeployReplyRecord: {
	candy!:   string @go(Candy)
	version?: string @go(Version)
}
#Iterate: {
	agent?: [...(string & !="")]
	sandbox!:           string & !=""
	plateau_iteration?: int & >=0 @go(PlateauIteration,type=int)
	prompt?:            string
	note?:              bool @go(,type=*bool)
	env?:               #StrMap
	mcp_endpoint?:      string @go(MCPEndpoint,type=*string)
}

// #OCIEmitStepParams is the plain-Go param struct the pod-overlay candy marshals into a
// HostBuild("step-emit", #StepEmitRequest{Word:"oci-emit-step", Payload:<#OCIEmitStepParams>})
// per install step (promoted from sdk/deploykit — #55 import-purity, Cone V value-vocabulary).
// Dir is the project dir the host "oci-emit-step" emitter uses to look up the cached overlay
// buildEngineContext (a live *Generator/DistroDef/BuilderConfig/Box cannot cross the wire);
// StepView is the step's wire form (StepToView) and PlanView the step's plan wire form — the
// host reconstructs both (StepFromView/PlanFromView) then calls ociEmitStep, returning the
// rendered Containerfile fragment. StepView/PlanView are required (always set by the caller);
// Dir is optional (empty for the non-overlay per-step emit path).
#OCIEmitStepParams: {
	dir?:       string           @go(Dir)
	step_view!: #InstallStepView @go(StepView)
	plan_view!: #InstallPlanView @go(PlanView)
}

// #SaveDeployStateInput holds the deployment parameters SaveDeployState persists to charly.yml
// (promoted from sdk/deploykit — #55 import-purity, Cone V value-vocabulary; the SaveDeployState
// FUNCTION stays in deploykit). Every field is a spec/stdlib type. The deploy candies
// (plugin-deploy-pod, plugin-fleet) pass it DIRECTLY to deploykit.SaveDeployState plugin-side
// (#55 K4 — no host seam carries it across the wire anymore; the fields are ephemeral
// in-operation, never persisted as-is — the node-form marshal writes only the resulting entry).
#SaveDeployStateInput: {
	ports?: [...string] @go(Ports)
	// set_ports gates whether Ports is written to charly.yml at all: `charly config <name>` (no
	// --port flags) and `charly update <name>` must not silently overwrite operator port overrides
	// with image-label defaults.
	set_ports?: bool @go(SetPorts)
	env?: {[string]: string} @go(Env)
	clean_env?: bool   @go(CleanEnv) // true = replace env map; false = merge (upsert by key)
	env_file?:  string @go(EnvFile)
	network?:   string @go(Network)
	security?:  #Security @go(Security,optional=nillable)
	volume?: [...#DeployVolume] @go(Volume)
	sidecar?: {[string]: bytes} @go(Sidecar,type=map[string]RawBody)
	tunnel?: #Tunnel @go(Tunnel,type=*TunnelYAML)

	// secret_names lists env var names declared as secret_accepts / secret_requires on the image;
	// saveDeployState defensively strips any matching KEY=VAL entries from both the input Env and
	// the existing persisted entry before writing.
	secret_names?: [...string] @go(SecretNames)

	// Disposable + Lifecycle classification (see /charly-internals:disposable).
	set_disposable?: bool   @go(SetDisposable)
	disposable?:     bool   @go(Disposable)
	set_lifecycle?:  bool   @go(SetLifecycle)
	lifecycle?:      string @go(Lifecycle)

	// Box + Target — the schema-required fields per the require-image cutover. Written when non-empty
	// AND when the existing entry doesn't already have a value (don't clobber operator-authored refs).
	box?:    string @go(Box)
	target?: string @go(Target)

	// resolved_image is the concrete overlay image ref produced by a pod deploy's add_candy: overlay
	// build, persisted by PrepareVenue so config/start deploy EXACTLY that overlay.
	resolved_image?: string @go(ResolvedImage)

	// vm_state + vm_cross_ref — the vm substrate's persisted runtime state + the kind:vm cross-ref,
	// shipped by the externalized vm plugin's PrepareVenue reply as the generic State patch.
	vm_state?:     #VmDeployState @go(VmState,type=*VmDeployState)
	vm_cross_ref?: string         @go(VmCrossRef)

	// Resource-arbitration axis: the holder-side Preemptible block + the claimant-side
	// requires_exclusive / requires_shared token lists. Persisted so a deploy/bed MEMBER round-trips
	// its arbiter role through the per-host overlay. Written when non-empty.
	preemptible?: #Preemptible @go(Preemptible,type=*PreemptibleConfig)
	requires_exclusive?: [...string] @go(RequiresExclusive)
	requires_shared?: [...string] @go(RequiresShared)
}
