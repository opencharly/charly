// CUE schema for the `candy` kind. #Candy validates ONE candy entity (the value
// under `candy:` in a kind-keyed candy charly.yml). CLOSED (an unknown top-level
// key is a typo) — this replaces the Go UnmarshalYAML known-field typo guard
// (sdk/loaderkit candyKnownFields, relocated from charly/layers.go in K-wave 2 cone R1 A2).
// Every key in candyKnownFields is modeled.
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
	// engine — a candy's required run engine; per-image resolution walks the
	// candy chain (engine.go ResolveBoxEngine) and cross-candy conflicts are a
	// validate error. RETAINED by the schema-compaction consumer audit.
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
	// packaging — the native-package metadata for this candy: the SINGLE source
	// of truth the `charly generate-packages` plugin (sdk/packagekit) reads to
	// build distro packages (deb/rpm/apk/archlinux/ipk/msix). Any candy may
	// declare it (kind-blind); the charly candy's charly.yml carries the real
	// section. Replaces the old `localpkg:` source-dir map — the plugin builds
	// from the released binary + plugins, not from a bundled source tree.
	packaging?: #Packaging @go(Packaging,optional=nillable)

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

	// --- security / hooks ---
	// (the candy-level `libvirt:` raw-XML-snippet field was removed — zero live Go
	// consumers, retired alongside the box-level libvirt hard-cutover; migrated
	// away by candy/plugin-migrate's stripCandyLibvirtField. The paired `kind: vm`
	// entity's OWN `libvirt: {...}` domain config, sdk/schema/vm.cue #Vm.libvirt,
	// is unrelated and unaffected.)
	security?: #Security @go(Security,optional=nillable)
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
	agent_provide?: [...#AgentRuntimeCapability] @go(AgentProvide)
	// terminal_profile keeps the authored-key convention: every collection-
	// holding authored key in this block is singular (env_provide, mcp_provide,
	// agent_provide, …). The baked wire/label shape is the PLURAL
	// `terminal_profiles` (schema/boxmetadata.cue; spec.LabelTerminalProfiles
	// "ai.opencharly.terminal_profiles") — a key burned into built images, so
	// the singular-authored ↔ plural-wire mapping is deliberate and stable,
	// not a typo to "align" (renaming either side is a format change).
	terminal_profile?: {[string]: #TerminalProfile} @go(TerminalProfiles,type=map[string]TerminalProfile)
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

// #Packaging — the native-package metadata section (candy `packaging:`): the
// common fields (name/description/maintainer/…) + a `variants:` map (named
// plugin-set variants → `charly-<variant>` packages) + a per-format map
// (`formats.<fmt>`: deps + the format's default variant). CLOSED. The per-format
// keys use the nFPM format names (deb/rpm/apk/archlinux/ipk/msix).
#Packaging: {
	name:        string & !=""
	description: string & !=""
	maintainer:  string & !=""
	homepage?:   string & !=""
	license?:    string & !=""
	section?:    string & !=""
	priority?:   string & !=""
	// variants — the named plugin-set variants. The format's `default_variant`
	// (or the variant named "default" when unset) packages as the plain `charly`
	// package; every other named variant packages as `charly-<variant>`.
	variants?: {[string]: #PackagingVariant} @go(Variants,type=map[string]*PackagingVariant)
	// formats — per-format (nFPM name) dependency + default-variant metadata.
	formats?: {[string]: #PackagingFormat} @go(Formats,type=map[string]*PackagingFormat)
}

// #PackagingVariant — one named plugin-set variant.
#PackagingVariant: {
	description: string & !=""
	// plugins — the default-plugin words this variant ships at
	// /usr/lib/charly/plugins/ (a subset of the released plugin tarball).
	plugins: [...(string & !="")]
}

// #PackagingFormat — per-format (nFPM name) packaging metadata.
#PackagingFormat: {
	depends?:    [...(string & !="")]
	// optdepends — the Arch `optdepend = <pkg>: <desc>` map (nFPM does not emit
	// optdepends; sdk/packagekit post-processes the .pkg.tar.zst to inject them).
	optdepends?: {[string]: string} @go(OptDepends,type=map[string]string)
	recommends?: [...(string & !="")]
	suggests?:   [...(string & !="")]
	// default_variant — the variant name packaged as the plain `charly` package
	// for this format (defaults to "default" when unset).
	default_variant?: string & !="" @go(DefaultVariant)
	// publisher — the msix Publisher display name (msix-specific).
	publisher?: string & !="" @go(Publisher)
	// properties — the msix Properties (msix-specific).
	properties?: {[string]: string} @go(Properties,type=map[string]string)
}

// ServiceEntry (spec). use_packaged XOR exec is a Go cross-field
// rule (mixed-entry polymorphism allows the SAME name twice: one packaged form,
// one exec form) — neither is required at the CUE layer.
#CandyService: {
	name:          string & !=""
	use_packaged?: string & !="" @go(UsePackaged)
	exec?:         string & !=""
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
	// exec_start_pre / exec_start_post — ordered hook commands run around a CUSTOM
	// (exec:) entry's own start, rendered by the init system's service_template
	// (systemd: ExecStartPre= / ExecStartPost= lines, emitted in authored order).
	// They exist for guards belonging to the SERVICE's lifecycle rather than to the
	// install timeline: a plan step runs ONCE, at deploy, whereas a pre-start hook
	// re-asserts a host precondition on EVERY start (a restart included), and a
	// post-start hook is the only place a guard runs ALONGSIDE the daemon it
	// guards — the sole vantage from which that daemon's own socket/API is
	// reachable. Both are init-supervised, so a failure surfaces in the unit's
	// status instead of being swallowed; prefix a command with "-" for systemd's
	// ignore-failure semantics.
	// Canonical consumer: candy/k3s-server (the cgroup cpuset-delegation assertion
	// and the CRD-establishment wedge heal).
	exec_start_pre?: [...(string & !="")] @go(ExecStartPre)
	exec_start_post?: [...(string & !="")] @go(ExecStartPost)
	overrides?: #CandyServiceOverrides @go(Overrides,optional=nillable)
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
	// register: an optional NAME-BLIND post-retrieve processing hint (e.g. "kubeconfig") —
	// what to do with the retrieved artifact beyond the generic retrieve/rewrite pipeline,
	// dispatched by a host-side registry keyed on this word (retrieveArtifactsAndK3s /
	// artifactRegisterHandlers). Replaces a hardcoded candy-NAME check
	// (deployHasCandy(candyList, "k3s-server")) with a declaration the candy itself carries,
	// so a future candy needing the same registration need not be named "k3s-server".
	register?: string & !=""
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
	// primary: verb word → the input field its scalar sugar shorthand targets
	// (`file: /x` → plugin_input: {<primary>: "/x"}). Declared in the MANIFEST so
	// the byte-gated prescan registers it BEFORE the out-of-process provider
	// connects (the parse-time desugar needs it pre-parse); the served
	// ProvidedCapability.Primary mirrors it for the compiled-in placement.
	primary?: {[string]: string & !=""}
})

// #PluginCapability — a "<class>:<word>" capability string. class ∈ the closed
// ProviderClass set; word is lowercase-hyphenated.
#PluginCapability: string & =~"^(kind|deploy|verb|step|build|builder|command|loader|refs|agent-runtime|terminal):[a-z0-9][a-z0-9_-]*$"
