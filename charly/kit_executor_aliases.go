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
	NestedJump     = kit.NestedJump
)

var NestedContainerName = kit.NestedContainerName

const (
	JumpDockerExec = kit.JumpDockerExec
	JumpPodmanExec = kit.JumpPodmanExec
)

var (
	BuilderRun          = kit.BuilderRun
	UserScopeBindMounts = kit.UserScopeBindMounts
	UserScopeEnv        = kit.UserScopeEnv

	// runCaptureCmd is kit's shared "run + capture stdout/stderr/exit" exec helper,
	// used by charly's commands.go alongside the executors.
	runCaptureCmd = kit.RunCaptureCmd

	// deployShellQuote is the kit.ShellQuote shell-arg quoter, aliased at the
	// charly name builder_venue.go + the deploy targets already use.
	deployShellQuote = kit.ShellQuote
)
