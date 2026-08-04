package cmd

// command.go — the `charly cmd` handler (#118 loader+check-tail cone), the plugin half of the
// deploy-lifecycle-coupled command split. `charly cmd <box> <command>` runs a single command in a
// running container with an optional completion notification. The interactive exec itself is
// deploy-lifecycle machinery (dispatchLifecycleTarget + LifecycleTarget.Attach) a plugin cannot
// perform, so the plugin drives the "pod-cmd" host-builder — cmd's slot in the FLOORED
// pod-lifecycle-dispatch family (host_build_pod_lifecycle_dispatch.go), joining its interactive
// sibling `charly shell` (pod-shell). The exec runs over the SAME host-held exec.RunInteractive leg
// (stdio never crosses the wire; the `-i` interactive stream reaches the operator's real terminal),
// so the former hidden `charly __cmd` core reentry is DISSOLVED. The plugin owns the CLI grammar +
// --notify (a host desktop-bus op) directly.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// CmdCmd runs a single command in a running container with optional completion notification.
type CmdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
	Notify   bool   `name:"notify" negatable:"" default:"true" help:"Send desktop notification on completion (--no-notify to disable)"`
	Sidecar  string `name:"sidecar" help:"Run in the named SIDECAR container (charly-<box>[-<instance>]-<sidecar>) instead of the app container"`
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

	// #55 K4 seam-completion: resolve the per-host deploy node plugin-side and thread it as DATA, so
	// the host's dispatchLifecycleTarget operates on it instead of re-reading the per-host config
	// itself (byte-identical to the retired core resolveLifecycleDeployNode; box/instance MUST match
	// the request's Box/Instance — the host derives deployName = DeployKey from those).
	// #55 coneC Unit C2: the resolver moved from deploykit.ResolveLifecycleDeployNodeViaSeam (the
	// deleted host bundle-config loader-seam round-trip) to the cycle-free
	// loaderkit.ResolveLifecycleDeployNodeViaExecutor (plugin-side, over the reverse channel).
	cmdNode, _ := loaderkit.ResolveLifecycleDeployNodeViaExecutor(cmdCtx, cmdExec, c.Box, c.Instance)
	start := time.Now()
	runErr := hostPodCmd(spec.PodCmdRequest{Box: c.Box, Command: c.Command, Instance: c.Instance, Sidecar: c.Sidecar, Node: cmdNode})
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

// hostPodCmd drives the "pod-cmd" host-builder — the deploy-lifecycle Attach a plugin cannot perform
// (dispatchLifecycleTarget + LifecycleTarget.Attach). cmd joins its interactive sibling `charly shell`
// (pod-shell) in the pod-lifecycle-dispatch family: the exec runs over the SAME host-held
// exec.RunInteractive leg (stdio never crosses the wire; the `-i` interactive stream reaches the
// operator's real terminal). The container command's non-zero exit rides the reply's ExitCode FIELD
// (the HostBuild ERROR return stringifies the typed *sdk.ExitCodeError, losing the code), which this
// reconstructs into an *sdk.ExitCodeError so the operator sees the command's own code — exactly as
// the former __cmd/CliReply.ExitCode path did.
func hostPodCmd(req spec.PodCmdRequest) error {
	if cmdExec == nil {
		return fmt.Errorf("cmd: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	out, err := cmdExec.HostBuild(cmdCtx, "pod-cmd", reqJSON)
	if err != nil {
		return err
	}
	var reply spec.PodCmdReply
	if uerr := json.Unmarshal(out, &reply); uerr != nil {
		return uerr
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: reply.ExitCode}
	}
	return nil
}
