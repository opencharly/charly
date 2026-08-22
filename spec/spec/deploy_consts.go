package spec

// deploy_consts.go — the deploy IR's named enum TYPES (SDD conversion of
// deploy_wire.go's non-wire-struct surface). HAND-WRITTEN, mirroring the
// spec/status_result.go precedent (Status: a distinct named int type + iota
// consts + a String() method — gengotypes has no construct for an iota-enum +
// Stringer) and the spec/gpu_consts.go / spec/arbiter_consts.go precedent
// (plain string-enum constants never carry a JSON/YAML shape for gengotypes to
// generate). The wire STRUCTS that reference these types (ReverseOp.Scope via
// @go(Scope,type=Scope); ReverseOp.Kind via @go(Kind,type=ReverseOpKind);
// InstallStepView.Scope/TargetScope likewise) are CUE-sourced at
// sdk/schema/deploy.cue -> generated into cue_types_gen.go, with their CUE
// field kept a plain int/string — Go owns the named type + behavior, CUE owns
// the wire VALUE SHAPE only.

// Scope classifies what kind of filesystem mutation a step makes. Steps are
// grouped by scope (and venue) when the host target batches into sudo vs user
// heredocs — mixing scopes in one batch would need per-command sudo. It is the
// integer-valued enum the ledger serializes (omitempty omits ScopeSystem=0),
// so the wire form is unchanged by living in spec.
type Scope int

const (
	// ScopeSystem mutates global host state: /etc, /usr, /var, systemd system
	// units, package DB. Requires sudo on host; emitted as USER root in the
	// Containerfile.
	ScopeSystem Scope = iota

	// ScopeUser mutates the invoking user's home or user-owned paths:
	// $HOME/.pixi, $HOME/.cargo, $HOME/.npm-global, $HOME/.local, systemd
	// user units, etc. No sudo needed on host; emitted as USER ${UID} in the
	// Containerfile.
	ScopeUser

	// ScopeUserProfile writes to the user's shell init surface:
	// ~/.bashrc / ~/.zshenv / fish conf.d + ~/.config/opencharly/env.d/.
	// Separate from ScopeUser because the host target has special handling
	// (managed blocks, shell detection) and the OCI target renders these as
	// ENV directives + path additions rather than file writes.
	ScopeUserProfile
)

func (s Scope) String() string {
	switch s {
	case ScopeSystem:
		return "system"
	case ScopeUser:
		return "user"
	case ScopeUserProfile:
		return "user-profile"
	}
	return "unknown"
}

// ScopeFromName parses a StepContract's authored scope NAME (the
// Describe-carried "user"/"user-profile" string; anything else, including
// empty, defaults to ScopeSystem) into a Scope.
func ScopeFromName(name string) Scope {
	switch name {
	case "user":
		return ScopeUser
	case "user-profile":
		return ScopeUserProfile
	default:
		return ScopeSystem
	}
}

// ReverseOpKind discriminates the kinds of teardown actions Reverse() produces.
// Ledger entries serialize these verbatim so a later `charly fleet del` can
// walk them without re-compiling the plan.
type ReverseOpKind string

const (
	ReverseOpPackageRemove  ReverseOpKind = "package-remove"
	ReverseOpCargoUninstall ReverseOpKind = "cargo-uninstall"
	ReverseOpNpmUninstallG  ReverseOpKind = "npm-uninstall-g"
	ReverseOpPixiEnvRemove  ReverseOpKind = "pixi-env-remove"
	ReverseOpRmFileSystem   ReverseOpKind = "rm-file-system"
	ReverseOpRmFileUser     ReverseOpKind = "rm-file-user"
	ReverseOpRmDirRecursive ReverseOpKind = "rm-dir-recursive"
	ReverseOpServiceDisable ReverseOpKind = "service-disable"
	ReverseOpServiceRemove  ReverseOpKind = "service-remove"
	ReverseOpRemoveDropin   ReverseOpKind = "remove-dropin"
	ReverseOpRestoreEnabled ReverseOpKind = "restore-enabled"
	ReverseOpRemoveManaged  ReverseOpKind = "remove-managed-block"
	ReverseOpRemoveEnvdFile ReverseOpKind = "remove-envd-file"
	ReverseOpRemoveRepoFile ReverseOpKind = "remove-repo-file"
	ReverseOpCoprDisable    ReverseOpKind = "copr-disable"

	// ReverseOpPluginScript is the GENERIC recordable reverse op an external
	// (out-of-process) deploy/step/builder plugin returns: a shell script + its
	// scope, run verbatim at teardown via the ReverseExecutor (system → sudo,
	// user → no sudo). The script lives in Extra["script"]; Scope picks the
	// privilege. It preserves the record-and-replay invariant — only RECORDED
	// ops are replayed, never recomputed — without any new struct shape.
	ReverseOpPluginScript ReverseOpKind = "plugin-script"
)

// ReverseOpPluginScriptKey is the Extra map key carrying a ReverseOpPluginScript's
// shell-script body. Exported so both the host handler and the SDK builder name
// the one key (R3 — no magic-string drift across the process boundary).
const ReverseOpPluginScriptKey = "script"
