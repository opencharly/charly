package spec

// deploy_methods.go — pure Go METHODS and CONVERTER FUNCTIONS on/for the
// CUE-generated deploy-wire types (sdk/schema/deploy.cue, seam.cue ->
// spec/cue_types_gen.go). `cue exp gengotypes` has no construct for a method
// or a hand conversion function — it generates ONLY field shapes — so these
// stay hand-written here, mirroring Op.Kind() in spec/charly_methods.go: a
// method, not a type. Ported verbatim from the former hand-written
// deploy_wire.go (deleted by the SDD conversion).

// HasErrors reports whether any item is error-severity (empty severity counts
// as error).
func (d Diagnostics) HasErrors() bool {
	for _, it := range d.Items {
		if it.Severity != "warning" {
			return true
		}
	}
	return false
}

// LifecycleOptsFromEmit projects an EmitOpts (spec/deploy_executor.go — a
// LIVE behaviour-contract type that cannot itself cross the wire, since
// EmitOpts.ParentExec carries a live DeployExecutor) onto its wire-safe
// LifecycleOpts subset — the ONE shared conversion the deploy-dispatch
// envelope (S3b, #DeployTargetDispatchRequest.opts_json) uses on BOTH sides:
// charly core (unified_targets.go's Add/Update) builds this BEFORE marshaling
// into OptsJSON — never marshal a raw EmitOpts directly — and
// candy/plugin-fleet decodes OptsJSON directly into a LifecycleOpts, so no
// plugin-side conversion is needed at all.
func LifecycleOptsFromEmit(o EmitOpts) LifecycleOpts {
	return LifecycleOpts{
		DryRun:               o.DryRun,
		AllowRepoChanges:     o.AllowRepoChanges,
		AllowRootTasks:       o.AllowRootTasks,
		WithServices:         o.WithServices,
		AssumeYes:            o.AssumeYes,
		Verify:               o.Verify,
		Pull:                 o.Pull,
		SkipIncompatible:     o.SkipIncompatible,
		BuilderImageOverride: o.BuilderImageOverride,
	}
}
