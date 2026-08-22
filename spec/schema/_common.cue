// Shared CUE definitions referenced by multiple kinds. R3: define each shared
// shape ONCE here, not per-kind. All schema/*.cue files compile into one
// instance (no package clauses), so any kind def can reference these directly.

// Execution context for a plan step.
#Context: ("build" | "deploy" | "runtime") @go(-)

// #OpVerb — the verb DISCRIMINATOR vocabulary: the subset of #Op fields that are
// VERBS (exactly-one-set discriminators), as opposed to the shared modifiers
// (to/mode/content/timeout/…) that decorate them. #Op alone cannot express the
// verb-vs-modifier split (every field is structurally a string/int), so this enum
// is the ONE CUE source naming the verbs — schemagen emits it as spec.OpVerbs
// (the canonical verb set for Op.Kind()'s exactly-one-verb error + the package-main
// VerbCatalog dispatch table, both gated against it). Every name MUST be an #Op
// field (the schemagen gate "OpVerbs ⊆ AuthoringVerbs" proves it) and MUST have a
// VerbCatalog entry (the registry bijection gate proves it). Keep in lockstep with
// the `--- verb discriminators ---` group in #Op.
#OpVerb: ("mkdir" | "copy" | "write" | "link" | "download" | "setcap" | "build" |
	"plugin") @go(-)

// ---------------------------------------------------------------------------
// Plan steps: the unified run/check/agent-run/agent-check/include vocabulary.
// ---------------------------------------------------------------------------

