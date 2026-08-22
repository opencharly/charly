// CUE schema for the `init` kind. #Init validates ONE value of the `init:` map
// (InitDef — supervisord/systemd). CLOSED: every authored key is modeled (an
// unknown key is a typo). *_template fields are Go text/template (plain
// `string`). No #Step (init has no plan).

#Init: {
	candy_field?: [...(string & !="")] @go(CandyFields)
	candy_file?: [...(string & !="")] @go(CandyFiles)
	depends_candy?: string & !="" @go(DependsCandy)
	requires_capability?: [...(string & =~"^[a-z][a-z0-9_]*$")] @go(RequiresCapability)

	// The one mandatory field: the build model.
	model: "fragment_assembly" | "file_copy"

	header_file?:    string & !="" @go(HeaderFile)
	fragment_dir?:   string & !="" @go(FragmentDir)
	relay_template?: string        @go(RelayTemplate)

	stage_name?:          string & !="" @go(StageName)
	stage_header_copy?:   string        @go(StageHeaderCopy)
	stage_fragment_copy?: string        @go(StageFragmentCopy)

	assembly_template?:      string @go(AssemblyTemplate)
	system_enable_template?: string @go(SystemEnableTemplate)
	post_assembly_template?: string @go(PostAssemblyTemplate)

	entrypoint?: [...(string & !="")]
	fallback_entrypoint?: [...(string & !="")] @go(FallbackEntrypoint)

	management_tool?: string & !="" @go(ManagementTool)
	management_command?: {
		[string]: string & !=""
	} @go(ManagementCommands)

	label_key?: string & =~"^ai\\.opencharly\\.service\\.[a-z0-9]+$" @go(LabelKey)

	service_schema?: #InitServiceSchema @go(ServiceSchema,optional=nillable)
}

#InitServiceSchema: {
	service_template?:     string @go(ServiceTemplate)
	unit_path_template?:   string @go(UnitPathTemplate)
	dropin_template?:      string @go(DropinTemplate)
	dropin_path_template?: string @go(DropinPathTemplate)
	supports_packaged?:    bool   @go(SupportsPackaged)
}

// --- resolve-to-envelope wire types (Cutover F; SDD conversion, per the
// standing operator directive: a hand-written wire struct not yet CUE-sourced
// is conversion-in-progress, never a sanctioned exception). candy/plugin-init
// owns the init-system knowledge (supervisord/systemd assembly); the kernel
// stores init bodies opaquely and consumes RESOLVED fragments (rendered
// service units, emitted Containerfile init stages, the
// capability/entrypoint blob) via the plugin — it never reads spec.Init
// fields. Written out explicitly (not embedding #Init, whose `model` field is
// REQUIRED — the resolved envelope's Model stays OPTIONAL, matching the
// former hand type exactly). Plain structs — gengotypes generates them
// faithfully, no disjunction needed.

// #EnvKV is a deterministic env-var ordering helper (template iteration order).
// Named #EnvKV, NOT #KeyValue — the schemagen vocab generator auto-registers
// EVERY top-level `#<X>Value` def as a fake kind-value gate (kindValueDefs,
// `^#?([A-Za-z0-9]+)Value$`), so `#KeyValue` would spuriously register a "key"
// kind (RDD-caught live: it appeared in the generated KindValueDefs map). The
// charly-name alias `KeyValue = EnvKV` (spec/charly_names.go) preserves the
// exported Go type name every real consumer (deploykit.MapToKeyValueSlice,
// spec.SortedEnvList) already uses.
#EnvKV: {
	key!:   string @go(Key)
	value!: string @go(Value)
}

// #ServiceRenderContext is the data an init system's service_template renders
// against — everything the renderer needs, nothing else reachable. The host
// computes it per service and hands it to candy/plugin-init's OpResolve.
#ServiceRenderContext: {
	name?:  string @go(Name)
	candy?: string @go(Candy)
	exec?:  string @go(Exec)
	env?: {[string]: string} @go(Env)
	env_list?: [...#EnvKV] @go(EnvList)
	restart?:           string @go(Restart)
	working_directory?: string @go(WorkingDirectory)
	user?:              string @go(User)
	after?: [...string] @go(After)
	before?: [...string] @go(Before)
	wanted_by?: [...string] @go(WantedBy)
	stdout?:            string @go(Stdout)
	stop_timeout?:      string @go(StopTimeout)
	scope?:             string @go(Scope)
	packaged_unit?:     string @go(PackagedUnit)
	home?:              string @go(Home)
	system_unit_dir?:   string @go(SystemUnitDir)
	user_unit_dir?:     string @go(UserUnitDir)
	fragment_dir?:      string @go(FragmentDir)

	// Lifecycle directives (supervisord + systemd).
	kind?:          string @go(Kind)
	events?:        string @go(Events)
	auto_start?:    bool   @go(AutoStart,type=*bool)
	start_retries?: int    @go(StartRetries,type=int)
	start_secs?:    int    @go(StartSecs,type=int)
	stop_signal?:   string @go(StopSignal)
	exit_codes?:    string @go(ExitCodes)
	priority?:      int    @go(Priority,type=int)

	// Start hooks (systemd only — supervisord's service_template references
	// neither, exactly as it already ignores wanted_by/before). Carried through
	// from the entry's exec_start_pre/exec_start_post so the template emits one
	// ExecStartPre=/ExecStartPost= line per element, in authored order.
	exec_start_pre?: [...string] @go(ExecStartPre)
	exec_start_post?: [...string] @go(ExecStartPost)

	// render_dropin is the host-precomputed drop-in decision (the entry
	// carries Overrides). PackagedUnit != "" selects the packaged branch. The
	// host derives both from the ServiceEntry so the plugin renders from the
	// ctx alone.
	render_dropin?: bool @go(RenderDropin)
}

