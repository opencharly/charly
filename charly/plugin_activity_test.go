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
	a := newPluginActivity(0)
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
	b := newPluginActivity(0)
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
	// POSIX-only loop (no $SECONDS, a bash/ksh extension): under dash this must still
	// actually spin, else the test would silently skip on the CI image.
	cmd := exec.Command("sh", "-c", "i=0; while [ $i -lt 100000000 ]; do i=$((i+1)); done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cpu-burner: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// The clock polls the BURNER's pid directly (standing in for the plugin pid: the
	// plugin's own CPU is what we must observe).
	a := newPluginActivity(cmd.Process.Pid)
	// Baseline via procCPUTicks DIRECTLY (not a.cpu()) so this test still exercises the
	// idle guard's CPU source: with cpu() disabled the guard would idle-kill below and
	// the test would FAIL, which is the regression it guards.
	base := uint64(0)
	for i := 0; i < 50 && base == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		base = procCPUTicks(cmd.Process.Pid)
	}
	if base == 0 {
		t.Fatal("procCPUTicks never left 0 for a busy child — the CPU-progress path is not being exercised")
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

// TestIdleBoundedContext_LongHostLegIsProgress pins the third progress path: a host
// reverse LEG that itself runs longer than the no-progress window (a multi-minute
// RunSystem/RunHostStep/HostBuild) must keep the call alive for its whole duration.
// The plugin is blocked in the RPC and its CPU frozen then, so without the leg
// heartbeat the idle guard would kill precisely the plugin-legitimate-long-work class
// this change exists to protect (review finding 6).
func TestIdleBoundedContext_LongHostLegIsProgress(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	a := newPluginActivity(0) // no pid: the CPU source is silent, only the leg heartbeat can act
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	// A host leg running 1s — >3x the window — wrapped by the heartbeat.
	done := make(chan error, 1)
	go func() {
		_, err := withActivityHeartbeat(a, func() (int, error) {
			time.Sleep(1 * time.Second)
			return 0, nil
		})
		done <- err
	}()
	select {
	case <-ctx.Done():
		t.Fatalf("a long host leg must not be idle-killed, got %v", context.Cause(ctx))
	case err := <-done:
		if err != nil {
			t.Fatalf("host leg: %v", err)
		}
	}
	stop()
}

// TestProcessPid_NilSafeAndLive pins the pid PLUMB (review finding 2): the idle guard's
// CPU source is only real if cmd.Process.Pid actually reaches grpcProvider.pid. A nil/not-
// yet-started cmd must yield 0 (source absent, guard falls back to host legs), and a
// STARTED cmd must yield its real pid.
func TestProcessPid_NilSafeAndLive(t *testing.T) {
	if got := processPid(nil); got != 0 {
		t.Fatalf("processPid(nil) = %d, want 0", got)
	}
	cmd := exec.Command("sleep", "5")
	if got := processPid(cmd); got != 0 {
		t.Fatalf("processPid(before Start) = %d, want 0", got)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if got := processPid(cmd); got != cmd.Process.Pid {
		t.Fatalf("processPid(started) = %d, want %d", got, cmd.Process.Pid)
	}
	// And a grpcProvider built with that pid surfaces it via providerPid.
	gp := &grpcProvider{pid: cmd.Process.Pid}
	if got := providerPid(gp); got != cmd.Process.Pid {
		t.Fatalf("providerPid = %d, want %d", got, cmd.Process.Pid)
	}
}

// TestIdleBoundedContext_NilWakeLiteralStillResets pins the nil-wake hazard: a clock
// built as a bare &pluginActivity{} literal (nil wake) must STILL have its deadline
// reset by a touch — idleBoundedContext arms the event channel before watching, so
// the guard can never silently revert to a bare timer and false-kill a progressing
// call. This FAILS if the arming is removed (the touch would never signal).
//
// The window/touch/wait budget is deliberately WIDE relative to the touch period: the
// touch rides its own goroutine, which a heavily loaded host (a CI runner or a machine
// building several plugin candies at once) can starve past the window — a 150ms window
// with a 50ms touch was starved in a real run and false-failed. A 3s window touched
// every 50ms needs a 3s scheduling gap to starve, while still firing well before the
// 7.5s wait when the arming is absent (so the test keeps its fail-without-the-fix
// property).
func TestIdleBoundedContext_NilWakeLiteralStillResets(t *testing.T) {
	const (
		window = 3 * time.Second
		touch  = 50 * time.Millisecond
		wait   = 7 * time.Second
	)
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = window
	defer func() { pluginInvokeNoProgressOverride = old }()

	a := &pluginActivity{} // deliberately a bare literal: wake is nil
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	defer stop()
	// Touch every `touch` (< the window) from a separate goroutine; if the event
	// channel were nil, each touch would never signal and the timer would fire at
	// `window` and cancel a call that is plainly progressing.
	done := make(chan struct{})
	go func() {
		tk := time.NewTicker(touch)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				a.touch()
			}
		}
	}()
	select {
	case <-ctx.Done():
		close(done)
		t.Fatalf("a bare-literal clock's touch must reset the deadline, got %v", context.Cause(ctx))
	case <-time.After(wait):
		// good: outlived >2x the window because the touches reset it
	}
	close(done)
}
