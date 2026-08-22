package spec

// candy_reader.go — CandyReader, the read-only view of a runtime candy that the
// deploy-plan compiler (deploykit.BuildDeployPlan) and the build-render engine consume. It lives
// in spec (the shared contract home, alongside the CUE-generated wire envelopes) rather than
// sdk/deploykit so an import-clean charly file can reach it WITHOUT crossing the mechanism-kit
// import-purity line — matches the DocParser/ProjectWalker precedent in loader_seam.go. The
// concrete runtime Candy (charly core) implements it; deploykit's specCandyAdapter implements it
// from the CUE-generated CandyModel/CandyView wire views. Every referenced type below is already
// spec-native (Step/EnvConfig/EnvDependency/Op/Security/CandyHook/CandyService/Shell/…) or promoted
// here alongside the interface (CandyRefEntry, below) — a spec-hosted contract never needs an sdk
// mechanism-kit import.
type CandyReader interface {
	GetName() string
	GetSourceDir() string
	GetVersion() string
	Vars() map[string]string
	PlanSteps() []Step
	Reboot() bool
	Apk() []ApkPackageSpec
	EnvConfig() (*EnvConfig, error)
	Service() []CandyService
	Shell() *Shell
	TopPackages() []string
	FormatSection(name string) *PackageSection
	// Deprecated: the localpkg source-build map is being replaced by the
	// `packaging:` section (Packaging()). Kept until the sdk's localpkg
	// replacement (Phase 0b) migrates the consumers off it.
	LocalPkg(format string) string
	// Packaging returns the candy's `packaging:` section (the single source of
	// truth for distro package metadata + variants). Nil when the candy declares
	// none — the deploy-plan compiler then emits no LocalPkgInstallStep for it.
	Packaging() *Packaging
	TagSection(tag string) *TagPkgConfig
	HasFile(filename string) bool

	// P8 render-delta accessors (build-mode graph/render surface). Field-backed
	// members use Get* to avoid a field/method name collision on the charly *Candy
	// implementer; the three already-methods keep their names.
	GetIncludedCandy() []CandyRefEntry // candy: composition refs (splicing)
	GetRequire() []CandyRefEntry       // require: deps (ordering + resolution)
	// GetBakePlugin (W9): bake_plugin: out-of-tree plugin candies baked into composing images
	// (generate.go emitBakedPlugins). Same bare-string-wrapped-as-CandyRefEntry shape as
	// GetRequire/GetIncludedCandy above (CandyModel.BakePlugin is the FINAL bare-string wire
	// form; the adapter re-wraps it so callers keep using .Bare() uniformly).
	GetBakePlugin() []CandyRefEntry
	HasContent() bool
	HasInstallFiles() bool // at least one install file (drives pixi-bound detection)
	PixiManifest() string
	GetHasPackageJson() bool
	GetHasCargoToml() bool
	GetExternalBuilder() string // external_builder: word (the out-of-tree builder plugin this candy selects)
	HasFormatPackages() bool
	GetRemote() bool // true if the candy came from a remote repo
	HasExtract() bool
	Extract() []CandyExtract
	HasData() bool
	Data() []CandyData
	GetHasPixiLock() bool
	GetRepoPath() string
	GetSubPathPrefix() string
	HasEnv() bool
	HasRoute() bool
	Route() (*RouteConfig, error)

	// P8 init-cluster accessors (emitInitAssembly/emitInitFragmentStages/
	// generateInitFragments). RelayPorts wraps the field-backed PortRelayPorts to
	// avoid a field/method name collision on the charly *Candy implementer.
	HasInit(initName string) bool // this candy contributes to the named init system
	ServiceFiles() []string       // file_copy-model service unit paths (globbed)
	RelayPorts() []int            // port_relay: ports (init-agnostic)

	// P8 writeCandySteps accessors.
	HasTasks() bool // the candy has any tasks: (runOps non-empty)
	RunOps() []Op   // the candy's plan lowered to build-mode run ops

	// OCI-label-collector surface (CollectSecurity/CollectHooks/layer_secrets):
	// per-candy security/hooks + the six env/secret/mcp dependency lists.
	Security() *Security
	Hooks() *CandyHook
	EnvRequire() []EnvDependency
	EnvAccept() []EnvDependency
	SecretRequire() []EnvDependency
	SecretAccept() []EnvDependency
	MCPRequire() []EnvDependency
	MCPAccept() []EnvDependency

	// W9 mass-edit interface-completeness fill: the remaining accessors the 42-file
	// repoint needs (CollectBoxAlias/CollectBoxVolume/ports.go/layer_capabilities.go/…)
	// — every one of these already exists on the charly *Candy implementer (layers.go);
	// this is a pure exposure widening, no new *Candy behavior. (Libvirt()/HasLibvirt()
	// were added here for this same fill, to serve charly/libvirt.go's
	// CollectLibvirtSnippets — that caller was dead code [box-level `libvirt:` was
	// removed in the VM hard-cutover] and was deleted, and the candy-level `libvirt:`
	// field itself was removed from the schema in the SAME cutover [candy.cue /
	// candymodel.cue, migrated by candy/plugin-migrate's stripCandyLibvirtField] — so
	// these two accessors are gone too, not merely deprecated.)
	// Consumer-set audited: HasAnyInit/HasAnyPackages/HasApk/HasService/HasTagPackages
	// were considered and DROPPED — zero external caller outside layers.go itself (they
	// back OTHER *Candy-internal composition, e.g. HasContent/HasInstallFiles, which
	// stays a host-precomputed field per the #67 predicate-carrying pattern above).
	Alias() []CandyAlias
	HasAliases() bool
	Volume() []CandyVolume
	HasVolumes() bool
	Artifact() []CandyArtifact
	Capabilities() *CandyCapability
	RequiresCapabilities() []string
	Engine() string
	EnvProvides() map[string]string
	MCPProvide() []CandyMCPProvide
	Secret() []CandySecret
	Port() ([]string, error)
	PortSpecs() []PortSpec
	// Deprecated: the localpkg source-build map is being replaced by the
	// `packaging:` section (Packaging()). Kept until the sdk's localpkg
	// replacement (Phase 0b) migrates the consumers off it.
	LocalPkgFormats() []string
	HasEnvAccepts() bool
	HasEnvProvides() bool
	HasEnvRequires() bool
	HasMCPAccepts() bool
	HasMCPProvides() bool
	HasMCPRequires() bool
	HasPorts() bool
	HasSecretAccepts() bool
	HasSecretRequires() bool

	// W9 pipeline-retype fill: the candy's OWN `plugin:` declaration (the data-read half of external
	// verb/step-word recognition, plus findCommandPluginCandy) — the provider REGISTRATION itself
	// stays core-M (it touches the providerRegistry), but recognizing which capability words a
	// scanned candy declares is a plain data read the interface must expose once *Candy is gone. The
	// validate path reads exactly these three off the resolved-project envelope and hands the
	// capability strings to the host's "validate-word-sets" seam, which does the registration.
	IsPluginCandy() bool
	GetPluginSource() string
	GetPluginProviders() []string

	// W9 pipeline-retype fill: identity-view scalars multiple OCI-label-collector consumers
	// (render_baked_metadata.go's candyStatus/worst-of-status + info-parts walk) need directly —
	// widely-needed enough to earn a real interface method, unlike the FULL identity view
	// (Description/Info/IsPlugin's sibling fields + the FormatSections/TagSections/RunOps maps)
	// resolved_project_host.go alone needs via the RawCandy() escape hatch.
	GetStatus() string
	GetDescription() string

	// W9 federated-control-plane fill: the candy-declared `agent_provide:`/`terminal_profile:`
	// surfaces (56b1a6b) the OCI-label-collector (render_baked_metadata.go's per-box
	// agent/terminal aggregation) needs once *Candy is gone.
	AgentProvide() []AgentRuntimeCapability
	HasAgentProvides() bool
	TerminalProfiles() map[string]TerminalProfile
}
