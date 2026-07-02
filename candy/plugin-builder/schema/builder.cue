// The BUILT-IN `builder` plugin's OWN CUE schema — the typed input for the `builder`
// KIND (the multi-stage builder vocabulary: pixi/npm/cargo/aur/bootstrap, formerly a
// core `builder:` kind decoded into the typed core map uf.Builder). SINGLE SOURCE for
// this plugin's params, used two ways (the same contract the package-group/agent/
// module/sidecar/distro plugins and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (task cue:gen) →
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — served over Describe (InProcTransport),
//     spliced base ++ plugin, every authored `builder:` body validated against
//     #BuilderInput BEFORE runPluginKind dispatches.
//
// SELF-CONTAINED: references NO base def — every shared shape (#CacheMount / #PhaseSet /
// #PhaseTemplates / #StrMap from _common.cue) is reproduced standalone here (prefix
// #Bd). Faithful reproduction of the core #Builder (schema/builder.cue) — same authored
// WIRE keys — so the host validates a real builder entity and the plugin's Invoke
// canonicalises the body back through the core spec.Builder type (which BuilderDef
// aliases and the generator consumes via the Builders() accessor). @go() annotations
// are dropped (CORE codegen only; the validator ignores attributes). NAME: the entity
// name is the node KEY, never a body field — #Builder has no name field.
#BuilderInput: {
	detect_file?: [...(string & !="")]
	detect_config?:    string & !=""
	requires_src_dir?: bool
	inline?:           bool
	cache_mount?: [...#BdCacheMount]
	env?:              #BdStrMap
	runtime_env?:      #BdStrMap
	stage_template?:   string
	install_template?: string
	manylinux_fix?:    string
	build_script?:     string & !=""
	phase?:            #BdPhaseSet
	install_command?: {
		[string]: string
	}
	copy_artifact?: [...#BdCopy]
	copy_binary?: #BdCopy
	path_contribution?: [...(string & !="")]
	kind:             *"layer" | "bootstrap"
	privileged:       *false | true
	output_artifact?: string & =~"^/"
	if privileged {
		output_artifact!: string & =~"^/"
	}
}

#BdCopy: {
	src:    string & !=""
	dst:    string & !=""
	chown?: bool
}

// reproduces #CacheMount (schema/_common.cue) standalone.
#BdCacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
}

// reproduces #PhaseSet / #PhaseTemplates (schema/_common.cue) standalone.
#BdPhaseSet: {
	prepare?: #BdPhaseTemplates
	install?: #BdPhaseTemplates
	cleanup?: #BdPhaseTemplates
}
#BdPhaseTemplates: {
	container?: string
	host?:      string
}

// reproduces #StrMap (schema/_common.cue) standalone — values are string-coercible
// (a quoted string / number / bool; yaml.v3 coerces an unquoted int/bool to its
// literal text).
#BdStrMap: {[string]: (string | number | bool)}
