// CUE schema for the fully-resolved BOX render-context data-carrier (#55 step3 unit 3a).
// #ResolvedBox is the wire-clean, in-process VALUE half of a box's resolved configuration —
// shared by charly core (the "resolved-project" envelope projector, the render-seam floor)
// and sdk/buildkit (the resolve ENGINE, ResolveBox/ResolveAllBox). SDD: a render-context
// data-carrier is CUE-sourced (the kit.CheckResult precedent, checkresult.cue) — hand-writing
// it is forbidden. buildkit.ResolvedBox EMBEDS the generated #ResolvedBox and adds back ONLY
// the three genuinely buildkit-owned host-render-cache pointers that reference buildkit's OWN
// vocabulary types (DistroConfig/DistroDef/BuilderConfig — the parsed distro:/builder: build
// vocabulary) plus the init-selection scalar/pointer (InitSystem/InitDef) — none of which CUE
// can express without importing buildkit (a spec→buildkit import would cycle, since buildkit
// already imports spec). Every OTHER field generates faithfully as a plain struct (the
// gengotypes CAN table: plain scalar/slice/map/pointer-to-generated-def fields all generate).
//
// #BuilderMap, #AggregatedCandyCaps, and #DistroBuilderCandidate are pure data with zero
// buildkit-specific typing, so they are CUE-sourced here too (rather than staying hand-written
// in buildkit) — moved out of buildkit per the same rule, aliased back as
// `type BuilderMap = spec.BuilderMap` / `type AggregatedCandyCaps = spec.AggregatedCandyCaps`
// for buildkit's existing bare references. Their METHODS (BuilderFor/HasBuilder/AllBuilder,
// SupportsTag/SupportsBuild) are NOT generated — gengotypes has no construct for behavior — and
// stay hand-written in spec/resolved_box_methods.go alongside the generated types (the
// Op.Kind()-style exception). The builder-map value-primitive FUNCTIONS
// (EffectiveBuilderForBox/ResolveEffectiveBuilder/distroBuilderMap/PickDistroBuilder) are
// likewise hand-written behavior in spec/spec/builder_resolve.go.

// #BuilderMap — a map of build type (pixi/npm/cargo/aur) → builder image name.
#BuilderMap: {[string]: string}

// #DistroBuilderCandidate is one named entity's Distro tags + Builder map — the generic shape
// PickDistroBuilder consumes (R3: the shared abstraction behind the "distro-keyed builder default"
// lookup). Pure data (string/[]string/#BuilderMap) with zero buildkit-specific typing, so it is
// CUE-sourced here alongside #BuilderMap per the same rule — moved out of buildkit (the
// builder-map value-primitive cluster: EffectiveBuilderForBox/ResolveEffectiveBuilder/
// distroBuilderMap/PickDistroBuilder + this candidate type relocated to spec/spec as pure-value
// CONTRACT computation, plan Rule 2 + step 3). The lookup FUNCTIONS themselves are BEHAVIOR
// (hand-written Go, not CUE-generated) and live in spec/spec/builder_resolve.go.
#DistroBuilderCandidate: {
	name!:    string @go(Name)
	distro!:  [...string] @go(Distro)
	builder!: #BuilderMap @go(Builder, type=BuilderMap)
}

// #AggregatedCandyCaps — the output of walking all candies in resolution order. Populated
// onto #ResolvedBox and consumed wherever code reads BoxConfig.Bootc/BoxConfig.DataImage/the
// init-system bootc parameter.
#AggregatedCandyCaps: {
	preserve_user?:         bool @go(PreserveUser)
	needs_root_after_init?: bool @go(NeedsRootAfterInit)
	init_system_hint?:      string @go(InitSystemHint)
	data_only?:             bool @go(DataOnly)
	oci_labels?: {[string]: string} @go(OCILabels)
	// provided is the set of capability names declared by some candy in the composition.
	// Used by CheckRequiredCapabilities to validate `requires_capabilities:` cross-candy
	// requirements.
	provided?: {[string]: bool} @go(Provided)
}

