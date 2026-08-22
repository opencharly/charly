package exec

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// deploy_executor_live_test.go — the F12 regression gate for the live-stdio executor legs. These tests
// FAIL TO COMPILE without ShellExecutor.RunInteractive/RunStream and FAIL their assertions if a leg
// stops inheriting the operator's os.Stdout or mis-maps the exit code (the runLiveExit contract).

// captureStdout redirects os.Stdout for the duration of fn and returns everything written to it — the
// operator's terminal the F12 legs inherit (the reverse-channel server runs in the host process).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// TestShellExecutorRunStream_InheritedStdout proves the F12 RunStream leg streams the venue command's
// stdout to the operator's inherited os.Stdout and returns exit 0 on success (`charly logs --follow`).
func TestShellExecutorRunStream_InheritedStdout(t *testing.T) {
	var (
		exit int
		err  error
	)
	out := captureStdout(t, func() {
		exit, err = ShellExecutor{}.RunStream(context.Background(), "printf 'MARK-%s\\n' streamed")
	})
	if err != nil {
		t.Fatalf("RunStream error: %v", err)
	}
	if exit != 0 {
		t.Fatalf("RunStream exit = %d, want 0", exit)
	}
	if !strings.Contains(out, "MARK-streamed") {
		t.Fatalf("RunStream stdout = %q, want it to contain MARK-streamed (stdio not inherited?)", out)
	}
}

// TestShellExecutorRunInteractive_InheritedStdout proves the F12 RunInteractive leg (`charly shell` /
// `charly cmd`) inherits os.Stdout and runs the venue command to a clean exit.
func TestShellExecutorRunInteractive_InheritedStdout(t *testing.T) {
	var (
		exit int
		err  error
	)
	out := captureStdout(t, func() {
		exit, err = ShellExecutor{}.RunInteractive(context.Background(), "printf 'MARK-%s\\n' interactive")
	})
	if err != nil {
		t.Fatalf("RunInteractive error: %v", err)
	}
	if exit != 0 {
		t.Fatalf("RunInteractive exit = %d, want 0", exit)
	}
	if !strings.Contains(out, "MARK-interactive") {
		t.Fatalf("RunInteractive stdout = %q, want it to contain MARK-interactive (stdio not inherited?)", out)
	}
}

// TestShellExecutorRunLive_ExitCode proves a real non-zero exit is returned as the RESULT (exit code),
// NOT an error — the runLiveExit contract: the operator already saw the live output, so the exit code
// is the return value, not a failure. A signal-kill or spawn failure would be the error path.
func TestShellExecutorRunLive_ExitCode(t *testing.T) {
	exit, err := ShellExecutor{}.RunInteractive(context.Background(), "exit 7")
	if err != nil {
		t.Fatalf("RunInteractive(exit 7) error = %v; a real non-zero exit must not be an error", err)
	}
	if exit != 7 {
		t.Fatalf("RunInteractive(exit 7) exit = %d, want 7", exit)
	}
	exit, err = ShellExecutor{}.RunStream(context.Background(), "exit 3")
	if err != nil {
		t.Fatalf("RunStream(exit 3) error = %v; a real non-zero exit must not be an error", err)
	}
	if exit != 3 {
		t.Fatalf("RunStream(exit 3) exit = %d, want 3", exit)
	}
}
