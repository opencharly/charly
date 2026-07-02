// Package command is the importable, COMPILED-IN host-coupled `command` check verb: run
// a shell command in-container / host-side / backgrounded and assert exit/stdout/stderr.
// CheckVerbProvider ONLY — its run: ACT is the dedicated package-main install-task emitCmd
// branch (`plugin == "command"`), NOT a kit.ProvisionActor. RunVerb needs the live
// kit.CheckContext (Exec under in-container, host os/exec under from_host, AddBackground
// for the fire-and-forget path), so it is COMPILED-IN-ONLY. Relocated out of charly's
// module (formerly charly/plugin/builtins/command + charly/plugin_verb_command.go).
// Matchers via the importable sdk.MatchAll; exit_status/stdout/stderr ride the base #Op.
package command

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"os/exec"

	"github.com/opencharly/charly/candy/plugin-command/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:command": "#CommandInput"}

// NewCheckVerb returns the command verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "command" }

// RunVerb runs the command via the live CheckContext and asserts exit/stdout/stderr.
// in-container (default) via cc.Exec; host-side (from_host / in_container:false) via
// os/exec under ModeLive; background (host-side, fire-and-forget) registers the PID with
// the plan via cc.AddBackground. The command + location flags ride plugin_input; the
// exit/stdout/stderr matchers + timeout stay base #Op (read off op). Mirrors r.runCommand.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.CommandInput
	kit.DecodeInput(op.PluginInput, &in)

	inContainer := true
	if in.InContainer != nil {
		inContainer = *in.InContainer
	}
	if in.FromHost {
		inContainer = false
	}

	// Background path — host-side only, fire-and-forget. Plan teardown reaps via SIGTERM.
	if in.Background {
		if inContainer {
			return kit.Fail("background: true is host-side only (set in_container: false or from_host: true)")
		}
		if cc.Mode() == kit.ModeBox {
			return kit.Skip("background command not meaningful under charly check box")
		}
		cmd := exec.Command("sh", "-c", in.Command) // not CommandContext — survives ctx cancel
		if err := cmd.Start(); err != nil {
			return kit.Failf("background start: %v", err)
		}
		cc.AddBackground(cmd.Process.Pid)
		// Reap asynchronously so a kill: SIGKILL doesn't leave a zombie.
		go func() { _ = cmd.Wait() }()
		return kit.Passf("backgrounded pid=%d", cmd.Process.Pid)
	}

	var stdout, stderr string
	var exitCode int
	var err error
	if inContainer {
		stdout, stderr, exitCode, err = cc.Exec().RunCapture(ctx, wrapContainerCommand(in.Command))
	} else {
		if cc.Mode() == kit.ModeBox {
			return kit.Skip("host-side command not meaningful under charly check box")
		}
		c := exec.CommandContext(ctx, "sh", "-c", in.Command)
		stdout, stderr, exitCode, err = captureCmd(c)
	}
	if err != nil {
		return kit.Failf("execution error: %v", err)
	}

	wantExit := 0
	if op.ExitStatus != nil {
		wantExit = *op.ExitStatus
	}
	if exitCode != wantExit {
		return kit.Failf("exit=%d, want %d (stderr: %s)", exitCode, wantExit, trimPreview(stderr))
	}
	if err := sdk.MatchAll(stdout, op.Stdout); err != nil {
		return kit.Failf("stdout: %v (got: %s)", err, trimPreview(stdout))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return kit.Failf("stderr: %v (got: %s)", err, trimPreview(stderr))
	}
	return kit.Passf("exit=%d", exitCode)
}

// wrapContainerCommand is the shared kit helper (FU-11 — formerly duplicated in core + plugins).
var wrapContainerCommand = kit.WrapContainerCommand

// captureCmd runs a host *exec.Cmd, capturing stdout/stderr/exit (mirrors runCaptureCmd).
func captureCmd(cmd *exec.Cmd) (string, string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return stdout.String(), stderr.String(), ee.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// trimPreview is the shared kit helper (FU-11 — formerly duplicated in core + plugins).
var trimPreview = kit.TrimPreview
