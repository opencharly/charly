package main

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/opencharly/spec/ops"
	pb "github.com/opencharly/spec/proto"
)

// progressThenReturnProvider performs host-visible progress on the shared per-call
// clock for a while (the shape of a long rebuild whose host child is alive), then
// returns successfully — like check-omarchy-iso-vm's [update]: ~13min of a running
// install, far past the old 10m total cap, with NO idle gap.
type progressThenReturnProvider struct{}

func (progressThenReturnProvider) Reserved() string     { return "progress" }
func (progressThenReturnProvider) Class() ProviderClass { return ClassVerb }
func (progressThenReturnProvider) Invoke(ctx context.Context, _ *Operation) (*Result, error) {
	// A real peer's progress is its HOST-visible reverse legs (which touch s.activity);
	// here we touch the same clock the host threaded onto our ctx, standing in for
	// those legs. Steady progress across 3x the no-progress window, then success.
	clock := pluginActivityFrom(ctx)
	window := pluginInvokeNoProgress()
	deadline := time.Now().Add(3 * window)
	for time.Now().Before(deadline) {
		clock.touch()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(window / 4):
		}
	}
	return &Result{JSON: []byte(`{}`)}, nil
}

// TestInvokeProvider_ProgressingLongCallIsNotKilled is THE regression guard for the
// real defect: a plugin call that keeps performing host-visible work must survive
// LONGER than the old fixed 10m total cap. The former code killed it at exactly the
// cap (`context deadline exceeded`) though it was progressing; the idle bound must
// let it finish.
func TestInvokeProvider_ProgressingLongCallIsNotKilled(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 200 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	RegisterBuiltinProvider(progressThenReturnProvider{})
	srv := &executorReverseServer{}

	start := time.Now()
	// 600ms of steady progress — well past the 200ms idle window, the shape of a
	// 13min install vs the old 10min cap.
	if _, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{
		Class: "verb", Reserved: "progress", Op: string(ops.OpRun),
	}); err != nil {
		t.Fatalf("a progressing long call must NOT be killed by the idle bound: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("expected the call to run its full duration (~600ms), got %s", elapsed)
	}
}

// TestIdleBoundedContext_TripsOnlyWhenIdle pins the core distinction directly: with
// steady activity the bound never fires; once activity stops, it fires with the sentinel.
func TestIdleBoundedContext_TripsOnlyWhenIdle(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 150 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// Idle: no touches → cancelled with the sentinel.
	a := &pluginActivity{}
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), errPluginCallIdle) {
			t.Fatalf("idle call: expected errPluginCallIdle, got %v", context.Cause(ctx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle call was not cancelled")
	}
	stop()

	// Progressing: a toucher every 50ms (< the 150ms window) → never cancelled.
	b := &pluginActivity{}
	ctx2, stop2 := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), b)
	touchStop := make(chan struct{})
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-touchStop:
				return
			case <-tk.C:
				b.touch()
			}
		}
	}()
	select {
	case <-ctx2.Done():
		close(touchStop)
		t.Fatalf("progressing call must not be cancelled, got %v", context.Cause(ctx2))
	case <-time.After(600 * time.Millisecond):
		// good: outlived 4x the idle window while progressing
	}
	close(touchStop)
	stop2()
}

// TestIdleBoundedContext_PluginLocalCPUIsProgress pins the SECOND progress source: a
// peer plugin doing its work ENTIRELY LOCALLY (its own child processes — virsh/ssh
// retries) touches NO host reverse leg, yet must count as progress. Without the
// /proc CPU signal, the idle guard false-killed a legitimately-booting ISO guest at
// prepare-venue (measured live).
func TestIdleBoundedContext_PluginLocalCPUIsProgress(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// A real child that burns CPU for ~2s — the plugin process's OWN work, no host leg.
	cmd := exec.Command("sh", "-c", "end=$((SECONDS+2)); while [ $SECONDS -lt $end ]; do :; done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cpu-burner: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// The clock polls the BURNER's pid directly (standing in for the plugin pid: the
	// plugin's own CPU is what we must observe).
	a := &pluginActivity{pid: cmd.Process.Pid}
	// Baseline via procCPUTicks DIRECTLY (not a.cpu()) so this test still exercises the
	// idle guard's CPU source: with cpu() disabled the guard would idle-kill below and
	// the test would FAIL, which is the regression it guards.
	base := uint64(0)
	for i := 0; i < 50 && base == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		base = procCPUTicks(cmd.Process.Pid)
	}
	if base == 0 {
		t.Skip("procCPUTicks unavailable on this platform — the CPU-progress path cannot be exercised")
	}
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	select {
	case <-ctx.Done():
		t.Fatalf("a plugin busy on its OWN children must not be idle-killed, got %v", context.Cause(ctx))
	case <-time.After(1 * time.Second):
		// good: outlived 3x the idle window purely on its own CPU
	}
	stop()
}

// TestProcCPUTicks_Monotonic sanity-checks the reader against a live child.
func TestProcCPUTicks_Monotonic(t *testing.T) {
	cmd := exec.Command("sh", "-c", "i=0; while [ $i -lt 3000000 ]; do i=$((i+1)); done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	// The very first sample can legitimately be 0 (the child has not accrued CPU in
	// its first instant), so establish a nonzero baseline first.
	first := uint64(0)
	for i := 0; i < 50 && first == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		first = procCPUTicks(cmd.Process.Pid)
	}
	if first == 0 {
		t.Fatal("procCPUTicks never left 0 for a busy process")
	}
	time.Sleep(200 * time.Millisecond)
	second := procCPUTicks(cmd.Process.Pid)
	if second <= first {
		t.Fatalf("procCPUTicks must advance for a busy process: first=%d second=%d", first, second)
	}
}
