// This out-of-tree COMMAND plugin's OWN CUE schema, served over the Describe channel.
//
// doctor is a COMPILED-IN COMMAND-class plugin: charly dispatches it in-proc via
// dispatchInProcCommand → Invoke(OpRun) (advertising command:doctor over Describe), NOT with a
// structured plugin_input (the command's args are plain CLI tokens). This served schema therefore
// carries no #*Input def; it exists ONLY to satisfy the host's "every plugin MUST ship a non-empty,
// base-splicing CUE schema" load gate (registerPluginUnitSchema) and the params codegen loop
// (task cue:gen).
//
// SELF-CONTAINED (carries no package clause and references NO base def): it compiles STANDALONE
// (the SDK serve-side check) AND splices onto the base — the base ++ plugin splice exists to
// detect a def-name collision with the base, not to resolve base references.

// #DoctorPlugin documents the command the plugin serves. The plugin OWNS the command (flag grammar +
// the entire host-dependency report + output); the genuine host-hardware subsystem (GPU/VFIO/device
// detection + credentialHealth + the core install-hint/device tables) stays in core, reached over the
// HostBuild("hostprobe") seam — there is no plugin_input to validate here (args are plain CLI tokens).
#DoctorPlugin: {
	command:  "doctor"
	contract: "doctor is a compiled-in command dispatched in-proc via dispatchInProcCommand → Invoke(OpRun); args are plain CLI tokens; the plugin owns the flags + the whole host-dependency report + output and reaches the genuine host-hardware detection primitives through the HostBuild(\"hostprobe\") seam"
}
