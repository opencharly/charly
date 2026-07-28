package cmd

// command.go — the `charly cmd` handler (#118 loader+check-tail cone), the plugin half of the
// deploy-lifecycle-coupled command split. `charly cmd <box> <command>` runs a single command in a
// running container with an optional completion notification. The interactive exec itself is
// deploy-lifecycle machinery (dispatchLifecycleTarget → OpAttach) a plugin cannot perform, so the
// plugin drives the hidden `charly __cmd` core reentry over the generic HostBuild("cli") seam with
// INHERITED stdio (Capture:false) — the `-i` interactive stream reaches the operator. The __cmd
// handler stays core (deploy-lifecycle-coupled RESIDUE, gated on the deploy-lifecycle relocation),
// NOT floor. The plugin owns the CLI grammar + --notify (a host desktop-bus op) directly.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CmdCmd runs a single command in a running container with optional completion notification.
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
	// bus the desktop notify drives) — and, as the core __cmd reentry does, the running-container gate.
	var engine, name string
	var rerr error
	if c.Sidecar != "" {
		engine, name, rerr = deploykit.ResolveSidecarContainer(c.Box, c.Instance, c.Sidecar)
	} else {
		engine, name, rerr = deploykit.ResolveContainer(c.Box, c.Instance)
	}
	if rerr != nil {
		return rerr
	}

	argv := []string{"__cmd", c.Box, c.Command}
	if c.Instance != "" {
		argv = append(argv, "-i", c.Instance)
	}
	if c.Sidecar != "" {
		argv = append(argv, "--sidecar", c.Sidecar)
	}
	start := time.Now()
	runErr := hostCli(argv)
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

// hostCli forks the given `charly <argv>` subcommand over the generic "cli" HostBuild seam with
// inherited (non-captured) stdio, so the interactive __cmd Attach streams to the operator; the
// child's exit code round-trips as an *sdk.ExitCodeError so the operator sees the command's own code.
func hostCli(argv []string) error {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: false})
	if err != nil {
		return err
	}
	out, err := cmdExec.HostBuild(cmdCtx, "cli", reqJSON)
	if err != nil {
		return err
	}
	var reply spec.CliReply
	if uerr := json.Unmarshal(out, &reply); uerr != nil {
		return uerr
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: reply.ExitCode, Err: fmt.Errorf("command exited %d", reply.ExitCode)}
	}
	return nil
}
