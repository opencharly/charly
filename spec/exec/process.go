package exec

import (
	"io"
	"os"
	"os/exec"
)

// The process-group shutdown ladder + the SortedEnvPairs / RemoteLaunchCommand
// launch renderers moved to the fabric slice github.com/opencharly/spec/proc
// (#55 step1) — a host primitive a plugin cannot hold, now single-sourced in
// the spec contract module. This file keeps only the caller-owned stdio pipe
// wiring, which is used exclusively by sdk/kit's own process-executor
// (deploy_executor_process.go) and therefore stays in kit.

// processPipes holds the parent-side ends of a child's three stdio pipes
// created by startProcessPipes.
type processPipes struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// startProcessPipes wires fresh os.Pipe ends to cmd.Stdin/Stdout/Stderr and
// starts cmd. The pipes are owned by the CALLER, not by os/exec: Wait closes
// only pipes it created itself (the StdinPipe/StdoutPipe/StderrPipe helpers),
// which is exactly the race the os/exec docs warn about — "incorrect to call
// Wait before all reads from the pipe have completed" — so a Wait monitor may
// run concurrently with readers here without ever closing a pipe under them
// ("read |0: file already closed"). The parent's copies of the child-side
// ends are closed as soon as Start succeeds (the child holds its own
// duplicates), so a reader sees EOF the instant the child exits. The
// parent-side ends stay open for the consumer to write/read and close.
func startProcessPipes(cmd *exec.Cmd) (processPipes, error) {
	var pipes processPipes
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return pipes, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return pipes, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return pipes, err
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return pipes, err
	}
	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	pipes.stdin = stdinW
	pipes.stdout = stdoutR
	pipes.stderr = stderrR
	return pipes, nil
}
