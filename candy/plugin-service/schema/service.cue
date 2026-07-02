// The BUILT-IN `service` plugin's OWN CUE schema — the typed plugin_input for the
// `service` verb (supervisorctl/systemctl probe in do:assert; ENABLE the named
// packaged unit in do:act). It is the SINGLE SOURCE for this plugin's params, used
// two ways (the same contract the reference examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `service` step's plugin_input against #ServiceInput.
//
// SELF-CONTAINED: every field is a bare primitive referencing NO base def, so it
// compiles standalone (gengotypes + the load-gate compile) AND splices onto the base
// — the base ++ plugin splice exists to detect a def-name collision with the base,
// not to resolve base refs.
//
// `service` is the TYPED-STEP-OUTLIER state-provision verb. It is BOTH a
// CheckVerbProvider (RunVerb → r.runService, the supervisorctl/systemctl probe that
// keeps the live *Runner) AND a TypedStepProvider — its do:act half lowers into a
// TYPED ServicePackagedStep whose Reverse() records the LOAD-BEARING reversals
// (ReverseOpServiceDisable / RestoreEnabled / RemoveDropin). A RenderProvisionScript
// shell string would DROP those reversals, so unlike user/unix_group/kernel-param/
// mount (RenderProvisionScript verbs) the build/deploy install timeline constructs the
// typed step (compileActOp → TypedStepProvider.ConstructStep), not an OpStep. The
// provider ALSO keeps a RenderProvisionScript (the runtime/opt-in live act path via
// resolveProvisionScript) for a `run: {plugin: service}` step the check Runner executes.
//
// `service`/`running`/`enabled` were base #Op fields read ONLY by the `service` verb
// (process reproduced `running` standalone in #ProcessInput and reads its own
// plugin_input — never #Op.running), so all three MOVE here when `service` extracts and
// leave #Op entirely. The probe asserts running/enabled when set; the act enables the
// `service` unit.
#ServiceInput: {
	// service — the unit name the probe queries (assert) / the typed ServicePackagedStep
	// enables (act). The verb discriminator.
	service: string @go(Service)
	// running — optional expected run state (supervisorctl RUNNING / systemctl
	// is-active). Tri-state pointer so an absent key skips the running probe.
	running?: bool @go(Running,type=*bool)
	// enabled — optional expected enable state (systemd is-enabled / supervisord
	// presence). Tri-state pointer so an absent key skips the enabled probe.
	enabled?: bool @go(Enabled,type=*bool)
}
