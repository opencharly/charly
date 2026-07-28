package main

import (
	"context"

	"github.com/opencharly/sdk/deploykit"
)

// cmd.go — the hidden `charly __cmd` core reentry (#118 loader+check-tail cone): the
// deploy-lifecycle-coupled half of the `charly cmd` split. candy/plugin-cmd owns the user-facing
// `charly cmd` CLI grammar + the completion notification and drives THIS reentry over
// HostBuild("cli") with inherited stdio for the interactive exec. The reentry runs a single command
// in a running container via the unified LifecycleTarget → OpAttach (dispatchLifecycleTarget): the
// host resolves the `<engine> exec -i … sh -c` command, and the owning deploy plugin runs it over
// the served venue executor via RunInteractive (stdio host-held; `-i` forwards the operator's stdin).
//
// This handler is TRACKED RESIDUE (deploy-lifecycle capability), NOT floor: it leaves core when the
// deploy-lifecycle coupling itself relocates (coneA3) — the same class as build.go's former
// __box-build reentry, which moved to candy/plugin-box once build:box was InvokeProvider-reachable.
// Registered as the hidden `__cmd` command in main.go's CLI (mirroring __box-pkg). The
// running-container gate + the notify container resolve now live plugin-side (candy/plugin-cmd).

// CmdCmd runs a single command in a running container. The user-facing --notify + the container
// resolve relocated to candy/plugin-cmd; this reentry is the deploy-lifecycle Attach alone.
type CmdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Sidecar  string `long:"sidecar" help:"Run in the named SIDECAR container (charly-<box>[-<instance>]-<sidecar>) instead of the app container"`
}

func (c *CmdCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)

	lt, err := dispatchLifecycleTarget("cmd", c.Box, c.Instance)
	if err != nil {
		return err
	}
	// The exec routes through the unified LifecycleTarget → OpAttach (F12): the host resolves the
	// `<engine> exec -i … sh -c` command (resolvePodCmdPlan re-resolves the same container host-side),
	// the owning plugin runs it over the served venue executor via RunInteractive (stdio host-held;
	// `-i` forwards the operator's stdin from the plugin-driven HostBuild("cli") inherited streams).
	return lt.Attach(withPodCmdOpts(context.Background(), podCmdOpts{Sidecar: c.Sidecar}), []string{c.Command}, false)
}
