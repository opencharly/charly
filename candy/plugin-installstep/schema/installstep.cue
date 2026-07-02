// schema/installstep.cue — the (vestigial) CUE schema for the compiled-in class:step build-emit
// plugin. The InstallStep kinds it serves (file / shell-hook / shell-snippet / service-packaged /
// service-custom / repo-change / apk-install / reboot / system-packages / builder / local-pkg-install /
// op) are COMPILER-EMITTED from
// declarative candy fields (copy:/env:/service:/package:/pixi.toml/Cargo.toml/reboot:/…), never authored as
// a `plugin:` step, so
// NO capability declares an InputDef. This def exists ONLY to satisfy the plugin load gate (every
// plugin MUST ship a non-empty CUE schema that splices onto charly's base). The OpEmit payload is
// a spec.InstallStepView (the compiler's step serialization), decoded directly in Go.
#InstallStepBuildEmit: {
	// Placeholder — the real OpEmit payload is spec.InstallStepView, not an authored plugin_input.
	kind?: string
}
