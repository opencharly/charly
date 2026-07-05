// This out-of-tree COMMAND plugin's OWN CUE schema, served over the Describe channel.
//
// preempt is a COMMAND-class plugin: charly dispatches it by fork/exec'ing this binary in CLI
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

// #PreemptPlugin documents the command the plugin serves. The plugin OWNS the status/restore grammar +
// the lease-table formatting and reaches its peer verb:arbiter via InvokeProvider — there is no
// plugin_input to validate here (args are plain CLI tokens).
#PreemptPlugin: {
	command:  "preempt"
	contract: "preempt is a compiled-in command dispatched in-proc via Invoke(OpRun); args are plain CLI tokens (status | restore [claimant]); the plugin owns the grammar + output and reaches its peer verb:arbiter over InvokeProvider"
}