// #RenderedService is the renderer output: the unit text, where it lands, and
// any drop-in for packaged-unit reuse.
#RenderedService: {
	unit_text?:   string @go(UnitText)
	unit_path?:   string @go(UnitPath)
	dropin_text?: string @go(DropinText)
	dropin_path?: string @go(DropinPath)
}

// #ServiceRenderInput is candy/plugin-init's OpResolve input for the
// service-render leg: the OPAQUE init body (the chosen init system's config) +
// the host-built render context (all entry-derived fields, home-expanded,
// with the packaged/drop-in branch decisions precomputed). The plugin renders
// from the ctx alone.
#ServiceRenderInput: {
	init!: bytes @go(Init,type=RawBody)
	ctx!:  #ServiceRenderContext @go(Ctx)
}

// #ServiceRenderReply wraps the rendered service.
#ServiceRenderReply: {
	rendered?: #RenderedService @go(Rendered,optional=nillable)
}

// #ResolvedInit is the resolve-to-envelope form of an init system the kernel
// consumes for stage/assembly emission, capability labels, and the
// entrypoint contract (legs 2–4 of the init de-type). It carries the init
// system's build + runtime VALUES + templates the host reads generically —
// the kernel never imports the concrete spec.Init. Raw is the opaque init
// body threaded to the plugin's service-render leg (leg 1).
// candy/plugin-init produces it from spec.Init.
#ResolvedInit: {
	candy_field?: [...string] @go(CandyFields)
	candy_file?: [...string] @go(CandyFiles)
	depends_candy?:       string @go(DependsCandy)
	requires_capability?: [...string] @go(RequiresCapability)
	model?:               string @go(Model)
	header_file?:         string @go(HeaderFile)
	fragment_dir?:        string @go(FragmentDir)
	relay_template?:      string @go(RelayTemplate)
	stage_name?:          string @go(StageName)
	stage_header_copy?:   string @go(StageHeaderCopy)
	stage_fragment_copy?: string @go(StageFragmentCopy)
	assembly_template?:      string @go(AssemblyTemplate)
	system_enable_template?: string @go(SystemEnableTemplate)
	post_assembly_template?: string @go(PostAssemblyTemplate)
	entrypoint?: [...string] @go(Entrypoint)
	fallback_entrypoint?: [...string] @go(FallbackEntrypoint)
	management_tool?: string @go(ManagementTool)
	management_command?: {[string]: string} @go(ManagementCommands)
	label_key?:      string             @go(LabelKey)
	service_schema?: #InitServiceSchema @go(ServiceSchema,optional=nillable)

	// Raw is the opaque init body — threaded to the service-render leg (leg 1).
	raw?: bytes @go(Raw,type=RawBody)
}

// #InitConfig is the `init:` build-vocabulary container — the whole init map
// keyed by init-system name (charly/charly.yml's `init:` section). #55 Cluster-B:
// CUE-sourced (completing #72, which CUE-sourced its siblings #DistroConfig /
// #Builder but left InitConfig hand-written) so every charly/plugin file that
// uses ONLY this value type reaches it over spec+proto — the import-purity
// lever. Its detection / resolution methods (DetectCandyInit / ResolveInitSystem
// / ActiveInit / InitNames) are pure Go METHODS CUE cannot express and stay
// hand-written in spec/init_config_methods.go (mirrors #DistroConfig's methods
// in spec/distro_config_methods.go: a method, not a type). This is NOT a wire
// type — it is never marshaled across the host↔plugin boundary; it mirrors the
// authored `init:` config surface, so SDD CUE-sources it exactly as #DistroConfig.
#InitConfig: {
	init: {[string]: #ResolvedInit} @go(Init,type=map[string]*ResolvedInit)
}

// #InitResolveInput is candy/plugin-init's OpResolve config-leg input: the
// opaque init body to resolve into a ResolvedInit.
#InitResolveInput: {
	init!: bytes @go(Init,type=RawBody)
}

// #InitResolveReply wraps the resolved init.
#InitResolveReply: {
	resolved?: #ResolvedInit @go(Resolved,optional=nillable)
}

// #InitResolveRequest is the discriminated OpResolve input for
// candy/plugin-init: exactly one of Render (leg 1 — render one service unit)
// or Config (legs 2–4 — the resolved init envelope) is set.
#InitResolveRequest: {
	render?: #ServiceRenderInput @go(Render,optional=nillable)
	config?: #InitResolveInput   @go(Config,optional=nillable)
}
