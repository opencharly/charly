// CUE schema for the `builder` kind. #Builder validates ONE value of the
// `builder:` map (BuilderDef). CLOSED: every authored key is modeled (an unknown
// key is a typo). Template/script bodies are Go text/template (plain `string`).
// #CacheMount / #PhaseSet / #PhaseTemplates are shared (_common.cue). No #Step
// (builder has no plan).

#Builder: {
	detect_file?: [...(string & !="")] @go(DetectFiles)
	detect_config?:    string & !="" @go(DetectConfig)
	requires_src_dir?: bool          @go(RequiresSrcDir)
	inline?:           bool
	cache_mount?: [...#CacheMount] @go(CacheMount)
	env?:              #StrMap
	runtime_env?:      #StrMap       @go(RuntimeEnv)
	install_template?: string        @go(InstallTemplate)
	manylinux_fix?:    string        @go(ManylinuxFix)
	build_script?:     string & !="" @go(BuildScript)
	phase?:            #PhaseSet     @go(Phases,optional=nillable)
	install_command?: {
		[string]: string
	} @go(InstallCommands)
	copy_artifact?: [...#Copy] @go(CopyArtifacts)
	copy_binary?: #Copy @go(CopyBinary,optional=nillable)
	path_contribution?: [...(string & !="")] @go(PathContributions)
	kind:             *"layer" | "bootstrap"
	privileged:       *false | true
	output_artifact?: string & =~"^/" @go(OutputArtifact)
	if privileged {
		output_artifact!: string & =~"^/"
	}
}

#Copy: {
	src:    string & !=""
	dst:    string & !=""
	chown?: bool
}

// #BuilderConfig is the `builder:` build-vocabulary container — the whole
// builder map keyed by builder name (charly/charly.yml's `builder:` section).
// #55 step 3-III: relocated from sdk/buildkit for the import-purity lever (see
// #DistroConfig). Its methods (ValidBuilderType / BuilderNames) are pure Go
// METHODS and stay hand-written in spec/builder_config_methods.go.
#BuilderConfig: {
	builder: {[string]: #Builder} @go(Builder,type=map[string]*Builder)
}
