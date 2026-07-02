package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestRunKill_SendsSIGKILL spawns a real subprocess, captures its
// PID, runs runKill with signal=KILL, and asserts the process is
// gone. This exercises the kill: verb's full path including
// strconv.Atoi, sendSIGKILL, and the success/fail messaging.
func TestRunKill_SendsSIGKILL(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() {
		// Best-effort cleanup if test fails before kill.
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	// Confirm alive
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process not alive immediately: %v", err)
	}

	r := &Runner{Mode: RunModeLive}
	c := &Op{
		Kill:   fmt.Sprintf("%d", pid),
		Signal: "KILL",
	}
	res := r.runKill(context.Background(), c)
	if res.Status != TestPass {
		t.Fatalf("runKill status=%v message=%q want PASS", res.Status, res.Message)
	}

	// Wait for the OS to reap. Up to 2 seconds with 50ms checks.
	gone := false
	for range 40 {
		// FindProcess always succeeds on unix; Signal(0) returns
		// "no such process" / "process already finished" once it's gone.
		proc, _ := os.FindProcess(pid)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			gone = true
			break
		}
		time.Sleep(50 * time.Millisecond)
		// Reap zombie if present
		_, _ = cmd.Process.Wait()
	}
	if !gone {
		t.Fatalf("process pid=%d still alive 2s after runKill SIGKILL", pid)
	}
}

// TestRunKill_RejectsBadPID covers the input-validation paths:
// empty, non-numeric, non-positive PID strings.
func TestRunKill_RejectsBadPID(t *testing.T) {
	r := &Runner{Mode: RunModeLive}
	cases := []struct{ name, kill string }{
		{"empty", ""},
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Op{Kill: tc.kill}
			res := r.runKill(context.Background(), c)
			if res.Status != TestFail {
				t.Fatalf("Kill=%q expected FAIL, got %v message=%q", tc.kill, res.Status, res.Message)
			}
		})
	}
}
