package main

import (
	"context"
	"fmt"
	"time"

	"github.com/opencharly/sdk/deploykit"
)

// CmdCmd runs a single command in a running container with optional notification.
type CmdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Notify   bool   `long:"notify" negatable:"" default:"true" help:"Send desktop notification on completion (--no-notify to disable)"`
	Sidecar  string `long:"sidecar" help:"Run in the named SIDECAR container (charly-<box>[-<instance>]-<sidecar>) instead of the app container"`
}

func (c *CmdCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)

	// Resolve the target container up-front for the completion notification (the venue whose session
	// bus the desktop notify drives, and the running-container gate). The exec itself routes through
	// the unified LifecycleTarget → OpAttach (F12): the host resolves the `<engine> exec -i … sh -c`
	// command (resolvePodCmdPlan re-resolves the same container host-side), the owning plugin runs it
	// over the served venue executor via RunInteractive (stdio host-held; `-i` forwards the operator's
	// stdin). --notify stays a host wrapper — it is a host desktop-bus op, not a venue op.
	resolve := func() (string, string, error) {
		if c.Sidecar != "" {
			return deploykit.ResolveSidecarContainer(c.Box, c.Instance, c.Sidecar)
		}
		return deploykit.ResolveContainer(c.Box, c.Instance)
	}
	engine, name, err := resolve()
	if err != nil {
		return err
	}

	lt, err := dispatchLifecycleTarget("cmd", c.Box, c.Instance)
	if err != nil {
		return err
	}

	start := time.Now()
	runErr := lt.Attach(withPodCmdOpts(context.Background(), podCmdOpts{Sidecar: c.Sidecar}), []string{c.Command}, false)
	elapsed := time.Since(start).Truncate(time.Millisecond)

	if c.Notify {
		status := "completed"
		if runErr != nil {
			status = "failed"
		}
		sendVenueNotification(deploykit.ContainerChain(engine, name),
			fmt.Sprintf("charly: command %s", status),
			fmt.Sprintf("%s (%s)", c.Command, elapsed))
	}

	return runErr
}
