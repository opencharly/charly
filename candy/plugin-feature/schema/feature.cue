// This COMMAND plugin's OWN CUE schema, served over the Describe channel.
//
// feature is a COMMAND-class plugin (command:feature). It is COMPILED-IN (charly.yml
// compiled_plugins): NewMeta advertises command:feature and the compiled-in registry path
// (registerCompiledPlugin → resolve(ClassCommand,"feature") → dispatchInProcCommand) dispatches
// its Invoke(OpRun) IN-PROCESS. The command's args are plain CLI tokens (list [kind] |
// pending [entity] | validate [entity]) — the pass-through argv, not a structured plugin_input —
// so this served schema carries no #*Input def; it exists ONLY to satisfy the host's "every
// plugin MUST ship a non-empty, base-splicing CUE schema" load gate (registerPluginUnitSchema)
// and the params codegen loop (task cue:gen).
//
// SELF-CONTAINED (carries no package clause and references NO base def): it compiles STANDALONE
// (the SDK serve-side check) AND splices onto the base — the base ++ plugin splice exists to
// detect a def-name collision with the base, not to resolve base references.

// #FeaturePlugin documents the command the plugin serves. The command keeps its entire contract
// in its own CLI grammar (parsed from the pass-through argv), so there is no plugin_input to
// validate here.
#FeaturePlugin: {
	command:  "feature"
	contract: "feature is a compiled-in command:feature plugin dispatched in-proc via the provider registry; args are plain CLI tokens (list [kind] | pending [entity] | validate [entity]). The plugin owns the list/pending/validate grammar + output; the unified loader (LoadConfig / ScanCandy) + Step plan model + validatePlanSteps stay core and are reached via the generic \"feature\" HostBuild seam."
}
