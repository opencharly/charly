package main

// install_plan.go — the InstallPlan IR.
//
// Background (see plan file referenced in the final design): today's code
// walks Candy objects and emits Containerfile text directly in
// generate.go:writeCandySteps. That hardcodes "we're building an OCI image"
// into the generator. The IR defined here lifts the walk into structured
// data so the same plan can be consumed by:
//
//   - deploykit.OCITarget        → deploy-mode pod-overlay (add_candy) Containerfile emission (charly bundle add <name>)
//   - ContainerDeploy  → deploy-mode overlay + quadlet (charly bundle add <name>)
//   - the local deploy target → deploy-mode host execution (charly bundle add host)
//
// `charly box build`/`generate` do NOT consume this IR — they emit Containerfile
// text directly via generate.go writeCandySteps→emitTasks. The IR is deploy-only.
//
// Keeping these three code paths behind one shared IR is the load-bearing
// move: every feature (service rendering, add_candy overlay, uninstall
// reversal) now lives in one place and applies to all three targets
// uniformly.
//
// This file defines only types and interfaces — no logic. The compiler that
// turns the candy manifest → InstallPlan lives in install_build.go; the emitters live
// in build_target_oci.go / deploy_target_pod.go / deploy_host_helpers.go.

import (
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// HomeToken is the deferred-home placeholder the compiler bakes into
// home-bearing step fields (env.d values, path_append entries, shell-snippet
// destinations) instead of expanding `~`/`$HOME` against a compile-time home.
// Each DeployTarget resolves it at emit time via InstallPlan.ResolveHome with
// the home of the ACTUAL destination — img.Home for the OCI/pod-overlay build,
// the host home for the external local deploy, the GUEST home for the external vm deploy
// (resolved host-side in externalDeployTarget.apply via the guest executor). This
// is what lets a `target: vm` deploy write env.d that points at the guest
// user's home (/home/<guest-user>) rather than the host operator's home.
// The `{{.Home}}` spelling matches the existing builder-artifact convention
// (deploykit expandBuilderPath), so the two token systems stay aligned.

// Scope / Venue / Phase / StepKind / Gate / ReverseOpKind / ReverseOp / InstallStep are the
// InstallPlan IR's discriminator enums + step interface, all homed natively in sdk/spec
// (deploy_wire.go / ir_enums.go) — referenced below as spec.Scope / spec.Venue / etc.
// (K3 ZERO-ALIASES dissolution — an alias is always residue regardless of what it aliases).
// They live in spec because an out-of-process deploy/step/builder plugin (through the SDK)
// constructs/returns them across the process boundary; package main and the SDK share ONE
// type (R3). Venue/Phase are int iota enums cue exp gengotypes cannot express, so they are
// hand-written in spec, not CUE-sourced.

// externalStep is an EXTERNAL, plugin-CONTRIBUTED install-step KIND (F3, closes C1): a step
// whose Kind() is "external:<word>", carried OPAQUELY (Payload) and whose Scope/Venue/Gate
// come from the serving class:step plugin's DECLARED StepContract (Describe), NOT from a
// compiled-in Go case. It is the generalization ExternalPluginStep is NOT: ExternalPluginStep
// wraps a VERB Op in the ONE fixed "ExternalPlugin" kind with a Go-fixed (advisory) contract;
// externalStep is a first-class per-word kind whose contract the PLUGIN declares — the carrier
// M2 needs to externalize the builtin step kinds (the compiler emits e.g. external:system-packages
// with a package-list Payload). Its host EXECUTION funnels through the SAME OpExecute-to-the-
// serving-plugin path ExternalPluginStep uses (dispatchExternalStepOp — R3); teardown ops are
// stepContract is a class:step plugin's DECLARED install-step contract (F3), decoded from its
// Describe capability (pb.StepContract / sdk.StepContract). compileActOp reads it (via the
// stepContractCarrier a provider implements) to build an externalStep carrying the
// plugin-declared Scope/Venue/Gate — the contract the host applies via the open default arm
// with NO compiled-in case.
type stepContract struct {
	Scope spec.Scope
	Venue spec.Venue
	Gate  spec.Gate
	// Emits is the F-STEP-EMIT flag: the step produces a build-context Containerfile
	// FRAGMENT (the serving plugin answers Invoke(OpEmit) → spec.EmitReply.Fragment).
	// The pod-overlay deploykit.OCITarget consults it via the open external-step arm — Emits=true →
	// bake the fragment; Emits=false → skip (a deploy-only external step, like apk on an
	// image build). Advisory for the DEPLOY leg (executeExternalStep ignores it); load-bearing
	// for the BUILD leg (ociEmitStep).
	Emits bool
}

// stepContractCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:step capability's declared StepContract. A nil/false
// return means the provider declares no step contract (every non-step capability).
type stepContractCarrier interface {
	declaredStepContract() (stepContract, bool)
}

// structuralKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's STRUCTURAL flag (F5). true → the kind's
// OpLoad returns a spec.Deploy member tree the host folds into uf.Bundle; false (or not
// implemented) → the flat F4 path (opaque body → uf.PluginKinds).
type structuralKindCarrier interface {
	isStructuralKind() bool
}

// validatingKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's VALIDATES flag (F7/C8). true → the host
// dispatches OpValidate to the kind at load (a deep plugin-owned check returning spec.Diagnostics,
// beyond the static CUE input-def gate); false (or not implemented) → only the static gate runs.
type validatingKindCarrier interface {
	isValidatingKind() bool
}

// deployTraitsCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a SUBSTRATE class:kind capability's DECLARED #DeployTraits (P9).
// Non-nil → deployTraitsFor returns them so kit.StampDescent stamps node.Descent BY TRAIT; nil
// (or not implemented) → the external-in-place default. This is the SINGLE plugin-declared
// source for a substrate's deploy behaviour — the consult sites read the stamped traits off
// node.Descent, never switching on the substrate kind word (the kernel/plugin boundary law).
type deployTraitsCarrier interface {
	declaredDeployTraits() *spec.DeployTraits
}

// phaseCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider compiled-in)
// that carries its declared lifecycle PHASE (F9). A provider not implementing it (e.g. a builtin
// non-plugin provider) is treated as PhaseRuntime by phaseOfProvider.
type phaseCarrier interface {
	pluginPhase() string
}

// phaseOfProvider returns a provider's lifecycle phase (F9), defaulting to sdk.PhaseRuntime for a
// provider that declares none / is not a phaseCarrier.
func phaseOfProvider(p Provider) string {
	if pc, ok := p.(phaseCarrier); ok {
		if ph := pc.pluginPhase(); ph != "" {
			return ph
		}
	}
	return sdk.PhaseRuntime
}

// scopeFromName maps a declared scope NAME (the author-friendly form a class:step plugin ships
// in its StepContract) to the internal Scope. Unknown / "system" → ScopeSystem (the safe
// default — an external step's scope is advisory for the self-exec'ing plugin, used for ledger
// + batching provenance, not host sudo-wrapping).

// ---------------------------------------------------------------------------
// InstallPlan — the top-level IR container.
// ---------------------------------------------------------------------------

// InstallPlan is the full ordered list of steps for one candy or one
// whole-image deploy. Compiled by BuildDeployPlan and consumed by any
// DeployTarget implementation.
//
// The compiler produces one InstallPlan per candy (then merges them in
// topological order for whole-image deploys). A whole-image deploy keeps
// candy boundaries visible so the ledger can refcount which candies
// participate in which deploys — crucial for correct uninstall.
