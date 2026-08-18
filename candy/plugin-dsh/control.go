package dsh

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// control.go — the `charly dsh` command tree: the kong grammar the host prescans
// into the CLI (via the reflected CLIModel) and the plugin kong-parses its
// pass-through args into (sdk.RunInProcCLI). Every leaf manages a DEPLOYED dsh
// box over the reverse channel via the plugin-cmd pattern: the executor stashed
// during Invoke (setCommandContext) drives the "pod-lifecycle" host-builder's
// op="cmd" (spec.PodLifecycleRequest) to run `dsh …` in the box — the command's
// stdout rides the host-held interactive leg straight to the operator's terminal
// (stdio never crosses the wire). This requires COMPILED-IN placement (the
// pod-lifecycle reverse channel is unavailable out-of-process — plugin-cmd
// documents this).

// dshCmdCtx / dshCmdExec carry the Invoke(OpRun) reverse-channel handle to the
// kong handlers' HostBuild("pod-lifecycle") op="cmd" calls.
var (
	dshCmdCtx  context.Context
	dshCmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one
// `charly dsh …` dispatch. Called once at the top of command:dsh's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	dshCmdCtx = ctx
	dshCmdExec = ex
}

// DshCmd is the `charly dsh` command tree.
type DshCmd struct {
	Status  DshStatusCmd  `cmd:"" help:"Show the dsh version and web UI status in a deployed box"`
	Profile DshProfileCmd `cmd:"" help:"Manage dsh profiles in a deployed box"`
	Plugin  DshPluginCmd  `cmd:"" help:"Manage dsh plugins in a deployed box"`
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

type DshStatusCmd struct {
	Box      string `name:"box" help:"Box name (required)"`
	Instance string `name:"instance" help:"Instance name"`
}

func (c *DshStatusCmd) Run() error {
	// dsh --version, then probe the loopback-bound web UI in-box (the direct
	// check; the socat forwarder is what makes it reachable from the host).
	command := "dsh --version && curl -fsS -o /dev/null http://127.0.0.1:3080/ && echo 'web UI: HTTP 200 on 127.0.0.1:3080'"
	return runInBox(c.Box, c.Instance, command)
}

// ---------------------------------------------------------------------------
// profile
// ---------------------------------------------------------------------------

type DshProfileCmd struct {
	List DshProfileListCmd `cmd:"" help:"List profiles in a deployed box"`
}

type DshProfileListCmd struct {
	Box      string `name:"box" help:"Box name (required)"`
	Instance string `name:"instance" help:"Instance name"`
}

func (c *DshProfileListCmd) Run() error {
	// DSH_HOME defaults to ~/.dsh (the dsh candy's env); a missing profile
	// directory reports "(no profiles yet)" instead of failing.
	command := `profiles="${DSH_HOME:-$HOME/.dsh}/profiles"; if [ -d "$profiles" ]; then ls -1 "$profiles"; else echo "(no profiles yet)"; fi`
	return runInBox(c.Box, c.Instance, command)
}

// ---------------------------------------------------------------------------
// plugin
// ---------------------------------------------------------------------------

type DshPluginCmd struct {
	List DshPluginListCmd `cmd:"" help:"List plugins for a profile in a deployed box"`
}

type DshPluginListCmd struct {
	Box      string `name:"box" help:"Box name (required)"`
	Instance string `name:"instance" help:"Instance name"`
	Profile  string `name:"profile" help:"Profile name (default: web)"`
}

func (c *DshPluginListCmd) Run() error {
	profile := c.Profile
	if profile == "" {
		profile = "web"
	}
	// dsh plugin forwards the remaining args to pnpm in the profile directory, so
	// --profile comes BEFORE the forwarded `list` (the dsh CLI's own grammar).
	return runInBox(c.Box, c.Instance, "dsh plugin --profile "+profile+" list")
}

// ---------------------------------------------------------------------------
// the pod-lifecycle op="cmd" leg (the plugin-cmd pattern)
// ---------------------------------------------------------------------------

// runInBox runs a command in a deployed box via the "pod-lifecycle" host-builder's
// op="cmd" — the deploy-lifecycle Attach a plugin cannot perform. The command's
// stdout rides the host-held interactive leg straight to the operator's terminal;
// a non-zero exit rides the reply's ExitCode FIELD (the HostBuild ERROR return
// stringifies the typed *sdk.ExitCodeError, losing the code), which this
// reconstructs into an *sdk.ExitCodeError so the operator sees the command's own
// code — exactly as plugin-cmd's hostPodCmd does.
func runInBox(box, instance, command string) error {
	if dshCmdExec == nil {
		return fmt.Errorf("dsh: no host reverse channel (command not compiled-in?)")
	}
	box, instance = deploykit.CanonicalizeDeployArg(box, instance)
	// Resolve the per-host deploy node plugin-side and thread it as DATA, so the
	// host's dispatchLifecycleTarget operates on it instead of re-reading the
	// per-host config itself (the plugin-cmd pattern).
	node, _ := loaderkit.ResolveLifecycleDeployNodeViaExecutor(dshCmdCtx, dshCmdExec, box, instance)
	payloadJSON, err := json.Marshal(spec.PodCmdPayload{Command: command})
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.PodLifecycleRequest{Op: "cmd", Box: box, Instance: instance, Node: node, Payload: payloadJSON})
	if err != nil {
		return err
	}
	out, err := dshCmdExec.HostBuild(dshCmdCtx, "pod-lifecycle", reqJSON)
	if err != nil {
		return err
	}
	var reply spec.PodLifecycleReply
	if uerr := json.Unmarshal(out, &reply); uerr != nil {
		return uerr
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: reply.ExitCode}
	}
	return nil
}
