// buildwire.cue — the out-of-proc BUILD-TIME wire (SDD conversion of the
// former deploy_wire.go's build-time section, per the standing operator
// directive: a hand-written wire struct not yet CUE-sourced is
// conversion-in-progress, never a sanctioned exception). What a plugin
// verb/builder/build-drive exchanges with the host across the go-plugin
// boundary on an OpEmit / OpResolve / OpBuild / OpCollectContext / OpReverse
// Invoke at IMAGE BUILD time. Plain structs — gengotypes generates them
// faithfully, no disjunction needed.

// #BuildEnv is the build-context descriptor the host puts in op.Env for an
// OpEmit Invoke at image-generation time: the image's distro tags + name, so
// a plugin can tailor its emitted Containerfile fragment per distro/arch.
#BuildEnv: {
	distros?: [...string] @go(Distros)
	image?: string @go(Image)

	dev_local_pkg?:      bool   @go(DevLocalPkg)
	image_build_dir?:    string @go(ImageBuildDir)
	context_rel_prefix?: string @go(ContextRelPrefix)
	// A pod-overlay deploy's add_candy: refs (if any) — threaded so a HOST-COUPLED word's OpEmit
	// (candy/plugin-installstep's getGenerator) can widen ITS OWN "resolved-project" envelope
	// re-fetch the same way ScanAllCandyWithConfigOpts' ResolveOpts.ExtraCandyRefs already widens
	// the host's own overlay Generator (hostBuildOverlay). Without this, an add_candy candy the
	// overlay build correctly resolved host-side is absent from this plugin's INDEPENDENT
	// envelope fetch, and candyByName's REMOTE-candy fallback still misses (RCA'd K1-alpha
	// regression: check-addcandy-pod's overlay-deploy path, "candy not found").
	extra_candy_refs?: [...string] @go(ExtraCandyRefs)
}

// #EmitReply is what a plugin verb/builder returns from an OpEmit Invoke at
// build time. By default fragment is a verbatim Containerfile FRAGMENT the
// generator splices into the emitted Containerfile. When act_script is true
// the fragment is instead a raw state-provision ACT SHELL (the former
// ProvisionActor.RenderProvisionScript output): the render wraps it in a RUN
// via the same EmitCmd path a literal command verb uses, rather than splicing
// it verbatim. This lets a state-provision verb self-declare its build-emit
// shape over the uniform Invoke(OpEmit), so the render dispatches every verb
// through InvokeProvider(OpEmit) with no package-main concrete-type assert.
#EmitReply: {
	fragment!:   string @go(Fragment)
	act_script?: bool   @go(ActScript)
}

// #StepEmitRequest is the F-STEP-EMIT HostBuild envelope for a HOST-COUPLED
// external step kind's build-context fragment.
#StepEmitRequest: {
	word!: string @go(Word)
	payload?: bytes @go(Payload,type=RawBody)
	distros?: [...string] @go(Distros)
}

// #BuilderResolveReply is what a builder plugin returns from an OpResolve
// Invoke at image-generation time — the build-time BUILDER leg.
#BuilderResolveReply: {
	stage?:           string   @go(Stage)
	copy_artifacts?: [...string] @go(CopyArtifacts)
	copy_binary?:     string   @go(CopyBinary)
	inline_fragment?: string   @go(InlineFragment)
}

// #BuilderResolveInput is the OpResolve params: the RENDER CONTEXT the host
// computes and hands a builder plugin so it can render its build-time
// multi-stage self-contained.
#BuilderResolveInput: {
	candy!: string @go(Candy)
	builder?:     string @go(Builder)
	builder_ref?: string @go(BuilderRef)
	stage_name?:  string @go(StageName)
	layer_stage?: string @go(LayerStage)
	copy_src?:    string @go(CopySrc)
	uid?:         int    @go(UID,type=int)
	gid?:         int    @go(GID,type=int)
	home?:        string @go(Home)
	user?:        string @go(User)
	manifest?:    string @go(Manifest)
	has_lock_file?: bool   @go(HasLockFile)
	install_cmd?:   string @go(InstallCmd)
	manylinux_fix?: string @go(ManylinuxFix)
	has_build_script?: bool   @go(HasBuildScript)
	build_script?:     string @go(BuildScript)
	packages?: [...string] @go(Packages)
	options?: [...string] @go(Options)
	cache_mounts_owned?: string @go(CacheMountsOwned)
	cache_mounts_auto?:  string @go(CacheMountsAuto)
	inline?:             bool   @go(Inline)
}