// One flat plan step: exactly ONE intent keyword (run / check / agent-run /
// agent-check / include) carrying prose, plus an inline Op (verb + modifiers).
// Exactly-one-keyword is the discriminated-union idiom — each arm requires its
// keyword and forbids the other four via _|_. Each arm EMBEDS the closed #Op,
// so the Op modifier field set is now CLOSED too (an unknown key is a typo).
// Op verb-exclusivity (Kind()) stays a Go per-entity check; CUE closes the
// field set and types every field. A PLUGIN verb is authored as generic sugar —
// `<word>: <input>` — which the parse-time desugar rewrites into the internal
// plugin/plugin_input form BEFORE this def validates the step, so a plugin
// word never appears here: core #Op carries no per-plugin fields (each
// plugin's input def, served over Describe, validates its own map).
// Every arm also forbids the runtime-derived venue/intent_do (stamped post-decode,
// never authored — see #Op).
#Step:
	{#Op, run: string & !="", check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, check: string & !="", run?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-run": string & !="", run?: _|_, check?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-check": string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, include: string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, venue?: _|_, intent_do?: _|_} @go(-) // gengotypes: hand-written Step (spec/union_types.go) embeds the generated Op

// #Op is the unified operation vocabulary (Go: charly/checkspec.go Op) AFTER
// the parse-time desugar: the builtin install verbs + the genuinely SHARED step
// modifiers + the internal plugin/plugin_input pair every plugin-verb sugar key
// desugars into. CLOSED: an unknown key on the desugared step is a typo (a
// plugin word never reaches this def — the desugar consumed it). Per-verb
// fields live in each plugin's own input def, served over Describe.
#Op: {
	// --- verb discriminators (exactly one set; Go Kind() enforces) ---
	mkdir?:          string
	copy?:           string
	write?:          string
	link?:           string
	download?:       string
	setcap?:         string
	build?:          string
	// plugin — the generic PLUGIN-VERB discriminator, INTERNAL-ONLY: the
	// parse-time desugar rewrites every authored `<word>: <input>` sugar key
	// into this plugin/plugin_input pair, and authoring plugin:/plugin_input:
	// directly in a step is a hard load error (run: charly migrate). The pair
	// stays on #Op because it is the wire/label form (the desugared tree this
	// def validates, the ai.opencharly.description JSON) and the Go dispatch
	// surface (providerRegistry.ResolveVerb). plugin_input is validated by the
	// PLUGIN's own served CUE schema (not base #Op).
	plugin?: string

	// --- shared modifiers ---
	id?:          string @go(ID)
	description?: string
	skip?:        bool
	timeout?:     #Duration
	// command — INTERNAL-ONLY rehydration target (never authored): the `command`
	// plugin verb's INSTALL-EMIT copies plugin_input.command here for emitCmd
	// (build) / renderOpCommand (deploy). Authored `command:` on a step IS the
	// command plugin's sugar key (scalar = the primary shorthand), consumed by
	// the parse-time desugar before this def validates; the wl/libvirt argv
	// moved into those plugins' own input defs.
	command?: string
	context?: [...#Context]
	// `pod:` (per-step container venue) is RETIRED — a step's execution venue is
	// derived ENTIRELY from its position in the fleet tree (flattenFleetVenues
	// → Op.venue, yaml:"-"). Authoring it is a closed-schema rejection (run:
	// charly migrate).
	depends_on?: [...string] @go(DependsOn)
	// plugin_input — generic params for a `plugin:` verb. Opaque to base #Op
	// (accepts any shape); the plugin's own spliced CUE schema validates it.
	plugin_input?: {...} @go(PluginInput,type=map[string]any)

	// --- install/build modifiers ---
	run_as?:  string @go(RunAs)
	to?:      string
	target?:  string
	caps?:    string
	content?: string
	extract?: "tar.gz" | "tar.xz" | "tar.zst" | "zip" | "none" | "sh" | ""
	extract_include?: [...string] @go(ExtractInclude)
	// unless_exists — capability GATE for an install step: when the named path already
	// exists in the image, the step is a no-op. It exists because a plan step has no
	// build-time distro filter (`exclude_distro:` is read by the CHECK runner, never by
	// the emitter), so a candy whose artifact is supplied by the distro package on SOME
	// distros and by an upstream archive on the rest had no declarative way to express
	// "fetch only where the package did not already provide this". Without it the only
	// recourse was to abandon the verb and hand-roll `curl`, which silently forfeits what
	// the verb provides: the content-addressed /tmp/downloads cache, the atomic .part
	// rename, and `cache:` mounts. Detection rather than a distro name, so any distro that
	// later gains the package is picked up with no edit.
	unless_exists?: string & !="" @go(UnlessExists)
	strip_components?: int & >=0 @go(StripComponents,type=int)
	uninstall?: [...string]
	comment?: string
	cache?: [...string]
	env?: #StrMap

	// --- step modifiers ---
	eventually?:      #Duration
	retry_interval?:  #Duration @go(RetryInterval)
	// `on:` (cross-member driver dispatch) is RETIRED — a step that drives a
	// peer/driver is authored as a step CHILD of that member node; its venue is
	// derived from position (flattenFleetVenues → Op.venue). Authoring `on:` is
	// a closed-schema rejection (run: charly migrate).
	tag?: [...string]

	// Origin is populated at collection time (candy:<name>/box:<name>/deploy-*),
	// NEVER authored (Go yaml:"-"), but it TRAVELS in the ai.opencharly.description
	// OCI-label JSON (Go json:"origin") — so the generated spec.Op MUST carry it
	// for a faithful drop-in (do NOT @go(-) it). gengotypes emits
	// `json:"origin,omitempty"`, matching the hand tag.
	origin?: string @go(Origin)

	// venue + intent_do are RUNTIME-DERIVED, never authored: venue is stamped
	// from a step's fleet-tree position (flattenFleetVenues), intent_do from
	// the enclosing Step's keyword (run→act / check→assert / agent→instruct).
	// They are generated onto the Go struct (the check runner persists them on
	// the Op-by-value passed to runOne / EffectiveDo), but the #Step authoring
	// arms forbid them (`venue?: _|_, intent_do?: _|_`) so authoring either is a
	// closed-schema rejection — exactly the contract the retired `pod:`/`on:`
	// fields enforced. yaml:"-" (never read from YAML); json omits them when
	// empty (the bake-time state, so they never leak into the description label).
	venue?:     string @go(Venue)
	intent_do?: string @go(IntentDo)

	// --- file/copy/write SHARED modifier ---
	// `mode` is the SHARED octal-permission modifier: the copy/write install verbs read
	// Op.Mode at deploy (the external local/vm deploy walk via kit.ParseTaskMode),
	// so it STAYS in #Op. The file-EXCLUSIVE fields (file/exists/owner/group_of/filetype/
	// contains/sha256) LEFT #Op — they are read ONLY by the `file` plugin verb and now live
	// in its #FileInput (candy/plugin-file, with the contains-default semantic
	// reproduced via the candy's decodeContainsList). The state-provision migrator MOVES `mode` into a
	// file step's plugin_input while LEAVING it here for copy/write (the shared-companion
	// pattern, like gid between unix_group and user).
	mode?: string & =~"^0[0-7]{3,4}$"

	// exclude_distro — a SHARED step-level skip filter read by the generic runOne for
	// EVERY verb (skip the step when any image distro tag intersects the list), NOT a
	// package-exclusive field, so it STAYS on #Op. The `package`-exclusive fields
	// (installed/version/package_map) MOVED into #PackageInput when `package` extracted.
	exclude_distro?: [...string] @go(ExcludeDistros)

	// `package`/`installed`/`version`/`package_map` are NOT here — `package` is an
	// extracted plugin verb (plugin: package + #PackageInput, candy/plugin-package).
	// It left #OpVerb/spec.OpVerbs, and installed/version/package_map (read ONLY by the
	// package verb off the step Op) MOVED into #PackageInput with it. The shared
	// `exclude_distro` modifier above is NOT package-exclusive and stays on #Op.
	//
	// `service`/`running`/`enabled` are NOT here — `service` is an extracted plugin
	// verb (plugin: service + #ServiceInput, candy/plugin-service). It left
	// #OpVerb/spec.OpVerbs, and `running`/`enabled` (read ONLY by the service verb off
	// the step Op) MOVED into #ServiceInput with it. `running` was reproduced standalone
	// in #ProcessInput when `process` extracted (process reads its own plugin_input, not
	// #Op), so removing it here leaves process untouched.

	// --- shared assertion matchers (SHARED via matchAll: the command/http probe
	// plugins + every live-verb plugin assert exit_status/stdout/stderr off the
	// marshalled step Op, so they STAY in #Op; verb-exclusive request/input
	// fields live in each plugin's input def) ---
	exit_status?: int @go(ExitStatus,type=*int)
	stdout?:      #MatcherList
	stderr?:      #MatcherList

}

// Matcher operators (validMatcherOps, validate_check.go). A matcher is a scalar
// (implicit equals), or a single-operator map; #MatcherList accepts a single
// matcher OR a list. (The base no longer carries a contains-default list def — that
// shape left with the `file` verb's `contains` field and is now reproduced standalone
// in the file plugin's #FileContains, decoded with the substring default via
// decodeContainsList; no base #Op field uses it anymore.)
#MatchOpMap: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}

#Matcher: (string | bool | number | #MatchOpMap) @go(-) // gengotypes: hand Matcher (spec/union_types.go, ported from checkspec.go)

#MatcherList: (#Matcher | [...#Matcher]) @go(-) // gengotypes: hand MatcherList

// ---------------------------------------------------------------------------
// Build-vocabulary shared shapes (distro formats + builders).
// ---------------------------------------------------------------------------

// A BuildKit cache mount. dst is the absolute in-builder cache path; sharing is
// the BuildKit sharing mode; owned renders a uid/gid-owned cache.
#CacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
}

