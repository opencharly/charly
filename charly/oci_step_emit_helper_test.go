package main

// oci_step_emit_helper_test.go — the Go-object-typed convenience the step-emit unit tests drive.
//
// It lived in oci_step_emit.go as a "compatibility wrapper" with ZERO production callers, which
// made three separate production comments describe the production chain as ending in it. It does
// not: production is HostBuild("step-emit","oci-emit-step") → stepEmitOCIEmitStep →
// dispatchOCIStep. Moving it here makes its status legible — it is scaffolding that converts
// concrete spec.InstallStep/spec.InstallPlan values into the wire views the production caller
// already sends, then forwards through the IDENTICAL seam. Not a parallel implementation (R3).

import "github.com/opencharly/spec/spec"

// ociEmitStep is a Go-object-typed compatibility wrapper over dispatchOCIStep: it serializes step
// and plan to their wire views (the SAME serialization the production "oci-emit-step" caller
// already sends) and forwards through the identical seam. Kept for the existing unit-test suite
// (apk_format_test.go, build_target_oci_test.go, localpkg_test.go, plugin_externalstep_e2e_test.go),
// which drives ociEmitStep with concrete spec.InstallStep/spec.InstallPlan values rather than
// pre-marshaled wire views — a real, non-mocked path through the SAME relocated dispatch, not a
// parallel implementation (R3).
func ociEmitStep(step spec.InstallStep, plan *spec.InstallPlan, distros []string, build buildEngineContext) (string, error) {
	return dispatchOCIStep(spec.StepToView(step), spec.WireView(plan), distros, build)
}
