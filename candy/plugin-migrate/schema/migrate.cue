// This COMPILED-IN command plugin's OWN CUE schema, served over the Describe channel.
//
// migrate is a COMMAND-class plugin: charly dispatches command:migrate either in-proc
// (Invoke OpRun — the operator `charly migrate` + refs.go remote-cache auto-migration) or,
// when NOT compiled-in, by fork/exec'ing the cmd/serve binary in CLI mode (sdk.Main →
// CliMain). Either way the command keeps its whole contract in its own CLI grammar
// (--dry-run / --project-only / --quiet / --dir), so it advertises NO plugin_input.
// This served schema therefore carries no #*Input def; it exists ONLY to satisfy the host's
// "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate.
//
// SELF-CONTAINED (no package clause, references NO base def): it compiles STANDALONE (the SDK
// serve-side check) AND splices onto the base to detect a def-name collision with the base.

// #MigratePlugin documents the command the plugin serves. The migration engine (the CUE-anchored
// declarative table + generic op-walker) lives in the plugin's Go; there is no plugin_input.
#MigratePlugin: {
	command:  "migrate"
	contract: "migrate is command-dispatched; args are plain CLI tokens (--dry-run | --project-only | --quiet | --dir <path>) driving the CUE-anchored declarative migration engine — bring any current-format config up to the head schema CalVer, refuse below-floor"
}
