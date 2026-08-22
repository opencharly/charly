// CUE schema for the build-render context keystones. #InstallContext and
// #BuildStageContext are the data an image build's format/builder Go
// text/template renders against — host-computed, and (future) handed to a
// builder plugin over OpResolve. Package-less; concatenated into the spec
// compilation unit. NOT authoring kinds (never in #Node/#Op) — pure generated
// param types, single-sourced here so `task cue:gen` produces the Go structs
// (charly aliases them via vmshared/spec_aliases.go). #CacheMount is shared
// (_common.cue). @go names match the Go field names the templates reference.

// #InstallContext — data a distro format's install/prepare/cleanup template
// renders against: packages + repo/copr/module/exclude/key modifiers, resolved
// cache mounts, and the builder-stage identity/uid/gid/home.
#InstallContext: {
	cache_mounts?: [...#CacheMount] @go(CacheMounts)
	packages?: [...string] @go(Packages)
	repos?: [...{...}] @go(Repos) // arbitrary per-repo config maps ([]map[string]any)
	options?: [...string] @go(Options)
	copr?: [...string] @go(Copr)
	modules?: [...string] @go(Modules)
	exclude?: [...string] @go(Exclude)
	keys?: [...string] @go(Keys)
	stage_name?:  string @go(StageName)
	builder_ref?: string @go(BuilderRef)
	user?:        string @go(User)
	uid?:         int    @go(UID,type=int)
	gid?:         int    @go(GID,type=int)
	home?:        string @go(Home)
}

// #BuildStageContext — data a builder's multi-stage `stage_template` renders
// against: builder/stage identity, scratch-stage + copy-src wiring, detected
// manifest/lockfile/build-script, install command, cache mounts, and (for
// config-detected builders like aur) packages/options.
#BuildStageContext: {
	builder_ref?:  string @go(BuilderRef)
	stage_name?:   string @go(StageName)
	layer_stage?:  string @go(LayerStage) // scratch stage name for COPY --from
	copy_src?:     string @go(CopySrc)     // build-context path for candy files
	uid?:          int    @go(UID,type=int)
	gid?:          int    @go(GID,type=int)
	home?:         string @go(Home)
	user?:         string @go(User)
	manifest?:     string @go(Manifest)
	has_lock_file?: bool  @go(HasLockFile)
	install_cmd?:  string @go(InstallCmd)
	manylinux_fix?: string @go(ManylinuxFix)
	cache_mounts?: [...#CacheMount] @go(CacheMounts)
	packages?: [...string] @go(Packages) // config-detected builders (aur)
	options?: [...string] @go(Options)   // config-detected builders (aur)
	has_build_script?: bool  @go(HasBuildScript)
	build_script?:     string @go(BuildScript)
}
