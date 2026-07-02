// CUE schema for the `deploy` AND `check` kinds. Both validate ONE
// BundleNode (charly/deploy.go): a `deploy:` map entry, or a `kind: check`
// bed (disposable:true + usually iterate:/plan:). #Deploy is the base node;
// #Check narrows it to the bed invariants. CLOSED. Shared defs REFERENCED, not
// redefined (R3): #Step/#Op/#Security/#EnvVar/#InstallOpts/#Duration/#CalVer/
// #EntityRef/#PortPin/#VmSize/#Sidecar/#ShellSpec live in _common.cue / sidecar.cue.

#Deploy: {
	version?:     #CalVer
	description?: string & !=""

	// target is DERIVED from the node's discriminator kind + cross-ref at load
	// (buildBundleNode/inferBundleTarget) — NOT authored in node-form. Optional
	// here so #Check's arms can pin it; the #BundleValue arm (node.cue) rejects an
	// authored `target:` outright. The former default `*"pod"` is dropped (Go's
	// classifyTarget supplies the empty→pod default). Generated as a plain Go
	// `string` (the loader stamps it; the CUE enum still validates a pinned value).
	target?: ("pod" | "vm" | "k8s" | "local" | "android") @go(Target,type=string) // loader-DERIVED (yaml:"-")

	// member_of + inside are loader-DERIVED runtime fields (never authored;
	// rejected by #BundleValue): member_of marks a folded sibling-member entry,
	// inside names the venue a nested resource deploys into. Generated for the Go
	// tree-walker, forbidden in authoring.
	member_of?: string @go(MemberOf)
	inside?:    string @go(Inside)

	// agent_provisioned marks a resource member/child the AI deploys at run time
	// (the iterate-benchmark contract): image-less (no box:), not folded to a
	// top-level entry, exempt from the box-required validators. See deploy.go.
	agent_provisioned?: bool @go(AgentProvisioned)

	// EDGE-INHERIT cutover B: the substrate kind is the EDGE discriminator (pod:/vm:/
	// k8s:/local:/android:/group:), so the deploy carries only NON-kind cross-refs:
	//   from  — inherit a SAME-kind template by name (vm/k8s/local/android deploys).
	//   image — the box/OCI artifact a pod/k8s/android RUNS (the former `box:`).
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
	env?: [...#EnvVar]
	env_file?: string & !="" @go(EnvFile)
	network?:  string & !=""
	engine?:   "podman" | "docker"
	security?: #Security @go(Security,optional=nillable)
	secret?: [...#DeploySecret]
	volume?: [...#DeployVolume]
	sidecar?: {[string]: #Sidecar}
	forward_gpg_agent?: bool @go(ForwardGpgAgent,type=*bool)
	forward_ssh_agent?: bool @go(ForwardSshAgent,type=*bool)

	plan?: [...#Step]
	iterate?: #Iterate @go(Iterate,optional=nillable)
	shell?: [...#DeployShellOverlay]

	add_candy?: [...(string & !="")] @go(AddCandy)
	install_opts?: #InstallOpts @go(InstallOpts,optional=nillable)

	host?: string & !=""
	user?: string & !=""
	ssh_arg?: [...string] @go(SSHArgs)

	cpus?:      int & >=1 @go(,type=int)
	ram?:       #VmSize
	disk_size?: #VmSize @go(DiskSize)

	kubernetes?: #K8sDeploy @go(Kubernetes,optional=nillable)

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

// #Check — a kind:check bed. Structurally IDENTICAL to #Deploy (same BundleNode
// Go struct), so it is a plain reference (R3 — no field duplication; stays CLOSED
// because #Deploy is closed). The bed-mode invariants the former `& (A|B|C)`
// disjunction expressed — disposable required + bed-legal target ∈ {pod,vm,local,
// android} for the deterministic/ephemeral modes, the iterate AI-benchmark mode,
// and the ephemeral⇒disposable promotion — are enforced in GO at load time
// (validateCheckBeds + validateEphemeralUnified in unified.go), which is the
// SINGLE source of truth for the actual bundle-form beds (a node-form check bed
// is a `bundle:` node validated via #BundleValue=#Deploy, so the disjunction was
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

// DeployShellOverlay (deploy.go) — per-deploy shell-rc overlay. CLOSED: the Go
// UnmarshalYAML allowlists exactly these keys + the 4 shell names.
#DeployShellOverlay: {
	id?:     string & !="" @go(ID)
	origin?: string & !=""
	skip?:   bool
	init?:   string
	path_append?: [...string] @go(PathAppend)
	path?:     string
	priority?: int        @go(,type=int)
	bash?:     #ShellSpec @go(Bash,optional=nillable)
	zsh?:      #ShellSpec @go(Zsh,optional=nillable)
	fish?:     #ShellSpec @go(Fish,optional=nillable)
	sh?:       #ShellSpec @go(Sh,optional=nillable)
}

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
// VmDeployState is hand-written in spec/union_types.go (the field references it
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
#K8sDeploy: {
	namespace?: #EntityRef
	workload?:  "Deployment" | "StatefulSet" | "DaemonSet" | "Pod" | "Job" | "CronJob"
	patches?: [...#K8sPatch]
	raw?: [...string]
}
#K8sPatch: {
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
#Iterate: {
	agent?: [...(string & !="")]
	sandbox!:           string & !=""
	plateau_iteration?: int & >=0 @go(PlateauIteration,type=int)
	prompt?:            string
	note?:              bool @go(,type=*bool)
	env?:               #StrMap
	mcp_endpoint?:      string @go(MCPEndpoint,type=*string)
}
