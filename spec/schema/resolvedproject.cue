// CUE schema for the RESOLVED-project envelope (K5, the S-K5 keystone). #ResolvedProject is the
// generic, sdk-expressible RESOLVED/MATERIALIZED projection of a whole project — the third and
// final member of the envelope SPINE (spec.ParsedProject → #LoadedProject → #ResolvedProject).
// #LoadedProject is the PARSED, un-materialized stage; #ResolvedProject is what the host's resolve
// engines (ResolveBox / ScanAllCandy / the folded uf.Fleet deploy tree) already COMPUTE, serialized
// as ONE generic view the ~20k of K5 IOU consumers (inspect/list, the fleet-add box graph, status,
// the bed runner) read INSTEAD of the host types (*ResolvedBox / runtime Candy). It carries ONLY
// CONFIG+RESOLVE DATA: the host-only RESOLVE-time compute-cache pointers (DistroConfig / DistroDef /
// BuilderConfig / InitSystem / InitDef / CandyCaps — the 6 json:"-" fields of buildkit.ResolvedBox)
// and all LIVE state (container snapshots, engine truth, live executors) are EXCLUDED by design and
// stay host / verb-probe. Most of the envelope is ALREADY CUE-sourced (spec.Deploy, ParsedProject,
// DeploymentStatus, ResolvedKubernetes/Android); this file adds exactly TWO new views — #ResolvedBoxView +
// #CandyView — composed with the existing #Deploy tree.
//
// Package-less; concatenated into the spec compilation unit. NOT an authoring kind (never in
// #Node/#Op) — pure generated wire/projection types, single-sourced here so `task cue:gen` produces
// the Go structs (charly aliases them via vmshared/spec_aliases.go). Shared defs (#BoxMerge,
// #CandyRef, #CandyMCPProvide, #Deploy) come from box.cue / _common.cue / candy.cue / deploy.cue.

