// The BUILT-IN `distro` plugin's OWN CUE schema — the typed input for the `distro`
// KIND (the per-distro build vocabulary, formerly a core `distro:` kind decoded into
// the typed core map uf.Distro). It is the SINGLE SOURCE for this plugin's params,
// used two ways (the same contract the reference exampleprobe/process, the
// package-group/agent/module/sidecar plugins, and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `distro:` entity body against #DistroInput BEFORE runPluginKind
//     dispatches (validateAuthoredPluginInput(ClassKind, "distro", …)) — the
//     kind-class analogue of the verb plugin_input gate.
//
// SELF-CONTAINED: it references NO base def — every shared shape (#CacheMount /
// #PhaseSet / #PhaseTemplates from _common.cue) is reproduced standalone here (prefix
// #Ds), so it compiles standalone (gengotypes + the load-gate compile) AND splices
// onto the base (the base ++ plugin splice exists to detect a def-name collision with
// the base, not to resolve base refs). It is a faithful reproduction of the core
// #Distro (schema/distro.cue) — the same authored WIRE keys, so the host validates a
// real distro entity (incl. the binary-embedded build vocabulary), and the plugin's
// Invoke canonicalises the body back through the core spec.Distro type (which DistroDef
// aliases and the generator/format code consumes via the Distros() accessor).
//
// NAME: in node-form the entity name is the top-level node KEY, not a body field, so
// the assembled entity body NEVER carries `name`; #Distro has no name field either, so
// there is nothing to make optional here. @go() annotations are dropped (they steer the
// CORE spec codegen only; the validator ignores attributes, the plugin Invoke decodes
// into spec.Distro, so the generated params struct's Go names are cosmetic for a builtin).
#DistroInput: {
	inherits?:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	inherit_packages?: bool
	version?:          string & =~"^[0-9]+(\\.[0-9]+)*$"
	bootstrap?:        #DsBootstrap
	workaround?: [...string]
	format?: {[string]: #DsFormat}
	base_user?:        #DsBaseUser
	pacstrap?:         #DsPacstrap
	debootstrap?:      #DsDebootstrap
	alpine_bootstrap?: #DsAlpineBootstrap
	bootloader?:       #DsBootloader
	dnf?:              #DsDnf
}

// install_cmd is the bootstrap command; ubuntu sets it to "" (kept WITHOUT
// `& !=""` so the empty-string base case validates).
#DsBootstrap: {
	install_cmd: string
	package?: [...string]
	cache_mount?: [...#DsCacheMount]
}

#DsFormat: {
	cache_mount?: [...#DsCacheMount]
	section_field?: {[string]: "list" | "list_of_maps"}
	install_template?:   string
	uninstall_template?: string
	phase?:              #DsPhaseSet
	validate?: [...#DsFormatRule]
	secondary?: bool
	local_pkg?: #DsLocalPkg
}

#DsFormatRule: {
	field: string & !=""
	rule:  string & !=""
}

#DsLocalPkg: {
	pkg_glob:           string & !=""
	source_sentinel:    string & !=""
	build_template:     string & !=""
	install_template:   string & !=""
	probe:              string & !=""
	dep_builder?:       string
	download_template?: string
}

#DsPacstrap: {
	base_package?: [...string]
	keyring_init_cmd?: string
	mirrorlist_url?:   string & =~"^https?://"
	extra_repo?: [...#DsPacstrapRepo]
	runtime_pacman_conf?: string
}
#DsPacstrapRepo: {
	name:      string & !=""
	server:    string & =~"^https?://"
	siglevel?: string
}
#DsDebootstrap: {
	suite?:      string
	mirror?:     string & =~"^https?://"
	variant?:    string
	components?: string
	include_package?: [...string]
	base_package?: [...string]
	extra_repo?: [...#DsDebootstrapRepo]
}
#DsDebootstrapRepo: {
	name:        string & !=""
	url:         string & =~"^https?://"
	suite?:      string
	components?: string
}
#DsAlpineBootstrap: {
	mirror_url?: string & =~"^https?://"
}
#DsBootloader: {
	install_template?:   string
	initramfs_template?: string
	fstab_template?:     string
}
#DsBaseUser: {
	name: string & !=""
	uid:  int & >=0
	gid:  int & >=0
	home: string & =~"^/"
}
#DsDnf: {
	max_parallel_downloads?: int & >=1
	fastestmirror?:          bool
}

// reproduces #CacheMount (schema/_common.cue) standalone.
#DsCacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
}

// reproduces #PhaseSet / #PhaseTemplates (schema/_common.cue) standalone.
#DsPhaseSet: {
	prepare?: #DsPhaseTemplates
	install?: #DsPhaseTemplates
	cleanup?: #DsPhaseTemplates
}
#DsPhaseTemplates: {
	container?: string
	host?:      string
}
