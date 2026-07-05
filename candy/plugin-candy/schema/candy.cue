// This out-of-tree COMMAND plugin's OWN CUE schema, served over the Describe channel.
//
// candy is a COMPILED-IN COMMAND-class plugin: charly dispatches it in-proc via
// dispatchInProcCommand → Invoke(OpRun) (advertising command:candy over Describe), NOT with a
// structured plugin_input (the command's args are plain CLI tokens). This served schema therefore
// carries no
// #*Input def; it exists ONLY to satisfy the host's "every plugin MUST ship a non-empty,
// base-splicing CUE schema" load gate (registerPluginUnitSchema) and the params codegen loop
// (task cue:gen).
//
// SELF-CONTAINED (carries no package clause and references NO base def): it compiles STANDALONE
// (the SDK serve-side check) AND splices onto the base — the base ++ plugin splice exists to
// detect a def-name collision with the base, not to resolve base references.

// #CandyPlugin documents the command the plugin serves — the TOP-LEVEL `charly candy` authoring
// tree, NOT `charly new candy`. The plugin OWNS the entire logic (the set/add-{rpm,deb,pac,aur}
// grammar + the comment-preserving yaml.Node mutation), sharing only the generic kit.SetByDotPath /
// kit.MappingChild utilities — there is no plugin_input to validate here (args are plain CLI tokens).
#CandyPlugin: {
	command:  "candy"
	contract: "candy is a compiled-in command dispatched in-proc via dispatchInProcCommand → Invoke(OpRun); args are plain CLI tokens; the plugin owns the set/add-{rpm,deb,pac,aur} grammar + the yaml.Node candy-manifest mutation itself, sharing only the generic kit.SetByDotPath / kit.MappingChild yaml utilities"
}
