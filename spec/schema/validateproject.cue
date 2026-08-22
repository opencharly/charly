// CUE schema for the two `charly box validate` wire channels.
//
// #ValidateProjectRequest / #ValidateProjectReply are the ENVELOPE channel: candy/plugin-box's
// runValidateEngine asks candy/plugin-build's `build:project` word (ops.OpValidate) for the
// error-TOLERANT resolved-project projection — a box that fails to resolve becomes a diagnostic,
// not a fatal abort, because validate MUST run on broken projects — and runs every pure per-kind/op
// rule, the deploykit resolution-graph checks, the raw-config rules, AND (since K-wave 2 cone R1
// unit B) the CUE-schema conformance + remote-candy rules over it. #ValidateProjectRequest is also
// the pre-build GATE's op payload (InvokeProvider(command:validate, ops.OpValidate)).
//
// #ValidateWordSetsRequest / #ValidateWordSetsReply are the REGISTRY-D channel: the ONE thing the
// validate plugin genuinely cannot answer for itself, because the provider registry is a kernel
// M-mechanism. It replaced a far fatter host-checks seam that made the host RE-LOAD and RE-SCAN the
// whole project purely to re-derive data the plugin already held — the boundary law's named
// re-derivation R-pattern. Now the plugin SENDS its own inventory and the host answers
// registry-only, with no load, no scan, and no host-side diagnostics.
//
// Package-less; concatenated into the spec compilation unit. #Diagnostics is the shared wire type
// (deploy.cue), referenced by @go so the reply carries it without redefining it.

// #ValidateProjectRequest — which project dir to validate (empty = the host's cwd) + whether to
// include enabled:false boxes. Mirrors #ResolvedProjectRequest (the sibling resolved-project seam).
#ValidateProjectRequest: {
	dir?:              string @go(Dir)
	include_disabled?: bool   @go(IncludeDisabled)
}

// #ValidateProjectReply — the error-TOLERANT #ResolvedProject projection (partial; only boxes and
// candies that resolved) + the per-box resolve-failure diagnostics. The plugin merges these with
// its own pure-rule, raw-config, CUE-conformance and resolution-graph findings for the verdict.
#ValidateProjectReply: {
	project?:     #ResolvedProject @go(Project,optional=nillable)
	diagnostics?: {...} @go(Diagnostics,type=Diagnostics)
}

// #ValidateWordSetsRequest — the plugin-supplied inventory the host answers registry-D over.
//
// plugin_words are the DISTINCT `plugin:` verb words the plugin enumerated from its OWN envelope
// (every candy model's plan plus every box plan); the host reports which of them ACT in
// build/deploy. external_providers are the `plugin.providers:` capability strings ("<class>:<word>")
// of every candy whose `plugin.source:` is a real out-of-tree ref — the host must LEARN those
// declarations before it can answer, because a declared-but-not-yet-connected external verb/step is
// act-capable by declaration alone (there is no provider to resolve until the build connects it).
#ValidateWordSetsRequest: {
	plugin_words?: [...string] @go(PluginWords)
	external_providers?: [...string] @go(ExternalProviders)
}

// #ValidateWordSetsReply — the two registry-derived D-data word sets the validate rules consume as
// membership sets. provider_capabilities is every compiled-in provider as "<class>:<word>" (the
// TARGET set a `source: builtin` plugin candy's declared providers must be a member of);
// act_capable_verbs is the subset of the request's plugin_words whose act form has a build/deploy
// install path (the check act-form rule).
#ValidateWordSetsReply: {
	provider_capabilities?: [...string] @go(ProviderCapabilities)
	act_capable_verbs?: [...string] @go(ActCapableVerbs)
}
