// CUE schema for the OCI-label metadata hub (P2B: BoxMetadata → spec, task #60).
// #BoxMetadata is the runtime-relevant config a deploy extracts from an image's OCI
// labels (ExtractMetadata) — CUE-sourced here so the whole deploy resolution reads ONE
// spec-owned type instead of a hand struct in charly core. The charly home + the sub-shape
// homes (labels.go/alias_collect.go/secrets.go) alias onto these (SDD: wire types are
// CUE-sourced; the P8b-followup/P14a precedent). Package-less; concatenated into spec.
//
// R8 (the byte-freeze gate): #BoxMetadata itself is NEVER whole-struct-marshaled — a deploy
// builds it FIELD-BY-FIELD from ~45 individual labels (ExtractMetadata), so ITS tags are
// wire-irrelevant. The R8 anchor is the MARSHALED label sub-shapes below — their json tags
// must reproduce the hand-struct tags byte-for-byte (required `!` → no omitempty, optional
// `?` → omitempty). deploykit.VolumeMount + kit.LabelDescriptionSet relocate here so
// #BoxMetadata's Volume/Description fields resolve (both alias back at their homes).

// #VolumeMount — a resolved named-volume mount (charly-<deploy>-<name> → container path).
// In-memory only (never marshaled) → tags wire-irrelevant. deploykit.VolumeMount aliases this.
#VolumeMount: {
	volume_name?:    string @go(VolumeName)
	container_path?: string @go(ContainerPath)
}

// #LabeledDescription — one origin's plan-shaped self-description (MARSHALED into
// ai.opencharly.description). origin is REQUIRED (json:"origin", no omitempty). kit aliases it.
#LabeledDescription: {
	origin!:      string @go(Origin)
	description?: string @go(Description)
	plan?: [...#Step] @go(Plan)
}

// #LabelDescriptionSet — the three-section (candy/box/deploy) description manifest
// (ai.opencharly.description). kit.LabelDescriptionSet aliases this.
#LabelDescriptionSet: {
	candy?: [...#LabeledDescription] @go(Candy)
	box?: [...#LabeledDescription] @go(Box)
	deploy?: [...#LabeledDescription] @go(Deploy)
}

// #LabelVolumeEntry — a volume in the ai.opencharly.volume label (name+path both required).
#LabelVolumeEntry: {
	name!: string @go(Name)
	path!: string @go(Path)
}

// #LabelRouteEntry — a traefik route in ai.opencharly.route (host+port both required).
#LabelRouteEntry: {
	host!: string @go(Host)
	port!: int    @go(Port,type=int)
}

// #CapabilityService — the full structured service spec baked into ai.opencharly.service.
// name required; all else omitempty. Mirrors the hand struct byte-for-byte.
#CapabilityService: {
	name!:         string @go(Name)
	scope?:        string @go(Scope)
	enable?:       bool   @go(Enable)
	use_packaged?: string @go(UsePackaged)
	exec?:         string @go(Exec)
	env?: {[string]: string} @go(Env)
	restart?:           string @go(Restart)
	working_directory?: string @go(WorkingDirectory)
	user?:              string @go(User)
	after?: [...string] @go(After)
	before?: [...string] @go(Before)
	stdout?:        string @go(Stdout)
	stop_timeout?:  string @go(StopTimeout)
	kind?:          string @go(Kind)
	events?:        string @go(Events)
	auto_start?:    bool   @go(AutoStart,optional=nillable)
	start_retries?: int    @go(StartRetries,type=int)
	start_sec?:     int    @go(StartSec,type=int)
	stop_signal?:   string @go(StopSignal)
	exit_code?:     string @go(ExitCode)
	priority?:      int    @go(Priority,type=int)
	init?:          string @go(Init)
	candy?:         string @go(Candy)
}

// #CapabilityInitDef — the deploy-relevant subset of an InitDef (ai.opencharly.init_def).
#CapabilityInitDef: {
	entrypoint?: [...string] @go(Entrypoint)
	fallback_entrypoint?: [...string] @go(FallbackEntrypoint)
	management_tool?: string @go(ManagementTool)
	management_commands?: {[string]: string} @go(ManagementCommands)
}

// #LabelDataEntry — a data mapping in ai.opencharly.data (volume/staging/candy required).
#LabelDataEntry: {
	volume!:  string @go(Volume)
	staging!: string @go(Staging)
	candy!:   string @go(Candy)
	dest?:    string @go(Dest)
}

// #ShellEntry — one origin's shell-init contribution (in ai.opencharly.shell). origin required.
#ShellEntry: {
	origin!:  string     @go(Origin)
	id?:      string     @go(ID)
	generic?: #ShellSpec @go(Generic,optional=nillable)
	by_shell?: {[string]: #ShellSpec} @go(ByShell,type=map[string]*ShellSpec)
	priority?: int @go(Priority,type=int)
}

