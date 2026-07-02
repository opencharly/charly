// The BUILT-IN `init` plugin's OWN CUE schema — the typed input for the `init` KIND
// (the init-system vocabulary: supervisord/systemd, formerly a core `init:` kind
// decoded into the typed core map uf.Init). SINGLE SOURCE for this plugin's params,
// used two ways (the same contract the package-group/agent/module/sidecar/distro/builder
// plugins and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (task cue:gen) →
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — served over Describe (InProcTransport),
//     spliced base ++ plugin, every authored `init:` body validated against #InitInput
//     BEFORE runPluginKind dispatches.
//
// SELF-CONTAINED: the core #Init (schema/init.cue) already references NO _common.cue
// def, so this is a verbatim reproduction with the inner #InitServiceSchema renamed
// #InServiceSchema (prefix #In) and the @go() annotations dropped (CORE codegen only;
// the validator ignores attributes). The plugin's Invoke canonicalises the body back
// through the core spec.Init type (which InitDef aliases and the generator consumes via
// the Inits() accessor). NAME: the entity name is the node KEY, never a body field —
// #Init has no name field. `model` is the one mandatory field (concrete-validated).
//
// NOTE — the Go package is `initbuiltin` (not `init`, a reserved Go identifier); the
// directory + the kind keyword stay `init`.
#InitInput: {
	candy_field?: [...(string & !="")]
	candy_file?: [...(string & !="")]
	depends_candy?: string & !=""
	requires_capability?: [...(string & =~"^[a-z][a-z0-9_]*$")]

	// The one mandatory field: the build model.
	model: "fragment_assembly" | "file_copy"

	header_file?:    string & !=""
	fragment_dir?:   string & !=""
	relay_template?: string

	stage_name?:          string & !=""
	stage_header_copy?:   string
	stage_fragment_copy?: string

	assembly_template?:      string
	system_enable_template?: string
	post_assembly_template?: string

	entrypoint?: [...(string & !="")]
	fallback_entrypoint?: [...(string & !="")]

	management_tool?: string & !=""
	management_command?: {
		[string]: string & !=""
	}

	label_key?: string & =~"^ai\\.opencharly\\.service\\.[a-z0-9]+$"

	service_schema?: #InServiceSchema
}

#InServiceSchema: {
	service_template?:     string
	unit_path_template?:   string
	dropin_template?:      string
	dropin_path_template?: string
	supports_packaged?:    bool
}
