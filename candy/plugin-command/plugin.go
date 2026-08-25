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
	"time"

	"github.com/opencharly/plugin-command/candy/plugin-command/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the command verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:command (plugin_input #CommandInput) + the embedded CUE schema, via
// sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb reads
// it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.2800",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "command", InputDef: "#CommandInput", Primary: "command"}},
		schemaFS)
}

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
	started := time.Now()
	if inContainer {
		stdout, stderr, exitCode, err = cc.Exec().RunCapture(ctx, wrapContainerCommand(in.Command))
	} else {
		if cc.Mode() == kit.ModeBox {
			return kit.Skip("host-side command not meaningful under charly check box")
		}
		c := exec.CommandContext(ctx, "sh", "-c", in.Command)
		stdout, stderr, exitCode, err = captureCmd(c)
	}
	elapsed := time.Since(started)
	if err != nil {
		return kit.Failf("execution error: %v", err)
	}
	// A probe the never-hang ceiling killed reports exit=-1 (killed by signal, not an exit status)
	// and/or an expired ctx. Falling through to the ordinary exit-code comparison rendered that as
	// a bare "exit=-1, want 0" — a message that says nothing about WHY, and reads like the command
	// returned a weird status rather than never having finished. Under the 32-bed roster that was
	// the difference between "this probe timed out at the ceiling" and an RCA spent hunting a
	// nonexistent exit code.
	//
	// It stays a FAIL rather than a skip or an infra signal, deliberately. A verb's result carries
	// only pass/fail/skip (kit.Result is spec.CheckVerbResult — the engine-internal
	// DeadlineExceeded flag is json:"-" and never crosses an out-of-process verb's wire), and a
	// killed probe DID NOT demonstrate the property it asserts. Reporting it as a skip would be a
	// fake pass of exactly the class the check skill bans for an unreachable dependency; the honest
	// report is a failure that names its own mechanism.
	if killedByCeiling(ctx, exitCode) {
		return kit.Failf("probe killed by never-hang ceiling after %s (no exit status — the process was signalled, not returned); command: %s",
			elapsed.Round(time.Millisecond), trimPreview(in.Command))
	}

	// expect_non_zero asserts the command FAILED (any non-zero code) and IGNORES
	// exit_status — the two are mutually-exclusive intents (any-non-zero vs
	// exact-code). Otherwise assert the exact code (exit_status, default 0).
	if in.ExpectNonZero {
		if exitCode == 0 {
			return kit.Failf("expected non-zero exit, got 0 (stdout: %s)", trimPreview(stdout))
		}
	} else {
		wantExit := 0
		if op.ExitStatus != nil {
			wantExit = *op.ExitStatus
		}
		if exitCode != wantExit {
			return kit.Failf("exit=%d, want %d (stderr: %s)", exitCode, wantExit, trimPreview(stderr))
		}
	}
	if err := sdk.MatchAll(stdout, op.Stdout); err != nil {
		return kit.Failf("stdout: %v (got: %s)", err, trimPreview(stdout))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return kit.Failf("stderr: %v (got: %s)", err, trimPreview(stderr))
	}
	return kit.Passf("exit=%d", exitCode)
}

// killedByCeiling reports whether a completed exec was cut short by the never-hang ceiling rather
// than returning a status of its own: either the step ctx expired, or the process died by signal
// (exec reports -1 for "no exit status", which is what SIGKILL from CommandContext produces).
// Checked together because the two do not always coincide — an in-container RunCapture can surface
// the signal death without the local ctx having expired yet, and a ctx cancelled by an outer
// deadline can land before the child's status is observed.
func killedByCeiling(ctx context.Context, exitCode int) bool {
	return ctx.Err() != nil || exitCode == -1
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
