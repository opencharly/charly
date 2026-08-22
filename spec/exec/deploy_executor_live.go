package exec

// deploy_executor_live.go — F12: the LIVE-STDIO executor legs (RunInteractive / RunStream),
// the interactive/stream siblings of RunCapture. Unlike RunCapture (buffers → returned),
// these INHERIT the host process's os.Stdin/os.Stdout/os.Stderr so the operator's terminal
// drives the venue command directly: the reverse-channel server runs IN the charly host
// process, so os.Std* IS the operator's terminal — stdio never crosses the plugin wire, only
// the script + the exit code (the hostBuildCli doctrine). The child (`podman exec -it` /
// `ssh -t`) owns the PTY, resize (SIGWINCH), and Ctrl-C, exactly as `charly shell` delegated
// them pre-F12. NOT ctx-deadlined and NOT process-group-killed: the TTY owns the session's
// lifetime; the child shares the host's controlling terminal + foreground process group, so
// signals flow naturally. Consumers: `charly shell`/`charly cmd` (RunInteractive) and
// `charly logs --follow` (RunStream).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// runLiveExit maps an inherited-stdio cmd.Run() result to (exitCode, error): clean → (0, nil);
// a real non-zero exit → (code, nil) — the exit code is the RESULT, not an error (the operator
// already saw the live output); a signal-kill or spawn failure → (-1, err). Shared by every
// RunInteractive/RunStream impl (R3), mirroring RunCaptureCmd's exit-vs-error split.
func runLiveExit(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if asExitErrorDeploy(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code, nil // ran to a real (non-zero) exit — the code IS the result
		}
		return -1, err // terminated by signal (negative code)
	}
	return -1, err // spawn failure
}

// --- ShellExecutor (operator's local machine) ---

func (ShellExecutor) RunInteractive(_ context.Context, script string) (int, error) {
	cmd := exec.Command("bash", "-c", script) // NOT CommandContext: the TTY owns the lifetime, not ctx
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runLiveExit(cmd.Run())
}

func (ShellExecutor) RunStream(_ context.Context, script string) (int, error) {
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr // no Stdin: streamed output only
	return runLiveExit(cmd.Run())
}

// --- SSHExecutor (over ssh) ---

func (e *SSHExecutor) RunInteractive(_ context.Context, script string) (int, error) {
	args := e.SSHBaseArgs()
	args = append(args, "-t") // -t forces a remote PTY
	if script != "" {
		args = append(args, script) // a bare interactive session (empty script) → the guest login shell
	}
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runLiveExit(cmd.Run())
}

func (e *SSHExecutor) RunStream(_ context.Context, script string) (int, error) {
	args := e.SSHBaseArgs()
	args = append(args, script) // no -t: streamed output, no remote PTY
	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runLiveExit(cmd.Run())
}

// --- NestedExecutor (through a jump: container-in-vm etc.) ---

func (n *NestedExecutor) RunInteractive(ctx context.Context, script string) (int, error) {
	if n.Parent == nil {
		return -1, fmt.Errorf("NestedExecutor: nil Parent")
	}
	wrapped, err := n.prepareJump(script, false /*root*/)
	if err != nil {
		return -1, err
	}
	return n.Parent.RunInteractive(ctx, wrapped)
}

func (n *NestedExecutor) RunStream(ctx context.Context, script string) (int, error) {
	if n.Parent == nil {
		return -1, fmt.Errorf("NestedExecutor: nil Parent")
	}
	wrapped, err := n.prepareJump(script, false /*root*/)
	if err != nil {
		return -1, err
	}
	return n.Parent.RunStream(ctx, wrapped)
}