// #LabelShellSet — the three-section shell-init manifest (ai.opencharly.shell).
#LabelShellSet: {
	candy?: [...#ShellEntry] @go(Candy)
	box?: [...#ShellEntry] @go(Box)
	deploy?: [...#ShellEntry] @go(Deploy)
}

// #CollectedAlias — a command alias baked into ai.opencharly.alias (name+command required).
#CollectedAlias: {
	name!:    string @go(Name)
	command!: string @go(Command)
}

// #LabelSecretEntry — a secret requirement in ai.opencharly.secret (name+target required).
#LabelSecretEntry: {
	name!:   string @go(Name)
	target!: string @go(Target)
	env?:    string @go(Env)
}

// #LabelSkillEntry — one skill baked into ai.opencharly.skill (the migration of the vestigial
// ai.opencharly.skill doc-pointer URL into the actual definitions — an image now carries its
// composed candies' skills, readable directly via ExtractMetadata / `charly box labels` /
// `charly fleet from-box`, with no external fetch). family+name+content required (an entry
// without content is not a readable skill); the JSON label wire form of the authoring #Skill
// (CollectSkills projects authoring → wire).
#LabelSkillEntry: {
	family!:      string @go(Family)
	name!:        string @go(Name)
	owner?:       string @go(Owner)
	description?: string @go(Description)
	content!:     string @go(Content)
	references?: [...#LabelSkillReference] @go(References)
	triggers?:    [...string] @go(Triggers)
	category?:    string @go(Category)
}
#LabelSkillReference: {
	name!:    string @go(Name)
	content!: string @go(Content)
}

