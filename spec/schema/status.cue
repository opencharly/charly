// CUE schema for the deployment-status wire types — the shared shape the
// command:status plugin renders and every substrate plugin's status-collect Op
// returns. Package-less; concatenated into the spec compilation unit. These are
// NOT authoring kinds (never in #Node/#Op) — pure generated wire/render structs,
// single-sourced here so `task cue:gen` produces the Go structs (charly aliases
// them via vmshared/spec_aliases.go). @go names match the Go field names the
// renderers reference; JSON tags drive both the `charly status --json` output and
// the host<->plugin wire.

// #PortMapping — one published port's structured runtime mapping (host IP/port ->
// container port/proto). Surfaces on #DeploymentStatus so renderers + host probes
// consume it without re-parsing.
#PortMapping: {
	host_ip?:        string @go(HostIP)
	host_port:       int    @go(HostPort,type=int)
	container_port:  int    @go(CtrPort,type=int)
	protocol:        string @go(Proto)
}

// #ToolStatus — one live tool-probe result row. status "-" means the tool is not
// configured in this deployment; it is filtered before render.
#ToolStatus: {
	name:    string @go(Name)
	status:  string @go(Status)
	port?:   int    @go(Port,type=int)
	detail?: string @go(Detail)
}

// #SubstrateKind — the substrate discriminator enum. CUE validates the value at
// the wire boundary; @go(-) suppresses a (degraded) generated Go type — the named
// Go type + typed consts are hand-written in spec/status_types.go and referenced by
// #DeploymentStatus.kind via @go(Kind,type=SubstrateKind).
#SubstrateKind: "pod" | "vm" | "kubernetes" | "local" | "android" @go(-)

// #DeploymentStatus — the rendered shape for the table + JSON outputs across every
// deployment substrate (pod / vm / kubernetes / local / android). kind discriminates the
// substrate; nested carries multi-hop children (RECURSIVE self-reference, populated
// by the nested overlay); source records provenance (libvirt|ledger|adb|tree|podman).
#DeploymentStatus: {
	kind:       #SubstrateKind @go(Kind,type=SubstrateKind)
	image:      string @go(Image)
	image_ref?: string @go(ImageRef)
	instance?:  string @go(Instance)
	status:     string @go(Status)
	uptime?:    string @go(Uptime)
	container:  string @go(Container)
	ports?: [...#PortMapping] @go(Ports)
	devices?: [...string] @go(Devices)
	tools?: [...#ToolStatus] @go(Tools)
	volumes?: [...string] @go(Volumes)
	network?:  string @go(Network)
	tunnel?:   string @go(Tunnel)
	secrets?: [...string] @go(Secrets)
	run_mode: string @go(RunMode)
	nested?: [...#DeploymentStatus] @go(Nested)
	source?: string @go(Source)
}

// #StatusSubstrateRequest — the host-collection request the command:status plugin sends over
// HostBuild("status-substrate"). single=true selects the pod-scoped detail path (box+instance);
// otherwise the full multi-substrate fan-out (include_all mirrors --all). The declared nested tree
// (formerly resolved here via `nested`) is now resolved PLUGIN-SIDE (candy/plugin-status's own
// buildStatusRootsTree, K5) directly off the resolved-project envelope + deploykit.LoadFleetConfig
// — no host round-trip, so this request carries no nested flag anymore.
#StatusSubstrateRequest: {
	single?:      bool   @go(Single)
	include_all?: bool   @go(IncludeAll)
	box?:         string @go(Box)
	instance?:    string @go(Instance)
}

// #StatusNestedNode — one pre-resolved node of the DECLARED nested tree, now built PLUGIN-SIDE
// (candy/plugin-status/nested_tree.go, K5) directly from the resolved-project envelope +
// deploykit.LoadFleetConfig/MergeDeployConfigs/ClassifyTarget/ResolveDeployChain — all sdk-portable,
// so this is no longer a host-computed wire payload; it stays a CUE def only because
// #StatusNestedNode is self-recursive and the plugin still needs the generated Go type. key is the
// declared child key (the Image cell). match_keys index the flat rows; for a ROOT node key itself is
// the flat match. RECURSIVE self-reference mirrors the deploy tree nesting.
#StatusNestedNode: {
	key:          string          @go(Key)
	path:         string          @go(Path)
	kind:         #SubstrateKind  @go(Kind,type=SubstrateKind)
	has_children: bool            @go(HasChildren)
	match_keys?: [...string]      @go(MatchKeys)
	live_status?: string          @go(LiveStatus)
	children?: [...#StatusNestedNode] @go(Children)
}

// #StatusSubstrateReply — the host-collection result: the flat rows (all substrates, already
// probed) and — on the single path — the one detail row. The declared nested roots are no longer
// carried here (K5: resolved plugin-side, see #StatusNestedNode) — the candy computes its own roots
// and applies the PURE overlay(rows, roots) then renders.
#StatusSubstrateReply: {
	rows?: [...#DeploymentStatus]   @go(Rows)
	single?: #DeploymentStatus      @go(Single)
}

// #SubstrateStatusRequest — the per-substrate COLLECTOR request the host sends to the substrate
// plugin's OpStatusCollect (P14a: the cleanly-movable collectors — pod live + local + the probes —
// relocated into candy/plugin-substrate, served on the kind provider's Invoke by word
// pod/vm/kubernetes/local/android). The host passes the scalar inputs a sdk-only candy cannot derive:
// the engine binary name (engine_bin), the run mode, the quadlet dir (pod's quadlet-description
// enrichment + enabled-but-not-running append), include_all (--all), and — on the single path —
// box+instance. NO deploy-cone (FleetConfig/UnifiedFile) crosses this seam: the deploy
// enrichment stays host-side until K5, applied to the live rows this reply returns. vm/kubernetes/android
// are deferred to K5 (their collectors are deploy-cone-coupled); the plugin returns no rows for
// those words until then.
#SubstrateStatusRequest: {
	include_all?: bool   @go(IncludeAll)
	run_mode:   string   @go(RunMode)
	quadlet_dir: string  @go(QuadletDir)
	engine_bin: string   @go(EngineBin)
	single?:    bool     @go(Single)
	box?:       string   @go(Box)
	instance?:  string   @go(Instance)
}

// #SubstrateStatusReply — the per-substrate COLLECTOR result: the LIVE rows the substrate plugin
// produced (pod: snapshot-derived + live mounts + tool probes; local: install-ledger rows). The
// host applies the deploy enrichment (tunnel + image-label fallback) to the pod rows after they
// cross this seam; the single field carries the one pod detail row.
#SubstrateStatusReply: {
	rows?:   [...#DeploymentStatus] @go(Rows)
	single?: #DeploymentStatus      @go(Single)
}
