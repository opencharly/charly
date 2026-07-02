// The BUILT-IN `package` plugin's OWN CUE schema — the typed plugin_input for the
// `package` verb (rpm -q / dpkg -s / pacman -Q presence probe in do:assert; INSTALL
// the package in do:act). It is the SINGLE SOURCE for this plugin's params, used
// two ways (the same contract the reference examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `package` step's plugin_input against #PackageInput.
//
// SELF-CONTAINED: every field is a bare primitive / list / map referencing NO base
// def, so it compiles standalone (gengotypes + the load-gate compile) AND splices onto
// the base — the base ++ plugin splice exists to detect a def-name collision with the
// base, not to resolve base refs.
//
// `package` is a STATE-PROVISION verb like service, but the TYPED-STEP form WITHOUT
// service's PriorEnabled teardown-restore state. It is BOTH a CheckVerbProvider
// (RunVerb → r.runPackage, the rpm/dpkg/pacman probe that keeps the live *Runner) AND
// a TypedStepProvider — its do:act half lowers into a TYPED SystemPackagesStep whose
// Reverse() records the LOAD-BEARING reversals (ReverseOpPackageRemove +
// ReverseOpCoprDisable for a copr repo). A RenderProvisionScript shell string would
// DROP those reversals, so unlike user/unix_group/kernel-param/mount (RenderProvisionScript
// verbs) the build/deploy install timeline constructs the typed step (compileActOp →
// TypedStepProvider.ConstructStep), not an OpStep. The provider ALSO keeps a
// RenderProvisionScript (the runtime/opt-in live act path via resolveProvisionScript)
// for a `run: {plugin: package}` step the check Runner executes.
//
// `package`/`installed`/`version`/`package_map` were base #Op fields read ONLY by the
// `package` verb (resolvePackageName + runPackage), so all four MOVE here when `package`
// extracts and leave #Op entirely. The SHARED `exclude_distro` modifier (read by the
// generic runOne skip filter for EVERY verb) is NOT here — it stays at step level on #Op.
#PackageInput: {
	// package — the package name the probe queries (assert) / the typed
	// SystemPackagesStep installs (act). The verb discriminator. Cross-distro-resolved
	// against package_map (the first image distro tag that matches a map key wins).
	package: string @go(Package)
	// installed — optional expected install state (default true). Tri-state pointer so
	// an absent key means "expected installed".
	installed?: bool @go(Installed,type=*bool)
	// version — optional exact-version allow-list. When set, the probe pulls the
	// installed version string and asserts it is one of these.
	version?: [...string] @go(Versions)
	// package_map — optional distro-specific package-name overrides (distro tag →
	// package name). The first of the image's distro tags that matches a key wins.
	package_map?: {[string]: string} @go(PackageMap)
}