// #BuildRequest is the CLI→DRIVE envelope: what `charly box build` /
// `charly box generate` marshal into the compiled-in candy/plugin-build's
// Invoke (op.Params).
#BuildRequest: {
	boxes?: [...string] @go(Boxes) // positional box selection ("" → all enabled)
	extra_candy_refs?: [...string] @go(ExtraCandyRefs) // extra candy refs to widen the resolve (e.g. an overlay's add_candy set); scanned into resolveBuildEngine

	tag?:              string @go(Tag)             // --tag override (empty → CalVer)
	dir?:              string @go(Dir)              // project dir the host reconstructs config from
	include_disabled?: bool   @go(IncludeDisabled)  // --include-disabled
	dev_local_pkg?:    bool   @go(DevLocalPkg)      // --dev-local-pkg (localpkg from local source; build only)
	push?:             bool   @go(Push)             // --push (build only)
	platform?:         string @go(Platform)         // --platform (build only)
	cache?:            string @go(Cache)            // --cache mode (build only)
	no_cache?:         bool   @go(NoCache)           // --no-cache (build only)
	jobs?:             int    @go(Jobs,type=int)        // --jobs outer concurrency (build only)
	podman_jobs?:      int    @go(PodmanJobs,type=int)  // --podman-jobs inner concurrency (build only)
}

// #BuildReply is what a build:box / build:generate plugin echoes back from
// its HostBuild call.
#BuildReply: {
	written?: [...string] @go(Written)
	error?:   string @go(Error)
}

// #OverlayBuildRequest is the BUILD-ENGINE DISPATCH envelope for the
// pod-overlay build — the F10 "overlay" host-builder.
#OverlayBuildRequest: {
	dir?:                string @go(Dir)                // project dir (build-context root) the host reconstructs config from
	deploy_name?:        string @go(DeployName)          // the raw deploy name (dotted for a nested pod; flattened engine-side)
	image?:              string @go(Image)               // the base box the overlay inherits FROM (node.Image; "" → DeployName)
	version?:            string @go(Version)             // the base image CalVer pin (node.Version; "" → newest-local)
	dry_run?:            bool   @go(DryRun)
	assume_yes?:         bool   @go(AssumeYes)
	allow_repo_changes?: bool   @go(AllowRepoChanges)
	allow_root_tasks?:   bool   @go(AllowRootTasks)
	with_services?:      bool   @go(WithServices)
}

// #OverlayBuildReply is what the "overlay" host-builder returns.
#OverlayBuildReply: {
	overlay_ref?: string @go(OverlayRef)
	base_image?:  string @go(BaseImage)
	deploy_name?: string @go(DeployName)
	error?:       string @go(Error)

	// resolved_project is the overlay-scoped resolved-project envelope the
	// candy constructs a deploykit.Generator from. Nil when there is no
	// add_candy overlay to synthesize (the tag-only path).
	resolved_project?: #ResolvedProject @go(ResolvedProject,optional=nillable)

	// plans is the deployment's compiled InstallPlans serialized as
	// InstallPlanViews. Empty for the no-overlay path.
	plans?: [...#InstallPlanView] @go(Plans)

	// base_user / base_security / base_registry (the base image's runtime USER,
	// baked LabelSecurity, and ai.opencharly.registry OCI label) are DELETED
	// (K3-W2): the candy already holds base_image (this reply) and can read
	// this metadata itself via deploykit.ExtractMetadata — the same
	// spec/container mechanism the host used, re-exported sdk-side — no host
	// round-trip needed (candy/plugin-deploy-pod/lifecycle.go's
	// podPrepareVenue computes it directly).

	// calver is the host's current CalVer.
	calver?: string @go(CalVer)

	// overlay_candy_security carries each overlay candy's own `security:`
	// block.
	overlay_candy_security?: {[string]: #Security} @go(OverlayCandySecurity,type=map[string]*Security)

	// parent_volumes carries the PARENT deploy node's bind-mount volumes for a
	// NESTED pod-in-pod overlay build.
	parent_volumes?: [...#DeployVolume] @go(ParentVolumes)
}

// #BuilderCollectInput is the OpCollectContext params: the host-supplied
// candy descriptor an external builder plugin reads to produce its per-candy
// stage context.
#BuilderCollectInput: {
	candy!:   string @go(Candy)
	builder!: string @go(Builder)
	home?:    string @go(Home)
	packages?: [...string] @go(Packages) // the builder's detect-config section packages (aur)
	replaces?: [...string] @go(Replaces) // aur `replaces:` — repo packages removed before pacman -U
}

// #BuilderCollectReply is the OpCollectContext reply: the builder-specific
// stage-context keys the host merges onto the base context.
#BuilderCollectReply: {
	context?: {...} @go(Context,type=map[string]any)
}