// #BoxMetadata — the OCI-label metadata hub. NEVER whole-marshaled (ExtractMetadata builds it
// field-by-field), so its own tags are wire-irrelevant. Deploy-only fields (Tunnel/DNS/
// AcmeEmail/Engine) are fed by MergeDeployOntoMetadata, never baked. PortProto RESHAPED
// map[int]string → map[string]string (the WIRE was always string-keyed; the int-key was an
// ExtractMetadata-internal convenience — resolveProto + ExtractMetadata rewired to match).
#BoxMetadata: {
	box?:      string @go(Box)
	version?:  string @go(Version)
	registry?: string @go(Registry)
	bootc?:    bool   @go(Bootc)
	uid?:      int    @go(UID,type=int)
	gid?:      int    @go(GID,type=int)
	user?:     string @go(User)
	home?:     string @go(Home)
	port?: [...string] @go(Port)
	volume?: [...#VolumeMount] @go(Volume)
	alias?: [...#CollectedAlias] @go(Alias)
	security?: #Security @go(Security)
	network?:  string    @go(Network)
	tunnel?: {...} @go(Tunnel,type=*TunnelYAML)
	dns?:        string @go(DNS)
	acme_email?: string @go(AcmeEmail)
	env?: [...string] @go(Env)
	hook?: #CandyHook @go(Hook,optional=nillable)
	route?: [...#LabelRouteEntry] @go(Route)
	init?:     string             @go(Init)
	init_def?: #CapabilityInitDef @go(InitDef,optional=nillable)
	service?: [...#CapabilityService] @go(Service)
	service_names?: [...string] @go(ServiceNames)
	env_candy?: {[string]: string} @go(EnvCandy)
	path_append?: [...string] @go(PathAppend)
	engine?: string @go(Engine)
	port_proto?: {[string]: string} @go(PortProto)
	port_relay?: [...int] @go(PortRelay,type=[]int)
	skills?: [...#LabelSkillEntry] @go(Skills,type=[]LabelSkillEntry)
	status?: string @go(Status)
	info?:   string @go(Info)
	candy_version?: {[string]: string} @go(CandyVersion)
	secret?: [...#LabelSecretEntry] @go(Secret)
	distro?: [...string] @go(Distro)
	build_format?: [...string] @go(BuildFormat)
	builder?: {[string]: string} @go(Builder)
	build?: [...string] @go(Build)
	data_entries?: [...#LabelDataEntry] @go(DataEntries)
	data_image?: bool @go(DataImage)
	env_provide?: {[string]: string} @go(EnvProvide)
	env_require?: [...#EnvDependency] @go(EnvRequire)
	env_accept?: [...#EnvDependency] @go(EnvAccept)
	secret_accept?: [...#EnvDependency] @go(SecretAccept)
	secret_require?: [...#EnvDependency] @go(SecretRequire)
	mcp_provide?: [...#CandyMCPProvide] @go(MCPProvide)
	agent_provide?: [...#AgentRuntimeCapability] @go(AgentProvide)
	terminal_profiles?: {[string]: #TerminalProfile} @go(TerminalProfiles,type=map[string]TerminalProfile)
	mcp_require?: [...#EnvDependency] @go(MCPRequire)
	mcp_accept?: [...#EnvDependency] @go(MCPAccept)
	description?: #LabelDescriptionSet @go(Description,optional=nillable)
	shell?:       #LabelShellSet       @go(Shell,optional=nillable)
	check_level?: string               @go(CheckLevel)
}

// #BakedLabelSet — the BUILD-side wire-form carrier for the render's OCI-label emission
// (#67, the build_resolve RENDER-leg death). The former charly/generate.go writeLabels body
// split at the data/format boundary: the HOST gather (buildBakedMetadata) reads the live
// *Candy/*Config graph + the Collect* aggregators and produces a *spec.BakedLabelSet holding
// EXACTLY the data writeLabels emits, in WIRE form; the deploykit FORMATTER (WriteLabels)
// emits the LABEL lines from it byte-for-byte WITHOUT the live graph. It is distinct from
// #BoxMetadata (the DEPLOY-side hub ExtractMetadata builds field-by-field from labels): a
// few fields differ in shape between the bake wire form and the deploy read form — env is a
// map here (the label bakes a JSON object) but []string KEY=VALUE pairs in #BoxMetadata, and
// volume is #LabelVolumeEntry here (name+path) but #VolumeMount in #BoxMetadata — so the
// build carrier is its own wire-faithful type, never the deploy hub. It rides the envelope on
// #ResolvedBoxView.baked_metadata (build-render projection only) and re-attaches via
// NewSpecResolvedBox. MARSHALED (crosses core→plugin), so its tags are wire-relevant; the
// sub-shapes are the same CUE-sourced label types writeLabels emitted.
#BakedLabelSet: {
	// always-present scalars (the formatter emits these unconditionally as LABEL %q).
	version!: string @go(Version)
	box!:     string @go(Box)
	uid!:     int    @go(UID,type=int)
	gid!:     int    @go(GID,type=int)
	user!:    string @go(User)
	home!:    string @go(Home)
	// conditional scalars (the formatter omits these when empty).
	registry?: string @go(Registry)
	bootc?:    bool   @go(Bootc)
	oci_labels?: {[string]: string} @go(OCILabels) // candy-declared arbitrary labels (emitted sorted)
	network?: string @go(Network)
	// platform identity + builder-pool coordination (writeJSONLabel).
	distro?: [...string] @go(Distro)
	build_format?: [...string] @go(BuildFormat)
	builder?: {[string]: string} @go(Builder)
	build?: [...string] @go(Build) // builder-provide capabilities
	// JSON labels (omitted when empty). env is the image-level env MAP (the label wire form).
	port?: [...string] @go(Port)
	port_proto?: {[string]: string} @go(PortProto)
	volume?: [...#LabelVolumeEntry] @go(Volume)
	alias?: [...#CollectedAlias] @go(Alias)
	security?: #Security @go(Security)
	env?: {[string]: string} @go(Env)
	hook?:        #CandyHook           @go(Hook,optional=nillable)
	description?: #LabelDescriptionSet @go(Description,optional=nillable)
	shell?:       #LabelShellSet       @go(Shell,optional=nillable)
	// init system: active name + the deploy-relevant init def + the DYNAMIC label key the
	// per-init service-name list emits under (writeJSONLabel(b, labelInitDef.LabelKey, names)).
	init?:           string             @go(Init)
	init_def?:       #CapabilityInitDef @go(InitDef,optional=nillable)
	init_label_key?: string             @go(InitLabelKey)
	service_names?: [...string] @go(ServiceNames)
	service?: [...#CapabilityService] @go(Service)
	port_relay?: [...int] @go(PortRelay,type=[]int)
	skills?: [...#LabelSkillEntry] @go(Skills,type=[]LabelSkillEntry)
	secret?: [...#LabelSecretEntry] @go(Secret)
	env_provide?: {[string]: string} @go(EnvProvide)
	env_require?: [...#EnvDependency] @go(EnvRequire)
	env_accept?: [...#EnvDependency] @go(EnvAccept)
	secret_require?: [...#EnvDependency] @go(SecretRequire)
	secret_accept?: [...#EnvDependency] @go(SecretAccept)
	mcp_provide?: [...#CandyMCPProvide] @go(MCPProvide)
	agent_provide?: [...#AgentRuntimeCapability] @go(AgentProvide)
	terminal_profiles?: {[string]: #TerminalProfile} @go(TerminalProfiles,type=map[string]TerminalProfile)
	mcp_require?: [...#EnvDependency] @go(MCPRequire)
	mcp_accept?: [...#EnvDependency] @go(MCPAccept)
	route?: [...#LabelRouteEntry] @go(Route)
	env_candy?: {[string]: string} @go(EnvCandy)
	path_append?: [...string] @go(PathAppend)
	skills?:      [...#LabelSkillEntry] @go(Skills,type=[]LabelSkillEntry)
	status?:      string @go(Status)
	check_level?: string @go(CheckLevel)
	info?:        string @go(Info)
	candy_version?: {[string]: string} @go(CandyVersion)
	data_entries?: [...#LabelDataEntry] @go(DataEntries)
	data_image?: bool @go(DataImage)
}
