// This out-of-tree COMMAND plugin's OWN CUE schema, served over the Describe channel.
//
// settings is a COMMAND-class plugin: charly dispatches it by fork/exec'ing this binary in CLI
// mode (sdk.Main → cliMain), NOT through the gRPC provider registry — so the plugin advertises
// NO gRPC capability and NO plugin_input (the command's args are plain CLI tokens parsed from
// os.Args in CLI mode, not a structured plugin_input). This served schema therefore carries no
// #*Input def; it exists ONLY to satisfy the host's "every plugin MUST ship a non-empty,
// base-splicing CUE schema" load gate (registerPluginUnitSchema) and the params codegen loop
// (task cue:gen).
//
// SELF-CONTAINED (carries no package clause and references NO base def): it compiles STANDALONE
// (the SDK serve-side check) AND splices onto the base — the base ++ plugin splice exists to
// detect a def-name collision with the base, not to resolve base references.

// #SettingsPlugin documents the command the plugin serves. The command keeps its entire contract
// in the in-core SettingsCmd grammar (the `charly __settings` get/set/list/path/reset tree it
// raw-forwards to), so there is no plugin_input to validate here.
#SettingsPlugin: {
	command:  "settings"
	contract: "settings is CLI-dispatched (charly fork/execs the binary); args are plain CLI tokens that raw-forward to the in-core runtime-config get/set/list/path/reset command tree via charly __settings"
}
