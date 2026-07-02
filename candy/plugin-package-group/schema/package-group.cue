// The BUILT-IN `package-group` plugin's OWN CUE schema — the typed input for the
// `package-group` KIND (the Calamares-style netinstall package group, formerly a
// core `package-group:` kind decoded into a typed core map). It is the SINGLE SOURCE for
// this plugin's params, used two ways (the same contract the reference
// exampleprobe/process and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `package-group:` entity body against #PackageGroupInput BEFORE
//     runPluginKind dispatches (validateAuthoredPluginInput(ClassKind,
//     "package-group", …)) — the kind-class analogue of the verb plugin_input gate.
//
// SELF-CONTAINED: it references NO base def — every shared shape (#PgPackageItem,
// #PgDistroPackages, #PgAUR, #PgRepoBlock) is reproduced standalone here, so it
// compiles standalone (gengotypes + the load-gate compile) AND splices onto the base
// (the base ++ plugin splice exists to detect a def-name collision with the base, not
// to resolve base refs). It is a faithful reproduction of #Group (schema/group.cue):
// the same authored WIRE keys, so the host validates a real package-group entity.
//
// NAME: in node-form the entity name is the top-level node KEY, not a body field, so
// the assembled entity body NEVER carries `name`; #PackageGroupInput therefore makes
// it OPTIONAL (concrete validation would otherwise fail on the always-absent name).
#PackageGroupInput: {
	name?:        string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	package?: [...#PgPackageItem]
	distro?: {[string]: #PgDistroPackages}
	hidden:        *false | bool
	selected:      *false | bool
	critical:      *false | bool
	immutable:     *false | bool
	expanded:      *false | bool
	noncheckable:  *false | bool
	pre_install?:  string & !=""
	post_install?: string & !=""
	source?:       string & !=""
	subgroup?: [...#PackageGroupInput]
	require?: [...(string & !="")]
}

// bare scalar shorthand XOR object form (reproduces #PackageItem standalone).
#PgPackageItem: ((string & !="") | {
	name:         string & !=""
	description?: string & !=""
})

// reproduces #DistroPackages standalone.
#PgDistroPackages: {
	package?: [...#PgPackageItem]
	copr?: [...(string & !="")]
	repo?: [...#PgRepoBlock]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")]
	module?: [...(string & !="")]
	aur?: #PgAUR
}

// reproduces #AUR standalone.
#PgAUR: {
	package?: [...#PgPackageItem]
	option?: [...(string & !="")]
	replace?: [...(string & !="")]
}

// reproduces #RepoBlock standalone — `name` load-bearing, the rest pass through.
#PgRepoBlock: {
	name: string & !=""
	...
}
