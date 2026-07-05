package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/opencharly/sdk/spec"
)

// host_build_cli.go — the generic "cli" F10 host-builder (M4). A lifecycle plugin
// (candy/plugin-deploy-pod / -vm), running ON the host but out-of-process, asks the host to run a
// `charly <argv>` subcommand via Executor.HostBuild("cli", spec.CliRequest{...}). The handler runs
// in the CHARLY process (os.Args[0] IS charly, which owns the terminal), so Capture=false inherits
// the host's stdin/stdout/stderr (the interactive legs: charly shell, logs -f — the "exec lane for
// TTY" doctrine inverted) and Capture=true captures stdout (short results the plugin parses). It is
// the lifecycle counterpart of the "overlay"/"image"/"plugin-binary" host-builders — a generic
// action noun, NOT a provider WORD (the F11 uniform-API gate forbids one). It replaces the in-core
// runCharlySubcommand / captureCharlyStdout the compiled-in pod/vm lifecycles used.
const cliBuilderKind = "cli"

// hostBuildCli runs a `charly <argv>` subcommand host-side and returns the CliReply. A non-zero exit
// rides CliReply.Error unless BestEffort. The context is unused (an interactive leg must not be
// deadlined — the host TTY owns its lifetime, like the operator running the command directly).
func hostBuildCli(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var req spec.CliRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return nil, fmt.Errorf("cli host-build: decode request: %w", err)
	}
	cmd := exec.Command(os.Args[0], req.Argv...)
	cmd.Stdin = os.Stdin
	var reply spec.CliReply
	if req.Capture {
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		reply.Stdout = buf.String()
		reply.ExitCode, reply.Error = cliExitResult(err, req.BestEffort)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		reply.ExitCode, reply.Error = cliExitResult(err, req.BestEffort)
	}
	return marshalJSON(reply)
}

// cliExitResult maps an exec error to (exitCode, errString): clean → (0, ""); non-zero exit →
// (code, "" if bestEffort else a message); a spawn failure → (-1, message).
func cliExitResult(err error, bestEffort bool) (int, string) {
	if err == nil {
		return 0, ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if bestEffort {
			return ee.ExitCode(), ""
		}
		return ee.ExitCode(), fmt.Sprintf("charly subcommand exited %d", ee.ExitCode())
	}
	return -1, err.Error()
}

// Register the cli host-builder on the F10 HostBuild seam at package-var init (before any init(),
// like the substrate/preresolver registries + the overlay/image builders).
var _ = func() bool { registerHostBuilder(cliBuilderKind, hostBuildCli); return true }()
