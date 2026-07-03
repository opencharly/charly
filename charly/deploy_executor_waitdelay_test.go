package main

import (
	"context"
	"testing"
	"time"
)

// A ctx-bounded command whose child BACKGROUNDS a grandchild that inherits the
// stdout pipe and outlives the parent is the canonical CommandContext
// lingering-grandchild hang: at the deadline the DEFAULT cancel SIGKILLs only the
// direct child (bash), the backgrounded grandchild survives holding the pipe open,
// and Cmd.Wait() blocks until the grandchild exits — the 40-min check-live wedge a
// hung `podman exec` probe produced. runCaptureCmd's process-group kill must take
// the grandchild down with the parent so RunCapture returns at the deadline.
func TestRunCapture_LingeringGrandchildDoesNotHang(t *testing.T) {
	// `sleep 30 &` backgrounds a grandchild in bash's process group that inherits
	// stdout and lives 30s; bash then blocks on the foreground `sleep 30`. Without
	// the group-kill, killing bash orphans the backgrounded sleep, it keeps the
	// stdout pipe open, and Cmd.Wait() blocks the full 30s despite the 500ms
	// deadline. With the fix the whole group dies and RunCapture returns promptly.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_, _, _, _ = ShellExecutor{}.RunCapture(ctx, "sleep 30 & sleep 30")
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		// Deadline is 500ms; allow generous slack for CI scheduling. The unfixed
		// path would take ~30s (the grandchild's lifetime).
		if elapsed > 5*time.Second {
			t.Fatalf("RunCapture took %v — the lingering-grandchild wedge is not bounded "+
				"(process-group kill / WaitDelay not applied)", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunCapture HUNG past 15s — the CommandContext lingering-grandchild wedge is NOT fixed")
	}
}

// The double-fork-escape safety net: a grandchild that detaches into its OWN
// session (setsid) escapes the process-group kill, so ONLY WaitDelay can bound the
// wait. Shrink the delay so the test doesn't wall-wait the production 10s.
func TestRunCapture_DoubleForkEscapeBoundedByWaitDelay(t *testing.T) {
	orig := runCaptureWaitDelay
	runCaptureWaitDelay = 500 * time.Millisecond
	defer func() { runCaptureWaitDelay = orig }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		// setsid detaches the grandchild into a new session/group (escaping the
		// group kill) while still inheriting the stdout pipe.
		_, _, _, _ = ShellExecutor{}.RunCapture(ctx, "setsid sleep 30 & sleep 30")
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		// deadline 300ms + WaitDelay 500ms → well under 5s; unfixed path hangs ~30s.
		if elapsed > 5*time.Second {
			t.Fatalf("RunCapture took %v — WaitDelay did not bound the double-fork-escape wait", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunCapture HUNG past 15s — WaitDelay is not bounding the escaped grandchild")
	}
}
