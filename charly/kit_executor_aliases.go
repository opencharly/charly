package main

// kit_executor_aliases.go — package-main bindings onto the host-side deploy
// executor IMPLEMENTATIONS (ShellExecutor/SSHExecutor/NestedExecutor) + the
// BuilderRun host-engine builder exec, moved to sdk/kit in P4. The executors
// implement spec.DeployExecutor; these aliases keep every charly construction +
// call site (deploy_tree, bundle_add, check_venue, builder_venue, vm_cp_box)
// compiling unchanged.

import "github.com/opencharly/sdk/kit"

type (
	ShellExecutor  = kit.ShellExecutor
	SSHExecutor    = kit.SSHExecutor
	NestedExecutor = kit.NestedExecutor
	JumpKind       = kit.JumpKind
	NestedJump     = kit.NestedJump
)

var NestedContainerName = kit.NestedContainerName

const (
	VenueLocal = kit.VenueLocal

	JumpDockerExec   = kit.JumpDockerExec
	JumpDockerRun    = kit.JumpDockerRun
	JumpPodmanExec   = kit.JumpPodmanExec
	JumpPodmanRun    = kit.JumpPodmanRun
	JumpSSH          = kit.JumpSSH
	JumpVirshConsole = kit.JumpVirshConsole

	// signalKillErrMarker is kit's runCaptureCmd signal-kill sentinel, used by
	// charly's description_eventually.go probe-timeout classification.
	signalKillErrMarker = kit.SignalKillErrMarker
)

var (
	BuilderRun          = kit.BuilderRun
	UserScopeBindMounts = kit.UserScopeBindMounts
	UserScopeEnv        = kit.UserScopeEnv
	BuildBuilderRunArgs = kit.BuildBuilderRunArgs

	// runCaptureCmd is kit's shared "run + capture stdout/stderr/exit" exec helper,
	// used by charly's commands.go alongside the executors.
	runCaptureCmd = kit.RunCaptureCmd

	// deployShellQuote is the kit.ShellQuote shell-arg quoter, aliased at the
	// charly name builder_venue.go + the deploy targets already use.
	deployShellQuote = kit.ShellQuote
)