// Three-phase template set: prepare → install → cleanup, each with a container
// (Containerfile) and host (shell) rendering. Template bodies are Go
// text/template — strings, never parsed here.
#PhaseSet: {
	prepare?: #PhaseTemplates @go(Prepare,optional=nillable)
	install?: #PhaseTemplates @go(Install,optional=nillable)
	cleanup?: #PhaseTemplates @go(Cleanup,optional=nillable)
}

#PhaseTemplates: {
	container?: string
	host?:      string
}

// ---------------------------------------------------------------------------
// Cross-kind scalar/ref patterns (R3: one home, referenced everywhere).
// ---------------------------------------------------------------------------

// CalVer schema/entity version: YYYY.DDD.HHMM.
#CalVer: string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$" @go(-)

// A lowercase-hyphenated entity name / cross-ref.
#EntityRef: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$" @go(-)

// A candy ref: bare lowercase-hyphenated name OR a remote @github…:vTAG ref.
#CandyRef: string & =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$" @go(-)

// A host:container port pin (optionally IP-prefixed).
#PortPin: string & =~"^(\\[[0-9a-fA-F:]+\\]:|[0-9]{1,3}(\\.[0-9]{1,3}){3}:)?[0-9]+:[0-9]+$" @go(-)

// A memory/disk size, e.g. "2G", "8192M", "6.5Gi".
#VmSize: string & =~"^[0-9]+(\\.[0-9]+)?([KMGTP]i?B?)?$" @go(-)

