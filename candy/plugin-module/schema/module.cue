// The BUILT-IN `module` plugin's OWN CUE schema — the typed input for the `module`
// KIND (the Calamares installer module / module.desc, formerly a core `module:` kind
// decoded into a typed core map). It is the SINGLE SOURCE for this plugin's params,
// used two ways (the same contract the reference exampleprobe/process, the
// package-group plugin, and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `module:` entity body against #ModuleInput BEFORE runPluginKind
//     dispatches (validateAuthoredPluginInput(ClassKind, "module", …)) — the
//     kind-class analogue of the verb plugin_input gate.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base (the base ++ plugin splice exists
// to detect a def-name collision with the base, not to resolve base refs). It is a
// faithful reproduction of the core #Module (schema/module.cue) — the same authored
// WIRE keys, so the host validates a real module entity, and the plugin's Invoke
// canonicalises the body back through the core spec.Module type.
//
// NAME: in node-form the entity name is the top-level node KEY, not a body field, so
// the assembled entity body NEVER carries `name`; #ModuleInput therefore makes it
// OPTIONAL (the core #Module requires it; concrete validation of the always-absent
// node-form name would otherwise fail).
#ModuleInput: {
	name?:        string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	type?:        "job" | "view"
	interface?:   "qtplugin" | "python" | "process"
	load?:        string & !=""
	script?:      string & !=""
	command?: [...(string & !="")]
	requiredModules?: [...(string & !="")]
	weight?:   int & >=0
	noconfig:  *false | bool
	emergency: *false | bool
	timeout?:  int & >=0
	chroot:    *false | bool
}