// #ResolvedBoxView — the resolved box METADATA a consumer reads: EXACTLY the non-json:"-" fields of
// buildkit.ResolvedBox, in declaration order, so this view is the wire-safe half of what
// `charly box inspect` already serializes (json.MarshalIndent(*ResolvedBox) — which never reaches the
// 6 json:"-" compute-cache pointers). Field order mirrors ResolvedBox so a future inspect relocation
// projects into this view byte-faithfully. Every field optional (a projection envelope); name is the
// stable key.
#ResolvedBoxView: {
	name!:                    string
	version?:                 string
	effective_version?:       string @go(EffectiveVersion)
	status?:                  string
	info?:                    string
	check_level?:             string @go(CheckLevel)
	base?:                    string
	from?:                    string
	bootstrap_builder_image?: string @go(BootstrapBuilderImage)
	platforms?: [...string]
	tag?:      string
	registry?: string
	pkg?:      string @go(Pkg)
	distro?: [...string]
	build_formats?: [...string] @go(BuildFormats)
	tags?: [...string]
	candy?: [...string]
	user?:         string
	uid?:          int @go(UID)
	gid?:          int @go(GID)
	home?:         string
	user_adopted?: bool      @go(UserAdopted)
	merge?:        #BoxMerge @go(Merge,optional=nillable)
	builder?: {[string]: string}
	builder_capabilities?: [...string] @go(BuilderCapabilities)
	auto?:             bool
	network?:          string
	data_image?:       bool   @go(DataImage)
	// OCI entrypoint/cmd baked into the image's OCI config (final Containerfile
	// ENTRYPOINT/CMD) — carried on the resolved view so the plugin-build resolve
	// leg (NewSpecResolvedBox) can re-emit them without the live *Candy graph.
	entrypoint?:       [...(string & !="")] @go(Entrypoint, type=[]string)
	cmd?:              [...(string & !="")] @go(Cmd, type=[]string)
	is_external_base?: bool   @go(IsExternalBase)
	full_tag?:         string @go(FullTag)

	// box-AGGREGATES — the cross-candy effective values `charly box inspect --format
	// ports|volumes|aliases|engine` prints (the host projector runs CollectBoxPorts /
	// CollectBoxVolume / CollectBoxAlias / ResolveBoxEngine into these). engine is the raw
	// resolver result ("" → the command body renders "(global default)").
	ports?: [...string] @go(Ports)
	volumes?: [...#ResolvedVolumeMount] @go(Volumes)
	aliases?: [...#CandyAlias] @go(Aliases)
	engine?: string @go(Engine)

	// box-AUTHORED surfaces the validate ENGINE checks (task #60): the box's OWN authored `plan:`
	// (validateOps box-arm — every Op validated for authoring errors) and `alias:` (validateAliases
	// box-arm — name char-set + cross-entry dedup). Distinct from the resolved `aliases` AGGREGATE
	// above (cross-candy CollectBoxAlias) — these are the box entity's raw authored entries the plugin
	// re-validates, so the whole validateOps/validateAliases move to plugin-box (ruling-a: grow the
	// envelope rather than keep the R-rule in core).
	plan?: [...#Step] @go(Plan)
	authored_aliases?: [...#BoxAlias] @go(AuthoredAliases)

	// box-AUTHORED deploy-overlay surfaces ExportAllBox reads (K5-Unit-1, the #67 keystone): the
	// box entity's OWN authored env / env_file / security / network / raw description — the fields
	// `charly fleet export --all` projects into a FleetConfig so the deploy-state model can be
	// built from the RESOLVED-PROJECT ENVELOPE, not the live *Config graph. description is the RAW
	// authored string (distinct from info above, which is its descriptionInfo first-line summary);
	// env/env_file/security are the box-authored deploy-overlay defaults. network is already carried
	// above; these three + description are the additions. Filled by projectBoxAggregates from
	// cfg.BoxConfig(name), alongside plan/authored_aliases.
	description?: string & !=""
	env?: {PATH?: _|_, [string]: #StrVal} @go(Env,type=map[string]string)
	env_file?: string & !="" @go(EnvFile)
	security?: #Security     @go(Security,optional=nillable)

	// build-render label carriers (#67, the build_resolve RENDER-leg death). The host projector runs
	// the label collectors + caps + init resolve + skill probe + status into a fully-baked #BakedLabelSet
	// (the EXACT wire data writeLabels emits) so the deploykit render's WriteLabels — relocated out of
	// core — FORMATS it byte-for-byte WITHOUT the live *Candy graph. #BakedLabelSet is the BUILD-side
	// wire-form carrier (env map, volume LabelVolumeEntry, +init_label_key/oci_labels), distinct from
	// #BoxMetadata (the deploy-side hub). caps carries the aggregated candy capability surface the
	// render reads beyond the baked labels (the two render-gating booleans; oci_labels also ride the
	// baked set for the formatter). Both filled ONLY in the build-render projection; empty for the
	// resolved-project / validate path (which never renders), so that path stays lean.
	baked_metadata?: #BakedLabelSet           @go(BakedMetadata,optional=nillable)
	caps?:           #AggregatedCandyCapsView @go(Caps,optional=nillable)

	// build-RENDER init/candy-order caches (#67, the build_resolve RENDER-leg death). The host
	// render-prep computes these over the live *Candy/*Config graph (globalOrderForBox,
	// AggregateCandyCapabilities, ActiveInit, ResolveInitSystem) and either sets them directly on
	// the live ResolvedBox (the parity-test live path) or carries them through this envelope (the
	// plugin-build drive path, re-attached by NewSpecResolvedBox) so the deploykit render reads
	// RESOLVED caches WITHOUT the live graph. Filled ONLY in the build-render projection; empty for
	// the resolved-project / validate path. render_candy_order is the per-box cache-optimal candy
	// sequence; init_system/init_def the active init; active_inits the full active-init map
	// (EmitInitFragmentStages + EmitInitAssembly read it).
	render_candy_order?: [...string] @go(RenderCandyOrder)
	init_system?: string @go(InitSystem)
	init_def?:    _      @go(InitDef,type=*ResolvedInit)
	active_inits?: {[string]: _} @go(ActiveInits,type=map[string]*ResolvedInit)
}

// #AggregatedCandyCapsView — the box-level aggregated candy capability surface the build RENDER reads
// (a wire projection of buildkit.AggregatedCandyCaps): the arbitrary candy-declared oci_labels + the
// two booleans that gate the render (preserve_user → the final-USER-reset skip + the bootc round-trip
// label; needs_root_after_init → the post-candy root reset). Carried so the plugin-side render
// (NewSpecResolvedBox) reattaches img.CandyCaps WITHOUT re-aggregating over the live *Candy graph.
#AggregatedCandyCapsView: {
	preserve_user?:         bool @go(PreserveUser)
	needs_root_after_init?: bool @go(NeedsRootAfterInit)
	oci_labels?: {[string]: string} @go(OCILabels)
}

// #ResolvedVolumeMount — one entry of the box-aggregate volume list (CollectBoxVolume): the
// charly-<box>-<name> volume name + the home-expanded container path. Mirrors deploykit.VolumeMount
// (a permanent deploykit home, never wire-marshaled there — spec carries the envelope copy).
#ResolvedVolumeMount: {
	volume_name?:    string @go(VolumeName)
	container_path?: string @go(ContainerPath)
}

// #CandyView — the resolved candy GRAPH node a consumer reads: identity + dep-graph + provides +
// ports/services (the exported surface of the runtime Candy). The Has* filesystem probes, the
// unexported package/service sections, and the *CandyPluginDecl stay host — this is NOT the candy
// BUILD model (plan-steps / package-format sections), which is the CandyModel / S-CM concern (K3-D),
// distinct by design. require / candy pin to the bare map-key ref form (#CandyRef is @go(-), so a
// list of it generates []string). name is the stable key.
#CandyView: {
	name!:        string
	version?:     string
	description?: string
	status?:      string
	info?:        string
	remote?:      bool
	repo_path?:   string @go(RepoPath)
	// sub_path_prefix — the parent directory within the repo for sibling ref resolution
	// (e.g. "candy/"). Build-mode remote-candy COPY-source leg: RepoPath + SubPathPrefix +
	// ref reconstruct a remote candy's context path. Filled by the resolve projector; the
	// specCandyAdapter returns it so the plugin-side render reproduces remote COPY sources
	// WITHOUT the live *Candy (K3-D render move, #67 — the GetSubPathPrefix deferral).
	sub_path_prefix?: string @go(SubPathPrefix)
	is_plugin?:       bool   @go(IsPlugin)
	// the candy's OWN declared plugin block (validatePluginCandy SUBJECT, task #60): the
	// `plugin.providers:` capability strings + `plugin.source:` so the validate plugin can check each
	// declared BUILTIN `<class>:<word>` is a member of ResolvedProject.ProviderCapabilities (the TARGET
	// set). Empty for a non-plugin candy. is_plugin stays the cheap presence bool for inspect/list.
	plugin_providers?: [...string] @go(PluginProviders)
	plugin_source?: string @go(PluginSource)
	require?: [...#CandyRef] @go(Require)
	candy?: [...#CandyRef] @go(IncludedCandy)
	env_provide?: {[string]: string} @go(EnvProvides)
	mcp_provide?: [...#CandyMCPProvide] @go(MCPProvide)
	agent_provide?: [...#AgentRuntimeCapability] @go(AgentProvide)
	terminal_profiles?: {[string]: #TerminalProfile} @go(TerminalProfiles,type=map[string]TerminalProfile)
	port?: [...int] @go(Ports)
	service_name?: [...string] @go(ServiceNames)

	// per-candy list-subcommand sources: `charly box list routes|volumes|aliases|services`.
	// route/volumes/aliases carry the authored detail each subcommand prints (their presence IS
	// the RouteCandy/VolumeCandy/AliasCandy predicate). has_init + port_relay reconstruct the
	// InitCandy predicate (HasAnyInit() || PortRelayPorts>0) for `list services`.
	route?: #RouteConfig @go(Route,optional=nillable)
	volumes?: [...#CandyVolume] @go(Volumes)
	aliases?: [...#CandyAlias] @go(Aliases)
	has_init?: bool @go(HasInit)
	// init_systems — the PER-INIT-SYSTEM trigger map (W9 finding: HasInit alone is one AGGREGATE
	// "any init" bool, which cannot answer "does this candy trigger init system Y" — the question
	// deploykit's HasInit(initName) / sdk/deploykit's EmitInitFragmentStages actually ask). Mirrors
	// the live *Candy.InitSystems field exactly; populated by the SAME cross-candy host-completion
	// pass PopulateCandyInitSystem runs (once, after every candy is scanned) — see
	// sdk/deploykit.specCandyAdapter.HasInit, which reads this field directly.
	init_systems?: {[string]: bool} @go(InitSystems)
	port_relay?: [...int] @go(PortRelayPorts,type=[]int)

	// capabilities — the per-candy capability surface the validate ENGINE reads (task #60,
	// ruling a). The projector fills it from the candy's `capabilities:` block; the validate
	// plugin re-derives it via boxPreserveUser over a box's candy order (a boolean OR of
	// preserve_user, order-independent) + the PreserveUser rule (the validatePackagedServices
	// box-arm). An aggregate over RESOLVED models belongs on the
	// envelope, not a host route-B diagnostic — a lean envelope is not a virtue when it keeps
	// an R-rule in core.
	capabilities?: #CandyCapabilitiesView @go(Capabilities,optional=nillable)
}

// #CandyCapabilitiesView — the per-candy capability surface a consumer reads off the envelope.
// Carries exactly the caps the validate ENGINE consumes (preserve_user today); grow it only when
// a relocated rule reads a further cap. Mirrors the read half of the runtime candy's `capabilities:`
// block (charly AggregatedCandyCaps).
#CandyCapabilitiesView: {
	preserve_user?: bool @go(PreserveUser)
}

// #ResolvedProject — the whole resolved projection: the schema version, the resolved boxes keyed by
// name, the resolved candy graph keyed by name, and the deploy tree (uf.Fleet verbatim — already
// map[string]spec.Deploy). The deploy map is @go-pinned to a pointer map so `cue exp gengotypes`
// generates map[string]*Deploy (recursive tree, faithful). provides/sidecar are additive later
// members of this same envelope (added by the consumer unit that first needs them).
#ResolvedProject: {
	version?: string
	boxes?: {[string]: #ResolvedBoxView}
	candies?: {[string]: #CandyView}
	// candy_models — the serializable candy BUILD models (validate, the plan-include splicer, K3-D)
	// keyed by name, distinct from candies (identity/graph). See candymodel.cue.
	candy_models?: {[string]: #CandyModel} @go(CandyModels)
	deploy?: {[string]: #Deploy} @go(Deploy,type=map[string]*Deploy)
	// build vocabulary (the validate ENGINE consumer): distro/init PIN the hand wire structs
	// (spec.ResolvedDistro/ResolvedInit — the distro/init de-type OpResolve envelopes, no #CUE def);
	// #Builder is a clean def. Pointer maps for parity with DistroConfig/BuilderConfig/InitConfig.Init.
	distro?: {[string]: _} @go(Distro,type=map[string]*ResolvedDistro)
	builder?: {[string]: _} @go(Builder,type=map[string]*Builder)
	init?: {[string]: _} @go(Init,type=map[string]*ResolvedInit)
	// kind templates (validate localtemplates + check-include pod/vm arms + status kubernetes/adb enumeration).
	templates?: #ProjectTemplates @go(Templates,optional=nillable)
	// kind:agent catalog (the harness AI-CLI pick — plugin-check reads it off this envelope; charly feature list-agent).
	agent_bodies?: {[string]: bytes} @go(AgentBodies,type=map[string]RawBody)
	// build order + auto-intermediates (charly box list targets).
	build_targets?: [...#BuildTarget] @go(BuildTargets)
	// validate D-data word-sets (task #60, ruling: host projects the registry facts into the envelope
	// so the validate ENGINE moves to the plugin WITHOUT the plugin ever dialing the host registry).
	// The host fills these in the validate-project path only (empty for the resolved-project path);
	// clause-D kind/word-recognition DATA consulted BY WORD, never a per-kind branch.
	//   provider_capabilities — every compiled-in provider as "<class>:<word>" (validatePluginCandy
	//     checks a `source: builtin` candy's declared providers are actually compiled in).
	//   act_capable_verbs — the plugin WORDS whose act form has a build/deploy install path (the host
	//     type-asserts ProvisionActor/TypedStepProvider/BuildEmitter + connected/declared externals +
	//     command, exactly as core's opActsInBuildDeploy does), so validateCheck's act-form rule keeps
	//     builtin rejection behaviour without the registry.
	provider_capabilities?: [...string] @go(ProviderCapabilities)
	act_capable_verbs?: [...string] @go(ActCapableVerbs)
	// box_plans — the include-ready FLATTENED acceptance plan per box (the `include: box:<name>`
	// arm of the plan-composition splicer). The host projector runs the SAME base-chain walk
	// CollectDescriptions uses (candy-chain bakeable steps + the box-level bakeable plan) and
	// flattens the three sections into one ordered []Step, keyed by the QUALIFIED box name
	// (namespaced boxes like `fedora.jupyter` included) — the exact result the former in-core
	// box arm produced. A plugin cannot recompute it (base-chain + candy-order + bakeable filter
	// are host resolve Mechanisms over the runtime Candy), so the resolve engine ships it. Only
	// boxes with a non-empty flattened plan appear.
	box_plans?: {[string]: [...#Step]} @go(BoxPlans)
	// global_order — the popularity-weighted GLOBAL candy order (GlobalCandyOrder) the
	// build RENDER reads (deploykit Generator.GlobalOrderForBox → per-box candy sequence for
	// cache-optimal layering). A host resolve Mechanism over the runtime Candy graph, so the
	// resolve engine ships it (the plugin-side render can't recompute it). Filled in the
	// build-render projection (#67); empty for the resolved-project/validate path.
	global_order?: [...string] @go(GlobalOrder)
	// externalized_builders — the registry D-FACT: which detection-builder WORDS
	// (pixi/npm/aur/cargo) are externalized (their inline render crosses the OpResolve seam vs
	// an in-core vocabulary). The render selects the branch by word (deploykit
	// DetectExternalizedBuilders + candy_steps builder arm). clause-D word-recognition DATA
	// consulted BY WORD; filled in the build-render projection (#67).
	externalized_builders?: {[string]: bool} @go(ExternalizedBuilders)
	// resources — the resolved `resource:` kind entities (K1-unblock wave 1), keyed by name,
	// mirroring `deploy` byte-for-byte: the host decodes each project resource:` entity via the
	// SAME kind-blind resource-kind plugin resolve it always used (never a kernel decode of the
	// concrete #Resource shape — clause-D/M unaffected), so this is a plain data projection, not a
	// new engine. Consumer: the resource arbiter (candy/plugin-preempt), which previously reached
	// this ONLY via the bespoke ExecutorService.HostArbiter "resources" seam
	// (charly/arbiter_host.go) — the last of its 2 genuinely-LoadUnified-coupled host seams. Reading
	// it off THIS envelope instead retires that seam, leaving `resources` (this field) as the single
	// source both the arbiter plugin and any other resolved-project consumer share (R3).
	resources?: {[string]: #ResolvedResource} @go(Resources,type=map[string]*ResolvedResource)
	// primaries — the plugin-verb PRIMARY-field D-fact: a plugin verb WORD → its declared primary
	// input field (`cdp` → `method`, `file` → `file`), the SAME registry-derived kind-recognition
	// DATA the LOAD path already snapshots as spec.Threaded.Primaries (loaderThreaded), carried here
	// SAVE-side too. The deploy-state WRITER's plan RESUGAR (deploy_nodeform.go's resugarPlan —
	// collapsing an internal plugin/plugin_input pair back to the authored `<word>: <input>` sugar)
	// needs this map to collapse a single-primary input to its scalar shorthand; carrying it on the
	// envelope lets a plugin holding this projection resugar a plan WITHOUT dialing the host provider
	// registry (the K4 keystone that unblocks moving the deploy-state marshal plugin-side). clause-D
	// word-recognition DATA consulted BY WORD, never a per-kind branch; filled on every resolve path.
	primaries?: {[string]: string} @go(Primaries)
}

// #ProjectTemplates — the bare pod:/vm:/local:/kubernetes:/android: template maps carried as OPAQUE payloads
// (the uf.Pod/VM/Local/Kubernetes/Android raw bytes, verbatim). The host projector stays KIND-BLIND — it
// copies the raw template bytes with NO concrete-kind decode (a kernel that read spec.Local/#Pod/…
// would violate the boundary law + trip TestNoConcreteKindInKernel). The CONSUMING PLUGINS
// (validate localtemplates, check-include pod/vm arms, status kubernetes/adb) decode a RawBody into the
// concrete spec kind type themselves — a plugin MAY know kinds, the kernel may not.
#ProjectTemplates: {
	local?: {[string]: bytes} @go(Local,type=map[string]RawBody)
	kubernetes?: {[string]: bytes} @go(Kubernetes,type=map[string]RawBody)
	pod?: {[string]: bytes} @go(Pod,type=map[string]RawBody)
	vm?: {[string]: bytes} @go(VM,type=map[string]RawBody)
	android?: {[string]: bytes} @go(Android,type=map[string]RawBody)
}

// #BuildTarget — a build-order entry (charly box list targets): the box/intermediate name + whether
// it is an auto-computed intermediate (the `name [auto]` output).
#BuildTarget: {
	name?: string @go(Name)
	auto?: bool   @go(Auto)
}

// #ResolvedProjectRequest — the `resolved-project` HostBuild request: which project dir to resolve
// (empty = the host's cwd) and whether to include enabled:false boxes. The reply is #ResolvedProject.
#ResolvedProjectRequest: {
	dir?:                string @go(Dir)
	include_disabled?:   bool   @go(IncludeDisabled)
	// Check commands resolving a distro-submodule project must see the parent
	// superproject's in-development candies before bed classification. The host
	// applies the same local override used by the later R10 session only for the
	// duration of this projection.
	local_superproject?: bool @go(LocalSuperproject)
	// A caller compiling against an `add_candy:`/`--add-candy` ref (a candy the project's own
	// image-closure walk never reaches — the reachability-scoped remote-ref collection is
	// deliberately narrower than "every candy anywhere," R1) widens the scan the SAME way
	// ScanAllCandyWithConfigOpts' ResolveOpts.ExtraCandyRefs already does for a check bed's own
	// add_candy: (deploy_add_shared.go's deployNodePluginContext) — without this, the compile
	// plugin's OWN independent resolved-project re-fetch (candy/plugin-fleet's compile.go)
	// never discovers a remote add-candy ref the HOST's separate scanCandiesForRef call already
	// pulled in via its own synthetic-augmented scan, and BuildDeployPlan fails "candy not in
	// resolved-project envelope" (RCA'd K1-alpha regression, check-addcandy-pod/check-stepkind-
	// emit-pod).
	extra_candy_refs?: [...string] @go(ExtraCandyRefs)
	// requested_boxes — the explicit build/generate targets (`charly box generate <name>`),
	// buildkit.NormalizeBoxArgs-normalized (task #17 fix). The reachability-scoped remote-ref
	// COLLECTION walk (loaderkit.CollectRemoteRefsOpts) only follows base/builder edges from
	// ROOT-owned images by default — a namespace-qualified on-demand target
	// (`charly box generate fedora.check-pod`) that is not itself a base/builder of any root
	// image is otherwise never visited, so its own remote candy refs are silently never
	// collected/fetched, and the later candy-order RESOLVE step then fails "unknown candy" for a
	// ref the fetch step skipped. Mirrors spec.ResolveOpts.RequestedBoxes, which already pulls a
	// qualified on-demand target into buildkit.ResolveAllBox's RESOLVE set the identical way —
	// this closes the matching gap on the COLLECT half.
	requested_boxes?: [...string] @go(RequestedBoxes)
	// The `boxes` field is DELETED (K-wave 2 cone R1). It carried the plugin-resolved box set to the
	// host's `buildengine-prep` leg, whose only job was seeding the render-seam floor's Generator
	// cache. That seam is gone — every render seam peer-dispatches now — so the leg, the cache, and
	// this field all lost their single consumer together, along with deploykit's host-side
	// ResolveAllSpecBoxes/WrapSpecBoxes/SpecBoxes bridge.
}
