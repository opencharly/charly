package spec

// ir_enums.go — the InstallPlan IR discriminator enums, shared between charly's
// core (package main, via the `= spec.X` alias surface) and any plugin that walks
// the IR. These are INTERNAL execution enums, NOT wire carriers: the #InstallStepView
// wire struct (schema/deploy.cue) serializes them as primitives (`Venue int`,
// `Gate string`, `Kind string`), so the wire-type-CUE mandate does not apply — and Venue/Phase are
// int iota enums that `cue exp gengotypes` cannot faithfully generate anyway. They
// live in package spec (the one importable home) so the IR is importable without
// charly core. Hand-written, matching the ReverseOpKind precedent (spec/deploy_consts.go).

// ---------------------------------------------------------------------------
// Venue — where a step executes.
// ---------------------------------------------------------------------------

type Venue int

const (
	// VenueHostNative runs commands directly on the host (or as a RUN in
	// the Containerfile for the container target). The step's shell
	// commands execute natively; no isolation container.
	VenueHostNative Venue = iota

	// VenueContainerBuilder runs the step inside the existing multi-stage
	// builder image (fedora-builder / arch-builder / ...). On the
	// host target this means `podman run --user <host-uid> -v <paths>
	// <builder> bash -c "..."`. On the OCI target this is a FROM + RUN
	// pair that becomes its own build stage, with copy_artifacts pulling
	// outputs into the final image.
	VenueContainerBuilder

	// VenueSkip records the step with a reason but doesn't execute. Used
	// for container-runtime-only fields (ports:, volumes:, tunnel:, …)
	// when compiling for the host target, and for aur: on non-Arch hosts.
	VenueSkip
)

func (v Venue) String() string {
	switch v {
	case VenueHostNative:
		return "host-native"
	case VenueContainerBuilder:
		return "container-builder"
	case VenueSkip:
		return "skip"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Phase — three-phase execution within each step kind.
// ---------------------------------------------------------------------------

// Phase identifies which template phase (prepare / install / cleanup) a
// rendered step belongs to. The phase lets targets treat repo-config
// mutation (prepare) distinctly from package install — which is exactly
// the granularity --allow-repo-changes gates. A single format section in
// the candy manifest typically emits three PhasePrepare/PhaseInstall/PhaseCleanup
// steps, and the compiler tags each with the appropriate phase.
type Phase int

const (
	PhasePrepare Phase = iota
	PhaseInstall
	PhaseCleanup
)

func (p Phase) String() string {
	switch p {
	case PhasePrepare:
		return "prepare"
	case PhaseInstall:
		return "install"
	case PhaseCleanup:
		return "cleanup"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// StepKind — discriminator for InstallStep implementations.
// ---------------------------------------------------------------------------

// StepKind names the concrete type behind an InstallStep. Used for
// ledger serialization and target-specific dispatch (e.g. HostDeploy
// knows to invoke the pixi builder differently than cargo).
type StepKind string

const (
	StepKindSystemPackages  StepKind = "SystemPackages"
	StepKindBuilder         StepKind = "Builder"
	StepKindOp              StepKind = "Op"
	StepKindFile            StepKind = "File"
	StepKindServicePackaged StepKind = "ServicePackaged"
	StepKindServiceCustom   StepKind = "ServiceCustom"
	StepKindShellHook       StepKind = "ShellHook"
	StepKindShellSnippet    StepKind = "ShellSnippet"
	StepKindRepoChange      StepKind = "RepoChange"
	StepKindApkInstall      StepKind = "ApkInstall"
	StepKindLocalPkgInstall StepKind = "LocalPkgInstall"
	StepKindReboot          StepKind = "Reboot"
	StepKindExternalPlugin  StepKind = "ExternalPlugin"
	StepKindExtract         StepKind = "Extract"
)

// ---------------------------------------------------------------------------
// Gate — opt-in flag names for host-state-mutating operations.
// ---------------------------------------------------------------------------

// Gate is the name of a CLI flag that must be enabled for a given step to
// run on the host target. Steps without a gate run unconditionally; steps
// with a gate are skipped (with a warning) unless the user opts in.
//
// The OCI target ignores gates — container builds are already isolated,
// so gating would only slow down image construction without adding safety.
type Gate string

const (
	GateNone             Gate = ""
	GateAllowRepoChanges Gate = "allow-repo-changes"
	GateAllowRootTasks   Gate = "allow-root-tasks"
	GateWithServices     Gate = "with-services"
)
