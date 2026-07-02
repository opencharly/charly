// The BUILT-IN `target` plugin's OWN CUE schema — the typed input for the `target`
// KIND (the Calamares install target / settings.conf, formerly a core `target:` kind
// decoded into the typed core map uf.Target). SINGLE SOURCE for this plugin's params,
// used two ways (the same contract the package-group/agent/module/sidecar/distro/
// builder/init/resource plugins and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (task cue:gen) →
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — served over Describe (InProcTransport),
//     spliced base ++ plugin, every authored `target:` body validated against
//     #TargetInput BEFORE runPluginKind dispatches.
//
// SELF-CONTAINED: the core #Target (schema/target.cue) references NO _common.cue def,
// so this is a verbatim reproduction with the inner #TargetInstance / #TargetSequence
// renamed #TgInstance / #TgSequence (prefix #Tg) and the @go() annotations dropped (CORE
// codegen only). The plugin's Invoke canonicalises the body back through the core
// spec.Target type (which TargetSpec aliases). Calamares has no on-disk corpus yet
// (importers/emitters deferred), so the kind has zero core readers — there is no
// Targets() accessor, exactly like the zero-reader module/package-group kinds.
//
// NAME: in node-form the entity name is the node KEY, not a body field, so the assembled
// entity body NEVER carries `name`; the core #Target's `name` field is therefore made
// OPTIONAL here (a name-less body must pass the concrete gate — the former lenient
// decodeNodeValue path never required it; making it required would add strictness the
// core kind never enforced).
#TargetInput: {
	name?:        string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	"modules-search"?: [...(string & !="")]
	instance?: [...#TgInstance]
	sequence?:        #TgSequence
	branding?:        string & !=""
	"prompt-install": *false | bool
	"dont-chroot":    *false | bool
	"oem-setup":      *false | bool
	"disable-cancel": *false | bool
	group?: [...(string & !="")]
	box?: [...(string & !="")]
}

#TgInstance: {
	id:      string & !=""
	module:  string & !=""
	config?: string & !=""
}

#TgSequence: {
	show?: [...(string & !="")]
	exec?: [...(string & !="")]
}
