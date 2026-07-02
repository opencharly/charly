// CUE schema for the `candy` kind. #Candy validates ONE candy entity (the value
// under `candy:` in a kind-keyed candy charly.yml). CLOSED (an unknown top-level
// key is a typo) — this replaces the Go UnmarshalYAML known-field typo guard
// (layers.go candyYAMLKnownFields). Every key in candyYAMLKnownFields is modeled.
// Shared defs (#Step/#Security/#Shell/#CalVer/#EntityRef/#CandyRef/#PackageItem/
// #DistroPackages) come from _common.cue. Source of truth: charly/layers.go
// CandyYAML + its sub-types (PortSpec, VolumeYAML, AliasYAML, ExtractYAML,
// DataYAML, RouteYAML, EnvDependency, MCPServerYAML, SecretYAML, HooksConfig,
// CandyArtifact, CandyCapabilities, ApkPackageSpec, LocalPkgMap, ServiceEntry).

#Candy: {
	// --- identity (required: ADE mandates version+name+description+plan) ---
	version:     #CalVer
	name?:       #EntityRef
	description: string & !=""
	plan?: [...#Step]

	// --- maturity / engine ---
	status?: *"testing" | "working" | "broken"
	engine?: "docker" | "podman"
	// `from:` is NOT a candy field — EDGE-INHERIT cutover D: a candy: node carrying
	// `base:` or `from:` is a full IMAGE (#Box, the former box:), routed there by the
	// loader; a LAYER fragment has neither. So `from:` lives only on #Box.
	reboot?: bool

	// --- composition ---
	candy?: [...#CandyRef]
	require?: [...#CandyRef]
	// bake_plugin — the OUT-OF-TREE plugin candies whose pre-built provider binary
	// this candy BAKES into every composing image at /usr/lib/charly/plugins/, so a
	// DEPLOYED container (no candy source, no go toolchain) can run an external plugin
	// its in-container charly needs at runtime — e.g. charly-mcp bakes plugin-mcp so
	// `charly mcp serve` resolves the external `mcp` command in-container. The S0
	// baked-plugin BUILD-side seam, the deploy-time counterpart of resolvePluginBinary's
	// bakedPluginBinary fallback (plugin_loader.go). See generate.go emitBakedPlugins.
	bake_plugin?: [...#CandyRef] @go(BakePlugin)
	requires_capability?: [...(string & !="")] @go(RequiresCapability)
	capability?: #CandyCapability @go(Capability,optional=nillable)

	// plugin — declaring this block makes the candy a PLUGIN: it provides
	// reserved-word Providers (built-in OR out-of-tree). The candy is otherwise
	// authored, validated, built, deployed, and checked like any candy (R3 — one
	// authoring surface). See provider.go / plugin_loader.go.
	plugin?: #Plugin @go(Plugin,optional=nillable)

	// external_builder — the reserved word of an EXTERNAL builder plugin
	// (`builder:<word>`, an out-of-tree grpcProvider) this candy SELECTS to produce
	// a multi-stage build artifact. At image build the generator resolves the word,
	// Invokes the provider's OpResolve, and splices the returned BuilderResolveReply
	// (the FROM…AS stage pre-main-FROM + the COPY --from artifacts post-main-FROM).
	// The build-time BUILDER leg — the counterpart of a `run:` step's `plugin:` verb
	// (the STEP leg). A builtin builder (pixi/cargo/npm/aur) is selected by detection
	// files, NOT this field. See generate.go emitExternalBuilderStages.
	external_builder?: string & !="" @go(ExternalBuilder)

	// --- runtime env / local vars / PATH ---
	// env forbids PATH (validate.go: use path_append instead). Values are
	// Go-coerced scalars (#StrVal) — an unquoted `PORT: 8080` is a string. The Go
	// field is map[string]string (yaml.v3 coerces the scalar), so pin the shape
	// (gengotypes degrades the #StrVal value to `any`).
	env?: {PATH?: _|_, [string]: #StrVal} @go(Env,type=map[string]string)
	// var: Go field is `Vars map[string]string`; gengotypes degrades the
	// pattern-keyed map to an empty struct, so pin name + shape.
	var?: {[=~"^[A-Z_][A-Z0-9_]*$"]: #StrVal} @go(Vars,type=map[string]string)
	path_append?: [...(string & !="")] @go(PathAppend)

	// --- package surface (Calamares-aligned) ---
	package?: [...#PackageItem]
	distro?: {[string]: #DistroPackages} @go(Distro,type=map[string]*DistroPackages)
	apk?: [...#CandyApk] @go(Apk,type=[]ApkPackageSpec)
	// localpkg maps a native package FORMAT to a bundled source dir; a scalar
	// form is rejected by Go. Closed to the three known formats.
	// localpkg Go field is `LocalPkg map[string]string` (a per-format → source-dir
	// map); pin name + shape (gengotypes would emit an inline struct named
	// `Localpkg`).
	localpkg?: {
		pac?: string & !=""
		rpm?: string & !=""
		deb?: string & !=""
	} @go(LocalPkg,type=map[string]string)

	// --- networking / routing ---
	// PortSpec: a plain int OR a "proto:port" string (proto ∈ http/https/tcp/…);
	// the Go normalizer canonicalizes either form to the {port, protocol}
	// PortSpec struct, so the Go field is []PortSpec (gengotypes degrades the
	// scalar|string disjunction element to `any`).
	port?: [...(int & >0 & <=65535 | string & =~"^[a-z+-]+:[0-9]+$")] @go(Port,type=[]PortSpec)
	port_relay?: [...(int & >0 & <=65535)] @go(PortRelay,type=[]int)
	route?: #CandyRoute @go(Route,optional=nillable)

	// --- services / volumes / aliases / extract / data ---
	service?: [...#CandyService]
	volume?: [...#CandyVolume]
	alias?: [...#CandyAlias]
	extract?: [...#CandyExtract]
	data?: [...#CandyData]

	// --- security / libvirt / hooks ---
	security?: #Security @go(Security,optional=nillable)
	libvirt?: [...(string & !="")]
	hook?: #CandyHook @go(Hook,optional=nillable)

	// --- env/secret/mcp dependency + provides surface ---
	// All six env/secret/mcp dependency lists decode into the ONE Go type
	// []EnvDependency (pinned via @go(...,type=[]EnvDependency)); the per-context
	// CUE refinements (env-var name regex; the charly/ secret-key prefix) only
	// TIGHTEN validation — they don't change the emitted Go type (an inline
	// `#EnvDependency & {…}` would otherwise make gengotypes emit an anonymous
	// struct). mcp_* keep the base shape (an MCP server name is not env-var-
	// constrained).
	env_provide?: #StrMap @go(EnvProvides)
	env_require?: [...(#EnvDependency & {name: string & =~"^[A-Za-z_][A-Za-z0-9_]*$"})] @go(EnvRequire,type=[]EnvDependency)
	env_accept?: [...(#EnvDependency & {name: string & =~"^[A-Za-z_][A-Za-z0-9_]*$"})] @go(EnvAccept,type=[]EnvDependency)
	secret_accept?: [...(#EnvDependency & {name: string & =~"^[A-Za-z_][A-Za-z0-9_]*$", key?: string & =~"^charly/[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9_-]*$"})] @go(SecretAccept,type=[]EnvDependency)
	secret_require?: [...(#EnvDependency & {name: string & =~"^[A-Za-z_][A-Za-z0-9_]*$", key?: string & =~"^charly/[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9_-]*$"})] @go(SecretRequire,type=[]EnvDependency)
	mcp_provide?: [...#CandyMCPProvide] @go(MCPProvide)
	mcp_require?: [...#EnvDependency] @go(MCPRequire)
	mcp_accept?: [...#EnvDependency] @go(MCPAccept)
	secret?: [...#CandySecret] @go(SecretYAML)

	// --- shell init (same shape as box shell) ---
	shell?: #Shell @go(Shell,optional=nillable)

	// --- operator-facing artifacts ---
	artifact?: [...#CandyArtifact]
}

// ---------------------------------------------------------------------------
// Candy sub-shapes (#Candy-prefixed to avoid cross-kind collisions). Each CLOSED.
// ---------------------------------------------------------------------------

// ServiceEntry (service_render.go). use_packaged XOR exec is a Go cross-field
// rule (mixed-entry polymorphism allows the SAME name twice: one packaged form,
// one exec form) — neither is required at the CUE layer.
#CandyService: {
	name:               string & !=""
	use_packaged?:      string & !="" @go(UsePackaged)
	exec?:              string & !=""
	// distro restricts this entry to the named distros — a bare distro name
	// ("debian") or a versioned tag ("debian:13"). Empty = every distro (the
	// backward-compatible default). The service analogue of a check step's
	// exclude_distros: — it lets ONE candy carry per-distro-DIVERGENT packaged
	// units / exec daemons (the modular virtqemud.socket + virtnetworkd.socket
	// on Fedora/Arch vs the monolithic libvirtd.socket on Debian/Ubuntu, whose
	// libvirt is built without the split daemons) WITHOUT a <name>-host sibling
	// candy (CLAUDE.md "Init-system polymorphism"; R3). Filtered at render time
	// against the target distro tag chain (compileServiceSteps + generate.go).
	distro?: [...(string & !="")]
	env?:               #StrMap
	restart?:           "no" | "on-failure" | "always" | "unless-stopped"
	working_directory?: string & !="" @go(WorkingDirectory)
	user?:              string & !=""
	after?: [...(string & !="")]
	before?: [...(string & !="")]
	wanted_by?: [...(string & !="")] @go(WantedBy)
	stdout?:       string & !=""         // "journal" | "none" | "file:<path>"
	stop_timeout?: (string & !="") | int @go(StopTimeout,type=string) // "20s" or an unquoted 20 (Go-coerced; Go field is string)
	scope?:        "system" | "user"
	enable?:       bool
	overrides?:    #CandyServiceOverrides @go(Overrides,optional=nillable)
	kind?:         "program" | "eventlistener"
	event?:        string & !="" @go(Events)
	auto_start?:   bool          @go(AutoStart,type=*bool)
	start_retry?:  int & >=0     @go(StartRetries,type=int)
	start_sec?:    int & >=0     @go(StartSecs,type=int)
	stop_signal?:  string & !="" @go(StopSignal)
	exit_code?:    string & !="" @go(ExitCode)
	priority?:     int           @go(,type=int)
}
#CandyServiceOverrides: {
	env?: #StrMap
	after?: [...(string & !="")]
	exec?: string & !=""
}

// VolumeYAML — name is lowercase-hyphen (validateVolume); path may be
// home-relative (~/…) or absolute, so not anchored to /.
#CandyVolume: {
	name: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	path: string & !=""
}

// AliasYAML — for a CANDY alias `command` is REQUIRED (validateAliases); the
// box-level #BoxAlias keeps it optional (defaults to name).
#CandyAlias: {
	name:    string & !=""
	command: string & !=""
}

// ExtractYAML — copy a path out of another OCI image into this one. source is
// an image ref; path (in the source image) and dest (in this image) are
// absolute (validateCandyContents).
#CandyExtract: {
	source: string & !=""
	path:   string & =~"^/"
	dest:   string & =~"^/"
}

// DataYAML — stage candy-dir data into a volume at build, provision at deploy.
#CandyData: {
	src:    string & !=""
	volume: string & !=""
	dest?:  string & !=""
}

// EnvDependency (layers.go) — ONE Go type reused for env_require/env_accept,
// secret_accept/secret_require, AND mcp_require/mcp_accept. The hand struct is a
// single SUPERSET {name, description, default, key}; the three former CUE defs
// (#CandyEnvDep/#CandySecretDep/#CandyMCPDep) were distinct only in their
// per-context name regex (env-var name vs mcp-server name) and the secret-only
// `key` override — all of which Go validate.go enforces. gengotypes cannot emit
// THREE shapes for ONE Go type, and a faithful drop-in for []EnvDependency
// requires every usage to carry all four fields (mcp_require's former 2-field
// def is missing default/key), so the three collapse to ONE shared
// #EnvDependency (R3 — one shared abstraction; per-context strictness stays in
// Go).
#EnvDependency: {
	name:        string & !=""
	description: string & !=""
	default?:    string
	key?:        string & !=""
}

// MCPServerYAML — mcp_provide entry exposed to peer containers.
#CandyMCPProvide: {
	name:       string & !=""
	url:        string & !="" @go(URL)
	transport?: *"http" | "sse"
}

// SecretYAML — a candy-owned secret (target defaults to /run/secrets/<name>).
#CandySecret: {
	name:    string & !=""
	target?: string & !=""
	env?:    string & !=""
}

// HooksConfig — lifecycle hook scripts.
#CandyHook: {
	post_enable?: string & !="" @go(PostEnable)
	pre_remove?:  string & !="" @go(PreRemove)
}

// CandyArtifact — a file the candy publishes back to the operator post-setup.
#CandyArtifact: {
	name:         string & !=""
	path:         string & !=""
	retrieve_to:  string & !="" @go(RetrieveTo)
	mode?:        string & =~"^0[0-7]{3,4}$"
	optional?:    bool
	wait_second?: int & >=0 @go(WaitSeconds,type=int)
	rewrite?: [...#CandyArtifactRewrite]
}
#CandyArtifactRewrite: {
	find:     string & !=""
	replace?: string
}

// CandyCapabilities — image-level facts a candy contributes (aggregated at
// resolve time). oci_label is a genuine open string→string passthrough.
#CandyCapability: {
	preserve_user?:         bool          @go(PreserveUser)
	needs_root_after_init?: bool          @go(NeedsRootAfterInit)
	init_system_hint?:      string & !="" @go(InitSystemHint)
	data_only?:             bool          @go(DataOnly)
	oci_label?: {[string]: string} @go(OCILabels)
}

// ApkPackageSpec — one Android app install. package (apkeep by id) XOR apk
// (committed local APK path) — exactly one.
#CandyApk: {
									package?:     string & !=""
									apk?:         string & !=""
									source?:      *"apk-pure" | "google-play" | "f-droid" | "huawei-app-gallery"
									arch?:        string & !=""
									app_version?: string & !=""
									// exactly one of package/apk (disjunction keeps it CLOSED; matchN would open it).
} & ({package!: _, apk?: _|_} | {apk!: _, package?: _|_}) @go(-) // gengotypes: hand ApkPackageSpec (spec/union_types.go)

// RouteYAML — generic service-route metadata (traefik / tunnel).
#CandyRoute: {
	host: string & !=""
	port: int & >0 & <=65535 @go(,type=int)
}

// #Plugin — the candy's plugin declaration. Its presence makes the candy a
// plugin (Go: charly/checkspec.go via the generated Candy.Plugin field, consumed
// by plugin_loader.go). CLOSED.
#Plugin: close({
	// providers: the "<class>:<word>" reserved-word capabilities this plugin
	// serves (e.g. "verb:exampleprobe", "kind:my-thing"). Each is registered into
	// providerRegistry — built-in (init()) or out-of-tree (gRPC).
	providers: [...#PluginCapability]
	// source: "builtin" (Go compiled into the charly binary, init()-registered) OR
	// a git ref (github.com/org/repo[/sub][@tag]) fetched via the @github resolver +
	// built into a provider binary. Default builtin.
	source: *"builtin" | (string & =~"^github\\.com/[^/]+/[^/]+(/.+)?$")
})

// #PluginCapability — a "<class>:<word>" capability string. class ∈ the closed
// ProviderClass set; word is lowercase-hyphenated.
#PluginCapability: string & =~"^(kind|deploy|verb|step|build|builder):[a-z0-9][a-z0-9-]*$"
