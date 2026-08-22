package spec

// provider_carriers.go — the capability-carrier interfaces + StepContract (K4-C relocation from
// charly/install_plan.go, per the #118 kernel/plugin boundary-law classification — this content is
// tracked plan-residue, not permanent kernel fabric). A provider (compiled-in or out-of-process)
// OPTIONALLY implements one or more of these to expose plugin-declared capability metadata the
// host consults generically — never a per-kind switch, always a type-assertion against one of
// these small, capability-shaped contracts. Living in sdk/spec lets the wire-shaped data these
// carriers expose (StepContract, DeployTraits) travel with ONE definition shared by the host
// registry and the SDK/proto layer that decodes a plugin's Describe reply into it (R3). Exported
// method names are REQUIRED here (not the charly-side lowerCamel originals): an interface with an
// unexported method can only be satisfied by a type in the SAME package as the interface, and
// charly's provider wrapper types (package main) must satisfy these from across the package
// boundary.

// StepContract is a class:step plugin's DECLARED install-step contract (F3), decoded from its
// Describe capability (pb.StepContract / sdk.StepContract). compileActOp reads it (via
// StepContractCarrier) to build an externalStep carrying the plugin-declared Scope/Venue/Gate —
// the contract the host applies via the open default arm with NO compiled-in case.
type StepContract struct {
	Scope Scope
	Venue Venue
	Gate  Gate
	// Emits is the F-STEP-EMIT flag: the step produces a build-context Containerfile FRAGMENT
	// (the serving plugin answers Invoke(OpEmit) → EmitReply.Fragment). The pod-overlay
	// deploykit.OCITarget consults it via the open external-step arm — Emits=true → bake the
	// fragment; Emits=false → skip (a deploy-only external step, like apk on an image build).
	// Advisory for the DEPLOY leg (executeExternalStep ignores it); load-bearing for the BUILD
	// leg (ociEmitStep).
	Emits bool
}

// StepContractCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:step capability's declared StepContract. A false return
// means the provider declares no step contract (every non-step capability).
type StepContractCarrier interface {
	DeclaredStepContract() (StepContract, bool)
}

// StructuralKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's STRUCTURAL flag (F5). true → the kind's
// OpLoad returns a Deploy member tree the host folds into uf.Fleet; false (or not implemented)
// → the flat F4 path (opaque body → uf.PluginKinds).
type StructuralKindCarrier interface {
	IsStructuralKind() bool
}

// ValidatingKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's VALIDATES flag (F7/C8). true → the host
// dispatches OpValidate to the kind at load (a deep plugin-owned check returning Diagnostics,
// beyond the static CUE input-def gate); false (or not implemented) → only the static gate runs.
type ValidatingKindCarrier interface {
	IsValidatingKind() bool
}

// DeployTraitsCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a SUBSTRATE class:kind capability's DECLARED DeployTraits (P9).
// Non-nil → deployTraitsFor returns them so kit.StampDescent stamps node.Descent BY TRAIT; nil
// (or not implemented) → the external-in-place default. This is the SINGLE plugin-declared
// source for a substrate's deploy behaviour — the consult sites read the stamped traits off
// node.Descent, never switching on the substrate kind word (the kernel/plugin boundary law).
type DeployTraitsCarrier interface {
	DeclaredDeployTraits() *DeployTraits
}

// PhaseCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider compiled-in)
// that carries its declared lifecycle PHASE (F9). A provider not implementing it (e.g. a builtin
// non-plugin provider) is treated as PhaseRuntime by the host's phaseOfProvider.
type PhaseCarrier interface {
	PluginPhase() string
}
