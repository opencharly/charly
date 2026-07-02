// CUE schema for IMAGES. #Box validates ONE image entity — a `candy:` node carrying
// `base:`/`from:` (the former `box:` kind, merged INTO `candy:` by EDGE-INHERIT cutover
// D; the image arm of #CandyValue) in a discovered box/<distro>/box/<name>/charly.yml.
// Per-entity model.
// CLOSED (an unknown key is a typo). Shared defs (#Step/#Security/#Shell/#CalVer/
// #EntityRef/#EnvVar) come from _common.cue. Source of truth: charly/config.go
// BoxConfig (the `defaults:` block reuses BoxConfig but is NOT validated against
// #Box — only box ENTITIES are — so every BoxConfig field is modeled here even
// when only `defaults:` authors it).

// Package formats (BoxConfig.Build / BuildFormats). Named #BuildFormat to avoid
// colliding with distro.cue's #Format (the package-format definition struct).
#BuildFormat: "rpm" | "deb" | "pac" | "aur" @go(-)

// Builder build-type slots (BoxConfig.Produce + BoxConfig.Builder keys).
#BuildType: "pixi" | "npm" | "cargo" | "aur" @go(-)

// MergeConfig (config.go). CLOSED.
#BoxMerge: {
	auto?:         bool
	max_mb?:       int & >=0 @go(MaxMB,type=int)
	max_total_mb?: int & >=0 @go(MaxTotalMB,type=int)
}

// AliasConfig (config.go). CLOSED. command defaults to name when omitted.
#BoxAlias: {
	name:     string & !=""
	command?: string & !=""
}

// ReadinessConfig (config.go / readiness_config.go) — the `defaults.readiness:`
// bounds for the unified pollUntil readiness primitive (poll.go). CLOSED. Every
// field is a Go time.ParseDuration string; absent → the named fallback const in
// poll.go. Defaults-only (reuses the BoxConfig type), but modeled here per the
// #Box completeness invariant in this file's header. The Resolve()-time ordering
// invariants (interval <= no_progress <= absolute_cap; poll_interval_local <=
// stop_grace <= absolute_cap) are cross-field and stay in Go (validateOrdering).
#Readiness: {
	poll_interval_local?:  #Duration @go(PollIntervalLocal)
	poll_interval_remote?: #Duration @go(PollIntervalRemote)
	poll_interval_heavy?:  #Duration @go(PollIntervalHeavy)
	per_attempt?:          #Duration @go(PerAttempt)
	per_attempt_heavy?:    #Duration @go(PerAttemptHeavy)
	no_progress?:          #Duration @go(NoProgress)
	absolute_cap?:         #Duration @go(AbsoluteCap)
	stop_grace?:           #Duration @go(StopGrace)
}

// base and from are mutually exclusive; neither is also valid (scratch box).
// The entity-level base⊻from mutual-exclusion is enforced in GO
// (BoxConfig.HasBaseFromConflict, surfaced by validateBoxBaseFrom in validate.go
// and resolveBase in config.go) — NOT by a trailing `& ({from?: _|_} |
// {base?: _|_})` disjunction. `cue exp gengotypes` collapses an entity-level
// top-level disjunction to an EMPTY `struct{}`, which would make spec.Box useless
// as a drop-in for BoxConfig; the plain closed struct below generates the full
// field set, and the Go rule restores the XOR. The struct stays CLOSED (no `...`)
// so an unknown key is still a typo.
#Box: {
	name?:        #EntityRef
	version?:     #CalVer
	description?: string & !=""
	enabled?:     bool @go(,type=*bool)

	base?:                    string & !=""
	from?:                    string & =~"^builder:[a-z0-9]+(-[a-z0-9]+)*$"
	bootstrap_builder_image?: string & !="" @go(BootstrapBuilderImage)

	platform?: [...(string & =~"^[a-z][a-z0-9]*/[a-z0-9]+")] @go(Platforms)
	tag?:      string & !=""
	registry?: string & !=""

	distro?: [...(string & !="")]
	// BuildFormats: a scalar OR a list on the wire (#BuildFormats UnmarshalYAML
	// normalizes "rpm" → ["rpm"]); the Go field is []string. gengotypes degrades
	// the disjunction to `any`, so pin the Go shape explicitly (the typed enum is
	// a future enhancement — collapse for now).
	build?: (#BuildFormat | [...#BuildFormat]) @go(Build,type=[]string)

	candy?: [...#CandyRef]

	// A box declares NO port: — published ports are inherited from the candy chain
	// (CollectBoxPorts) and host mappings are auto-allocated at deploy. A residual
	// box `port:` is rejected by CUE closedness (an unknown field).

	user?:        string & !=""
	uid?:         int & >=0 @go(UID,type=*int)
	gid?:         int & >=0 @go(GID,type=*int)
	user_policy?: *"auto" | "adopt" | "create" @go(UserPolicy)

	builder?: {[string]: string & !=""}
	produce?: [...#BuildType]

	env?: [...#EnvVar]
	env_file?:   string & !="" @go(EnvFile)
	security?:   #Security @go(Security,optional=nillable)
	network?:    string & !=""
	init?:       "supervisord" | "systemd"
	data_image?: bool @go(DataImage)
	bootc?:      bool // image is bootc-bootable (for `charly vm build` → qcow2)
	readiness?:  #Readiness @go(Readiness,optional=nillable)

	jobs?:            int & >=1 @go(,type=*int)
	podman_jobs?:     int & >=0 @go(PodmanJobs,type=*int) // 0 = auto (min(NCPU, cap))
	podman_jobs_cap?: int & >=1 @go(PodmanJobsCap,type=*int)
	context_ignore?: [...(string & !="")] @go(ContextIgnore)
	cache?:           "image" | "registry" | "gha" | "none"
	keep_images?:     int & >=0 @go(KeepImages,type=*int)
	keep_check_runs?: int & >=0 @go(KeepCheckRuns,type=*int)

	merge?: #BoxMerge @go(Merge,optional=nillable)
	alias?: [...#BoxAlias]

	plan?: [...#Step]
	check_level?: *"noagent" | "none" | "build" | "agent" @go(CheckLevel)

	shell?: #Shell @go(Shell,optional=nillable)
}