// A container-resource size (SecurityConfig shm_size/memory_*).
#Size: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$" @go(-)

// An env entry: KEY=VALUE (value may be empty / contain =).
// A YAML scalar Go decodes into a string (map[string]string / string field):
// yaml.v3 coerces an unquoted int/bool/float to its literal text, so an
// idiomatic `PORT: 8080` is a valid string value. #StrMap is the matching map.
#StrVal: string | number | bool // gengotypes: emits `type StrVal any` — referenced by inline {[string]: #StrVal} maps

#StrMap: ({[string]: #StrVal}) @go(-) // gengotypes: hand StrMap = map[string]string (spec/union_types.go)

// Go time.ParseDuration string (units ns/us/µs/ms/s/m/h), e.g. "30m", "1h30m".
#Duration: string & =~"^[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$" @go(-)

// ---------------------------------------------------------------------------
// Container security (SecurityConfig) — shared by box / candy / deploy / sidecar.
// ---------------------------------------------------------------------------
#Security: {
	privileged?: bool
	cgroupns?:   "host" | "private" | "" @go(CgroupNS)
	cap_add?: [...string] @go(CapAdd)
	devices?: [...string]
	security_opt?: [...string] @go(SecurityOpt)
	ipc_mode?: "host" | "private" | "shareable" | "" @go(IpcMode)
	shm_size?: #Size                                 @go(ShmSize)
	group_add?: [...string] @go(GroupAdd)
	mount?: [...string] @go(Mounts)
	memory_max?:      #Size @go(MemoryMax)
	memory_high?:     #Size @go(MemoryHigh)
	memory_swap_max?: #Size @go(MemorySwapMax)
	cpus?:            string & =~"^[0-9]+(\\.[0-9]+)?$"
}

// ---------------------------------------------------------------------------
// Shell-rc config (ShellConfig/ShellSpec) — shared by box + candy. CLOSED: the
// Go UnmarshalYAML rejects any key outside the 4 intrinsic + 4 shell names.
// ---------------------------------------------------------------------------
#Shell: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?:     string
	priority?: int @go(,type=int)
	bash?:     #ShellSpec @go(Bash,optional=nillable)
	zsh?:      #ShellSpec @go(Zsh,optional=nillable)
	fish?:     #ShellSpec @go(Fish,optional=nillable)
	sh?:       #ShellSpec @go(Sh,optional=nillable)
}
#ShellSpec: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?: string
}

// ---------------------------------------------------------------------------
// Calamares package surface (PackageItem/DistroPackages/AURPackages) — shared by
// candy + group. `repo` is the genuine typed-open passthrough (#RepoBlock).
// ---------------------------------------------------------------------------
// bare scalar shorthand XOR object form.
#PackageItem: ((string & !="") | {
		name:         string & !=""
		description?: string & !=""
}) @go(-) // gengotypes: hand PackageItem (spec/union_types.go)
#DistroPackages: {
	package?: [...#PackageItem]
	copr?: [...(string & !="")]
	repo?: [...#RepoBlock]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")] @go(Options)
	module?: [...(string & !="")]
	aur?: #AUR @go(AUR,optional=nillable)
}
#AUR: {
	package?: [...#PackageItem]
	option?: [...(string & !="")] @go(Options)
	replace?: [...(string & !="")] @go(Replaces)
}

// Free-form per-distro upstream-repo block: `name` is load-bearing (the only
// key any code requires), the rest pass through verbatim to install templates.
#RepoBlock: {
	name: string & !=""
	...
} @go(-) // gengotypes: hand-written `type RepoBlock = map[string]any` (spec/scalar_aliases.go) — an ALIAS so []RepoBlock IS []map[string]any (toMapSlice / template rendering)

// install_opts gates (deploy.go InstallOptsConfig) — shared by local + deploy.
#InstallOpts: {
	with_service?:       bool @go(WithServices)
	allow_repo_changes?: bool @go(AllowRepoChanges)
	allow_root_tasks?:   bool @go(AllowRootTasks)
	skip_incompatible?:  bool @go(SkipIncompatible)
	verify?:             bool
	builder_image?:      string & !="" @go(BuilderImage)
}
