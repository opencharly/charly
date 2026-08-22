package exec

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestStartProcessWaitMonitorNeverClosesConsumerPipes is the deterministic
// regression gate for the Wait/pipe-ownership race: with os/exec's own
// StdoutPipe/StderrPipe, the Wait monitor closed the pipes the consumer was
// still reading ("incorrect to call Wait before all reads from the pipe have
// completed" — os/exec), surfacing as `read |0: file already closed` roughly
// one run in ten on the small-payload StartProcess tests. A payload larger
// than the pipe buffer forces ReadAll to span several read syscalls, so the
// Wait monitor ALWAYS fires mid-drain — the race window is open on every
// iteration, not by scheduler luck. The os.Pipe ownership pattern (the pipes
// belong to the caller; Wait only closes pipes it created itself) makes the
// monitor harmless.
func TestStartProcessWaitMonitorNeverClosesConsumerPipes(t *testing.T) {
	for i := range 8 {
		p, err := (ShellExecutor{}).StartProcess(context.Background(), spec.ProcessLaunch{
			Argv: []string{"sh", "-c", "head -c 1048576 /dev/zero; head -c 65536 /dev/zero >&2"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(p.Stdin(), ""); err != nil {
			t.Fatalf("iteration %d: stdin: %v", i, err)
		}
		_ = p.Stdin().Close()

		// Drain stdout and stderr CONCURRENTLY. The fixture script runs both
		// head invocations under one sh, so sh's stdout fd stays open across
		// the whole script; io.ReadAll(p.Stdout()) cannot observe EOF until sh
		// itself exits, which requires the second head's 65536-byte write to
		// the (default-64KiB-capacity) stderr pipe to complete. Reading the
		// two streams sequentially — stdout fully, then stderr — is the
		// classic exec.Cmd multi-stream deadlock: nothing drains stderr while
		// this goroutine blocks in the stdout read, so the second head blocks
		// on write(), sh blocks in wait4() for it, and stdout's write end
		// never closes. This starved a genuine goroutine (observed live: a
		// 600s+ hang inside startCommandProcess's cmd.Wait(), `/proc` showing
		// head parked in anon_pipe_write). Concurrent drain — the same
		// pattern testkit.StartSSHProcessServer already uses for its own
		// stdout/stderr pumps — is the fix.
		var out, diagnostic []byte
		var outErr, errErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); out, outErr = io.ReadAll(p.Stdout()) }()
		go func() { defer wg.Done(); diagnostic, errErr = io.ReadAll(p.Stderr()) }()
		wg.Wait()
		if outErr != nil {
			t.Fatalf("iteration %d: stdout read: %v", i, outErr)
		}
		if errErr != nil {
			t.Fatalf("iteration %d: stderr read: %v", i, errErr)
		}
		if err := p.Wait(); err != nil {
			t.Fatalf("iteration %d: wait: %v", i, err)
		}
		if len(out) != 1048576 {
			t.Fatalf("iteration %d: stdout = %d bytes, want 1048576", i, len(out))
		}
		if len(diagnostic) != 65536 {
			t.Fatalf("iteration %d: stderr = %d bytes, want 65536", i, len(diagnostic))
		}
		if err := p.Close(); err != nil {
			t.Fatalf("iteration %d: idempotent close: %v", i, err)
		}
	}
}
