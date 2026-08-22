package spec

// install_step.go — the InstallStep interface, the polymorphic element of the
// InstallPlan IR E-envelope. It lives in package spec (alongside the IR enums it
// returns + the InstallPlan container that holds a []InstallStep) so the IR is
// importable without charly core: an out-of-tree deploy/step plugin references
// spec.InstallStep. The 13 CONCRETE step structs that implement it are the step
// VOCABULARY (sdk/deploykit); they satisfy this interface structurally, so the
// interface is the base and the structs import it, never the reverse.

// InstallStep is one polymorphic step in an InstallPlan. Every concrete step
// kind (SystemPackages, Builder, Op, File, …) implements it; a DeployTarget
// walks a plan's steps and dispatches on the returned discriminators.
type InstallStep interface {
	// Kind returns the step's concrete type discriminator.
	Kind() StepKind

	// Scope classifies where the effect lands on the target filesystem.
	Scope() Scope

	// Venue classifies where the commands physically execute.
	Venue() Venue

	// RequiresGate names the opt-in flag that must be enabled, or
	// GateNone if the step can run unconditionally. Only consulted by
	// host-target emission; the OCI target ignores gates.
	RequiresGate() Gate

	// Reverse returns the teardown actions this step contributes to the
	// ledger. Called at install time (not at teardown) so the ledger
	// captures the exact reversal actions tied to the specific artifacts
	// created. Empty return value means no reversal is recorded (e.g.
	// phases that leave no state).
	Reverse() []ReverseOp
}
