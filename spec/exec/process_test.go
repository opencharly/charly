package exec

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/opencharly/spec/proc"
)

// SortedEnvPairs + RemoteLaunchCommand moved with their subject to
// github.com/opencharly/spec/proc (launch_test.go). These two tests stay here
// because they exercise proc.ShutdownProcessGroup through kit's own
// caller-owned pipe machinery (startProcessPipes) — the exact fixture the
// sdk/kit process executor uses — keeping that fixture single-sourced (R3).

// startTrapChild launches a Setpgid child through the same caller-owned pipe
// machinery the executors use and blocks until the child reports its signal
// traps are installed. The readiness line is the synchronization point:
// without it, a shutdown signal could arrive before the trap exists and the
// test would assert on the default disposition instead of the trap.
func startTrapChild(t *testing.T, script string) (*exec.Cmd, processPipes, *bufio.Reader, chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pipes, err := startProcessPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pipes.stdin.Close()
		_ = pipes.stdout.Close()
		_ = pipes.stderr.Close()
	})
	reader := bufio.NewReader(pipes.stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child readiness = %q, %v", line, err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	return cmd, pipes, reader, done
}

func TestShutdownProcessGroupTerminatesBeforeKilling(t *testing.T) {
	// A child stopped by the SIGTERM stage prints from its trap; a child hit by
	// SIGKILL first could never print.
	cmd, pipes, reader, done := startTrapChild(t, `trap 'echo caught-term; exit 0' TERM; echo ready; sleep 3600 & wait`)
	proc.ShutdownProcessGroup(cmd, pipes.stdin, done)
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "caught-term") {
		t.Fatalf("child did not exit through its SIGTERM trap: %q", rest)
	}
}

func TestShutdownProcessGroupEscalatesToKill(t *testing.T) {
	// This child never reads stdin and ignores SIGTERM, so only the SIGKILL
	// escalation can reap it. ShutdownProcessGroup returning at all is the
	// proof: without the escalation the call blocks forever on an immortal
	// child.
	cmd, pipes, _, done := startTrapChild(t, `trap '' TERM; echo ready; while :; do sleep 60; done`)
	proc.ShutdownProcessGroup(cmd, pipes.stdin, done)
}