// #ResolvedBox — a fully resolved box configuration, the wire-clean subset. EXCLUDED here
// (both stay on the buildkit.ResolvedBox embedding wrapper, as hand `json:"-"` fields — the
// gengotypes CANNOT-table's "json:"-" keep-in-Go, drop-from-wire" exception, spike-verified:
// no CUE construct emits a suppressed-from-marshal field):
//   - the three host-render-cache VOCABULARY pointers DistroConfig/DistroDef/BuilderConfig
//     (+ InitSystem/InitDef) — buildkit-owned types spec must never import;
//   - the four RENDER-cache fields CandyCaps/BakedMetadata/RenderCandyOrder/ActiveInits —
//     `json:"-"` on ResolvedBox itself (the SEPARATE ResolvedBoxView wire type carries the
//     actual wire copies of this same data, via NewSpecResolvedBox/ProjectResolvedBox).
// Fields that carried NO json tag on the former hand struct (always emitted, no omitempty)
// are REQUIRED (`!`) here; fields that already carried `,omitempty` are OPTIONAL (`?`).
#ResolvedBox: {
	name!: string @go(Name)
	// version is the authored per-entity CalVer (the box config `version:`); optional.
	version?: string @go(Version)
	// effective_version is the content-derived identity emitted as the ai.opencharly.version
	// label: the dedicated version if set, else the highest candy version across the full
	// chain. Stable across builds when no candy changed.
	effective_version?: string @go(EffectiveVersion)
	status?:             string @go(Status)      // effective status (worst of box + candies)
	info?:                string @go(Info)        // aggregated info from box + candies
	check_level?:         string @go(CheckLevel)  // acceptance-depth rung (none|build|noagent|agent)
	base!:                string @go(Base)         // resolved base (external OCI ref or internal image name)
	// from mirrors BoxConfig.From after resolution. When non-empty (e.g. "builder:pacstrap"),
	// the generator emits FROM scratch + ADD <staged-rootfs.tar.gz> instead of FROM <base>.
	from?:                    string @go(From)
	bootstrap_builder_image?: string @go(BootstrapBuilderImage)
	platforms!: [...string] @go(Platforms)
	tag!:      string @go(Tag)
	registry!: string @go(Registry)
	pkg!:      string @go(Pkg)      // primary build format (first entry in build_formats)
	distro!: [...string] @go(Distro)       // resolved distro tags: ["fedora:43", "fedora"]
	build_formats!: [...string] @go(BuildFormats) // resolved build formats: ["rpm"] or ["pac", "aur"]
	tags!: [...string] @go(Tags)           // union: ["all"] + distro + build_formats
	candy!: [...string] @go(Candy)

	// User configuration.
	user!: string @go(User) // username
	uid!:  int    @go(UID, type=int)
	gid!:  int    @go(GID, type=int)
	home!: string @go(Home) // resolved home directory (detected or /home/<user>)
	// user_adopted is true when the resolved user came from the distro's base_user
	// declaration rather than being created by the bootstrap.
	user_adopted!: bool @go(UserAdopted)

	// Merge configuration — layer merge settings (nil means use CLI defaults).
	merge?: #BoxMerge @go(Merge, optional=nillable)

	// Builder configuration (resolved: image -> base image -> defaults -> {}).
	builder!: #BuilderMap @go(Builder, type=BuilderMap)
	// Builder capability declaration (image-specific, not inherited).
	builder_capabilities!: [...string] @go(BuilderCapabilities)

	auto!: bool @go(Auto) // true for auto-generated intermediate images

	// Container network mode (e.g. "host", "none") — declaration of required/recommended
	// network mode. Deployment overrides via MergeDeployOntoMetadata.
	network!: string @go(Network)

	data_image!: bool @go(DataImage) // true = FROM scratch, no runtime, no init, no services

	// OCI entrypoint/cmd baked into the image's OCI config (final Containerfile
	// ENTRYPOINT/CMD). Empty by default — normal images carry no baked command and
	// the deploy-time init injects it; declared only when something spawns the image
	// directly from its OCI config (e.g. an embedded controller spawning child
	// agents) with no charly deploy in the loop.
	entrypoint?: [...(string & !="")] @go(Entrypoint, type=[]string)
	cmd?:        [...(string & !="")] @go(Cmd, type=[]string)

	// Derived fields.
	is_external_base!: bool   @go(IsExternalBase) // true if base is external OCI image
	full_tag!:         string @go(FullTag)        // registry/name:tag
}