// #BuilderReverseInput is the OpReverse params: the candy + its resolved
// stage-context keys.
#BuilderReverseInput: {
	candy!:   string @go(Candy)
	builder!: string @go(Builder)
	context?: {...} @go(Context,type=map[string]any)
}

// #BuilderReverseReply is the OpReverse reply: the builder's teardown ops.
#BuilderReverseReply: {
	reverse_ops?: [...#ReverseOp] @go(ReverseOps)
}

// --- build:ensure wire (core-min wave 3, build-engine cluster relocation) ---
//
// The former core ensure-image + cross-engine-transfer ORCHESTRATION (decide
// pull-vs-build, exec podman pull/tag, cross-engine transfer) moved into the
// compiled-in candy/plugin-build as a third word alongside box/generate:
// build:ensure. It reaches the host for the two things it genuinely cannot do
// itself: resolving a user-authored image identifier against the project's
// charly.yml (the "box-ref-resolve" HostBuild seam, wrapping ResolveBox /
// FindBoxByLeaf — loader-cone, still core) and cloning/caching an @github.com/...
// remote ref's source ("remote-image-resolve", wrapping EnsureRepoDownloaded —
// also loader-cone; the registry pull ref is now computed plugin-side, K1 loader
// wave — the former host-side box-RESOLVE shed from charly core). The build
// fallback itself reaches the EXISTING build:box word in-process (no new seam).

// #BoxRefResolveRequest / #BoxRefResolveReply — the "box-ref-resolve" HostBuild
// seam: resolve a short-name or full-ref image identifier against charly.yml.
#BoxRefResolveRequest: {
	image!: string @go(Image)
	dir?:   string @go(Dir)
}

#BoxRefResolveReply: {
	// exists_ref / pull_ref is the fully-qualified registry ref to check for local
	// presence / hand to `podman pull`; "" when the identifier could not be
	// resolved (no project reachable, or an unknown short name).
	exists_ref?: string @go(ExistsRef)
	pull_ref?:   string @go(PullRef)
	// build_fallback_short is the short name (bare or namespace-qualified, e.g.
	// "fedora.fedora-builder") `charly box build` should target when the pull
	// fails; "" when no local build-fallback is possible for this identifier.
	build_fallback_short?: string @go(BuildFallbackShort)
	// produced_ref is the registry ref build_fallback_short resolves to today
	// (registry+name, tag-less) — used to tag-alias a pinned-tag input ref onto
	// the freshly built image.
	produced_ref?: string @go(ProducedRef)
}

// #RemoteImageResolveRequest / #RemoteImageResolveReply — the
// "remote-image-resolve" HostBuild seam: clone/cache an @github.com/org/repo/box
// ref's source + return the cached dir + short box name. The host does ONLY the git
// clone/cache (EnsureRepoDownloaded, K1/B floor); the calling plugin resolves the
// registry pull ref itself via the K1 loader reverse legs (K1 loader wave — sheds
// deploykit.ResolveSpecBox from charly core; the former image_ref field is GONE).
#RemoteImageResolveRequest: {
	ref!: string @go(Ref)
	tag?: string @go(Tag)
}

#RemoteImageResolveReply: {
	cache_dir?: string @go(CacheDir)
	box_name?:  string @go(BoxName)
	error?:     string @go(Error)
}

// #BuildEnsureRequest / #BuildEnsureReply — the build:ensure word's Invoke
// envelope: ensure an image is present in local podman storage, falling back
// to a local (or remote-cached) build when the identifier maps to a project
// charly.yml entry.
#BuildEnsureRequest: {
	image!:        string @go(Image)
	dir?:          string @go(Dir)
	build_engine?: string @go(BuildEngine)
	run_engine?:   string @go(RunEngine)
}

#BuildEnsureReply: {
	error?: string @go(Error)
}

// #BuildPkgRequest / #BuildPkgReply — the build:pkg word's Invoke envelope: `charly box pkg`'s
// CLI→DRIVE envelope (mirrors #BuildRequest's shape, K3 build-tail move — the former hidden core
// `__box-pkg` reentry is DELETED, its body now runs plugin-side in candy/plugin-build).
#BuildPkgRequest: {
	format?: [...string] @go(Format) // requested package formats (empty → every format the candy declares)
	candy?:  string @go(Candy)       // candy whose localpkg sources to build
	out?:    string @go(Out)         // output directory for the built package files
	dir?:    string @go(Dir)         // project dir the host reconstructs config from
}

#BuildPkgReply: {
	written?: [...string] @go(Written)
	error?:   string @go(Error)
}

