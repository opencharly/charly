package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/opencharly/spec/ops"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/storeroot"
)

// withoutDaemonDiscovery isolates a test from any real charly daemon on the developer's
// host (a concurrent build's buildkitd would otherwise be summed into the clock). The
// daemon-following arm has its own test that installs an explicit root set.
func withoutDaemonDiscovery(t *testing.T) {
	t.Helper()
	old := charlyDaemonPidsFn
	charlyDaemonPidsFn = func() []int { return nil }
	t.Cleanup(func() { charlyDaemonPidsFn = old })
}

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
	withoutDaemonDiscovery(t)
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

	// Progressing — and this arm is DETERMINISTIC, which is the whole point of the seam.
	//
	// The former arm raced a 50ms ticker against the 150ms window and asserted survival for
	// 600ms. That asserts something the scheduler does not owe anyone: the ticker's cadence
	// is a TARGET, and a loaded machine can starve the toucher past the whole window
	// (measured on this host: `host reverse legs=4 (last 244ms ago); window=150ms`), at
	// which point the guard is RIGHT to cancel — nothing attempted progress inside the
	// window — while the test reads that correctness as failure. Raising the multiplier
	// would only tune the test to the runner.
	//
	// So the arm is split into the two things that are each provable without a race:
	// (a) the guard's CONFIRMATION treats an ATTEMPTED touch as progress — the case that
	// makes the guard load-robust, proved directly below; and (b) a call whose progress
	// source is LIVE and still attempting is not cancelled, proved by advancing the
	// producer from THIS goroutine so the premise cannot be starved away.
	b := newPluginActivity(0)
	ctx2, stop2 := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), b)
	// stop2 runs LAST (the idle arm below needs the watcher still live), so it is deferred
	// rather than called inline; the toucher is stopped explicitly before that assertion.
	defer stop2()

	// The producer is represented by a real background toucher, as in production.
	touchStop := make(chan struct{})
	go func() {
		tk := time.NewTicker(20 * time.Millisecond)
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

	// (b) ADVANCE THE PRODUCER FROM HERE, so "the source is live" cannot be starved away:
	// the loop below runs on THIS goroutine, and it stops the moment the guard cancels, so
	// a cancellation is a REAL finding rather than a missed deadline. Assert the clock
	// counted the attempts and that the context is still alive while they happen.
	deadline := time.Now().Add(3 * pluginInvokeNoProgress())
	for time.Now().Before(deadline) {
		b.touch()
		select {
		case <-ctx2.Done():
			close(touchStop)
			t.Fatalf("a call whose progress source is live (attempts=%d) must not be cancelled: %v", b.touchAttempts(), context.Cause(ctx2))
		default:
		}
	}
	if got := b.touchAttempts(); got < 10 {
		t.Fatalf("the progress source made %d attempts over 3 windows — the arm did not exercise a live producer", got)
	}

	if ctx2.Err() != nil {
		close(touchStop)
		t.Fatalf("progressing call must not be cancelled while its source attempts progress, got %v", context.Cause(ctx2))
	}
	close(touchStop)

	// And IDLENESS still trips: once the producer stops, the guard cancels with the
	// sentinel — so the arm proves both directions rather than only the flattering one.
	select {
	case <-ctx2.Done():
		if !errors.Is(context.Cause(ctx2), errPluginCallIdle) {
			t.Fatalf("idle-after-progress call: expected errPluginCallIdle, got %v", context.Cause(ctx2))
		}
	case <-time.After(4 * pluginInvokeNoProgress()):
		t.Fatal("an idle call must still be cancelled once progress stops — the guard must not be held open by a producer that went quiet")
	}
}

// TestPluginActivity_ConfirmationAcceptsAnAttemptedTouch pins the EXPIRY path's third
// confirmation source, which is what makes the guard load-robust (#913): at expiry the guard
// re-measures deliveries, tree-work advance AND whether the producer ATTEMPTED a touch since
// the last reset. A delivery can be coalesced away and a producer can be starved, but an
// attempt is a fact the clock counts, so a live source is never mistaken for a dead one.
//
// Deterministic: the clock is built here, the token is seeded synchronously, and observe is
// called directly — no goroutine, no wall-clock race, nothing the scheduler can reorder.
func TestPluginActivity_ConfirmationAcceptsAnAttemptedTouch(t *testing.T) {
	a := newPluginActivity(0) // pid 0: the tree-work source contributes nothing
	const burst = 32
	for i := 0; i < burst; i++ {
		a.touch()
	}
	if got := a.touchAttempts(); got != burst {
		t.Fatalf("touchAttempts = %d after %d touches; a DELIVERY-based count would read %d, because the depth-1 buffer coalesces a burst to one pending token", got, burst, cap(a.wake))
	}
	// pid 0 turns the tree-work source off, so the ONLY things observe can find are the
	// pending host-leg token and the attempt count. This is the state a starved producer
	// leaves behind.
	if _, _, ok := a.observe(a.wake, procWork{}, 0); !ok {
		t.Fatal("the guard's expiry confirmation found nothing while a progress event was pending and touches had been attempted — a live producer would be killed as idle")
	}
	// And an attempt with NO pending token still counts: the token was consumed by the
	// guard's normal loop and the producer never got to send another. Without this, a burst
	// that coalesced into a consumed token reads as silence at expiry.
	a.touch()
	if attempts := a.touchAttempts(); attempts != burst+1 {
		t.Fatalf("touchAttempts = %d, want %d", attempts, burst+1)
	}
}

// TestIdleGuardStarvedProducerArm is the live-arm witness for #924, and the step the
// `check-idle-guard-starved` bed runs on its venue. It drives the REAL guard — touch(),
// observe() and idleBoundedContext() — through the controllable interleaving the seam
// provides (CHARLY_PLUGIN_TOUCH_GATE defers the SEND while the producer keeps ATTEMPTING),
// against the shape the #913 kill report describes: `peer pid=0`, a parked peer with no
// attributable tree, whose only progress source is the real heartbeat producer.
//
// The state it enters: an expiry at which there is NO drainable token, NO tree-work advance,
// and a GROWN attempt count. With the attempt source present the call SURVIVES every trial;
// with that one hunk reverted from observe() every trial is KILLED. The bed asserts both
// directions by re-running this same test against the mutated tree, so a bed that still
// passed with the mechanism removed would be caught rather than quoted.
func TestIdleGuardStarvedProducerArm(t *testing.T) {
	const window = 60 * time.Millisecond
	oldWindow, oldGate := pluginInvokeNoProgressOverride, pluginActivityTouchGate
	pluginInvokeNoProgressOverride = window
	pluginActivityTouchGate = 2 * window
	defer func() { pluginInvokeNoProgressOverride = oldWindow; pluginActivityTouchGate = oldGate }()

	const trials = 10
	for i := 0; i < trials; i++ {
		a := newPluginActivity(0)
		stopBeat := startPluginActivityHeartbeat(a)
		ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
		select {
		case <-ctx.Done():
			stopBeat()
			stop()
			t.Fatalf("trial %d: a producer that ATTEMPTED progress while nothing was delivered was read as a wedged plugin (attempts=%d): %v",
				i, a.touchAttempts(), context.Cause(ctx))
		case <-time.After(3 * window):
			// survived: the attempts carried the call
		}
		stopBeat()
		stop()
	}
}

// TestPluginActivity_TouchGateIsInertByDefault pins the SAFETY property of the instrument:
// the send gate is zero unless someone sets it, so a production binary's touch() is the same
// single non-blocking send it always was, with no wait and no goroutine.
func TestPluginActivity_TouchGateIsInertByDefault(t *testing.T) {
	if pluginActivityTouchGate != 0 {
		t.Fatalf("pluginActivityTouchGate = %s; the gate must be ZERO unless a bed sets it", pluginActivityTouchGate)
	}
	a := newPluginActivity(0)
	a.touch()
	select {
	case <-a.wake:
	default:
		t.Fatal("an ungated touch must still deliver its token synchronously")
	}
}

// TestPluginActivity_TouchGateDefersOnlyTheSend is the instrument's own proof, and the shape a
// live bed enters: with the gate set, touch() COUNTS the attempt and returns immediately while
// the delivery is still in flight — the production interleaving (a producer preempted between the
// attempt and its non-blocking send) held long enough to observe. Holding the PRODUCER instead
// would stop the attempts, and a count that stops growing is not evidence.
func TestPluginActivity_TouchGateDefersOnlyTheSend(t *testing.T) {
	old := pluginActivityTouchGate
	pluginActivityTouchGate = 60 * time.Millisecond
	defer func() { pluginActivityTouchGate = old }()

	a := newPluginActivity(0)
	start := time.Now()
	a.touch()
	if elapsed := time.Since(start); elapsed >= pluginActivityTouchGate {
		t.Fatalf("touch() held the producer for %s — the gate must defer the SEND, never the caller", elapsed)
	}
	if got := a.touchAttempts(); got != 1 {
		t.Fatalf("touchAttempts = %d immediately after a gated touch, want 1 — the attempt must be counted BEFORE the send", got)
	}
	select {
	case <-a.wake:
		t.Fatal("the gated send delivered its token synchronously; the gate must defer it")
	default:
	}
	select {
	case <-a.wake:
		// good: the real send arrived, late
	case <-time.After(4 * pluginActivityTouchGate):
		t.Fatal("the deferred send never arrived")
	}
}

// TestPluginActivity_TouchAttemptCountsAttemptsNotDeliveries pins the seam's ONE property,
// because getting it backwards would re-introduce exactly the confusion the seam exists to
// remove: a counter of ACCEPTED sends undercounts whenever the depth-1 buffer already holds
// a pending token, which is the normal case for a burst.
func TestPluginActivity_TouchAttemptCountsAttemptsNotDeliveries(t *testing.T) {
	a := newPluginActivity(0)
	const burst = 64
	for i := 0; i < burst; i++ {
		a.touch()
	}
	if got := a.touchAttempts(); got != burst {
		t.Fatalf("touchAttempts = %d after %d touches; a delivery-based count would read %d (the depth-1 buffer coalesces)", got, burst, cap(a.wake))
	}
	// A NIL clock is safe and reports zero, so a server without a clock never trips.
	var nilClock *pluginActivity
	if nilClock.touchAttempts() != 0 {
		t.Fatal("a nil clock must report 0 attempts")
	}
	nilClock.touch() // must not panic
}

// TestPluginInvokeErr_CarriesTheStallReport is the regression guard for the FIRST R10 run of
// this change: the watchdog cancelled with fmt.Errorf("%w: %s", errPluginCallIdle,
// stallReport), but pluginInvokeErr re-wrapped the BARE sentinel, so the bed got a conclusion
// with no evidence — the exact defect #699 is about, reproduced by the fix for it, and
// invisible to errors.Is. The report has to survive to the caller.
func TestPluginInvokeErr_CarriesTheStallReport(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 100 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// A real idle kill, deterministic: pid 0 turns the CPU source off, so only the absent
	// host legs are watched and the 100ms window always trips.
	a := newPluginActivity(0)
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the idle guard did not trip on a genuinely idle clock")
	}

	// The watchdog wrapped this report around the sentinel; the caller must receive that
	// very string, not one it rebuilt. Comparing the cause's report to the caller's error
	// directly is exact and deterministic — recomputing the report here would not be, since
	// it carries the AGE of the last host leg, which moves.
	cause := context.Cause(ctx).Error()
	report, ok := strings.CutPrefix(cause, errPluginCallIdle.Error()+": ")
	if !ok || report == "" {
		t.Fatalf("the cause must wrap the sentinel and the watchdog's report.\ncause: %v", cause)
	}
	if !strings.Contains(report, "peer tree at kill:") || !strings.Contains(report, "host reverse legs=") {
		t.Fatalf("the cause's report must carry the measurement, not just a phrase.\ncause: %v", cause)
	}

	err := pluginInvokeErr(ctx, "InvokeProvider deploy:dummy op=rebuild",
		errors.New("rpc error: code = Unknown desc = peer cancelled"))
	if !errors.Is(err, errPluginCallIdle) {
		t.Fatalf("a caller must still recognise the idle kill: got %v", err)
	}
	if !strings.HasSuffix(err.Error(), report) {
		t.Fatalf("THE BUG: the report must reach the caller, not just the sentinel.\ngot: %v\nreport: %s", err, report)
	}
	if !strings.Contains(err.Error(), "InvokeProvider deploy:dummy op=rebuild") {
		t.Fatalf("the call site must still be named: got %v", err)
	}
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
	startInOwnGroup(t, cmd)

	// The clock polls the BURNER's pid directly (standing in for the plugin pid: the
	// plugin's own CPU is what we must observe).
	a := newPluginActivity(cmd.Process.Pid)
	// Baseline via procCPUTicks DIRECTLY (not a.work()) so this test still exercises the
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
	startInOwnGroup(t, cmd)
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
	startInOwnGroup(t, cmd)
	if got := processPid(cmd); got != cmd.Process.Pid {
		t.Fatalf("processPid(started) = %d, want %d", got, cmd.Process.Pid)
	}
	// And a grpcProvider built with that pid surfaces it via newProviderActivity, while a
	// COMPILED-IN provider watches the host's descendants instead of a nonexistent peer pid.
	gp := &grpcProvider{pid: cmd.Process.Pid}
	if a := newProviderActivity(gp); a.pid != cmd.Process.Pid || a.excludeRoot {
		t.Fatalf("newProviderActivity(grpc pid %d) = pid %d excludeRoot %v, want pid %d excludeRoot false",
			cmd.Process.Pid, a.pid, a.excludeRoot, cmd.Process.Pid)
	}
	if a := newProviderActivity(blockingProvider{}); a.pid != os.Getpid() || !a.excludeRoot {
		t.Fatalf("newProviderActivity(compiled-in) = pid %d excludeRoot %v, want host pid %d excludeRoot true",
			a.pid, a.excludeRoot, os.Getpid())
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
// 7s wait when the arming is absent (so the test keeps its fail-without-the-fix
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

// TestIdleBoundedContext_PeerWaitingOnBusyChildIsProgress is THE reproduction for
// opencharly/charly#699: a peer plugin process whose ONLY work is one long-lived
// CPU-active child. The peer forks the child and then blocks in wait(); the child burns
// CPU. The peer's OWN /proc/<pid>/stat utime+stime is frozen (it is asleep), and
// cutime+cstime aggregate a descendant's CPU only once that descendant has been REAPED
// — so while the child is still running, procCPUTicks(peer) stands still. The guard
// polls exactly that number and has no other signal here, so it cancels a call that is
// plainly progressing. This is a false positive BY CONSTRUCTION, at the peer's own
// cadence: no host load or timing luck can avoid it.
//
// TestIdleBoundedContext_PluginLocalCPUIsProgress does NOT cover this: it samples the
// BURNER's own pid, i.e. it stands in for a plugin that runs its work as itself. The
// uncovered shape is a peer that is merely the PARENT of the work — which is what the
// deploy plugin is while its prepare-venue children (the venv build, the engine
// invocation) run.
func TestIdleBoundedContext_PeerWaitingOnBusyChildIsProgress(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// The peer: forks the busy grandchild, then blocks in wait(). `& wait` is TWO
	// statements, which defeats the shell's single-command exec optimisation, so the
	// outer sh really is the parent that blocks while the inner sh burns CPU.
	cmd := exec.Command("sh", "-c",
		"sh -c 'i=0; while [ $i -lt 100000000 ]; do i=$((i+1)); done' & wait")
	startInOwnGroup(t, cmd)
	peerPid := cmd.Process.Pid

	// PREMISE, self-validated (the idiom the CPU test uses): the peer has a live child,
	// that child's CPU advances, and the peer's OWN ticks do not. If any leg does not
	// hold on this kernel the gap is not being exercised, and the test says so rather
	// than passing vacuously.
	childPid := 0
	for i := 0; i < 200 && childPid == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		childPid = liveChildPid(peerPid)
	}
	if childPid == 0 {
		t.Fatalf("no live child of peer %d — a peer blocked on a busy child is not exercised", peerPid)
	}
	var childFirst, peerFirst uint64
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		childFirst, peerFirst = procCPUTicks(childPid), procCPUTicks(peerPid)
		if childFirst > 0 {
			break
		}
	}
	// The reader is validated on the CHILD (a nonzero sample proves procCPUTicks works
	// here). The PEER's own count is legitimately 0 — a shell that has forked and blocked
	// in wait() has accrued no utime/stime of its own, and its child's CPU stays in
	// cutime/cstime only after a reap. That 0 is precisely the trap: procCPUTicks returns
	// 0 for "frozen" and 0 for "unreadable" alike, so the guard cannot tell a wedged peer
	// from a parent whose work is entirely in a live child.
	if childFirst == 0 {
		t.Fatalf("procCPUTicks never left 0 for the peer's busy child — the CPU reader is not exercised")
	}
	if !procAlive(peerPid) {
		t.Fatalf("peer %d is not alive — the reproduction's premise is broken", peerPid)
	}
	time.Sleep(400 * time.Millisecond)
	childSecond, peerSecond := procCPUTicks(childPid), procCPUTicks(peerPid)
	if childSecond <= childFirst {
		t.Fatalf("the peer's child must be burning CPU (child %d -> %d)", childFirst, childSecond)
	}
	if peerSecond != peerFirst {
		t.Skipf("peer ticks advanced (%d -> %d) — a peer that accrues its child's CPU while it is live is outside this gap",
			peerFirst, peerSecond)
	}
	if !procAlive(peerPid) {
		t.Fatalf("peer %d died before the assertion — the reproduction's premise is broken", peerPid)
	}

	// THE ASSERTION: the peer IS progressing (its child is), so the idle guard must not
	// cancel. On the guard as shipped this FAILS — the peer's frozen ticks reset nothing
	// and there is no host leg, so the 300ms timer fires and cancels.
	a := newPluginActivity(peerPid)
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	defer stop()
	select {
	case <-ctx.Done():
		t.Fatalf("a peer blocked in wait() on a CPU-active child must not be idle-killed, got %v", context.Cause(ctx))
	case <-time.After(1 * time.Second):
		// good: outlived >3x the window on its child's work alone
	}
}

// liveChildPid returns the pid of the first live direct child of pid, or 0 when there is
// none. /proc/<n>/stat is comm-parenthesised (field 2 may contain spaces), so the fields
// are read from the LAST ')' — fields[0]=state (field 3), fields[1]=ppid (field 4). A
// zombie is not live: its CPU has already been folded into the parent's cutime, which is
// exactly the accounting difference this test is about.
//
// These three helpers are TEST scaffolding and deliberately do not share names with the
// guard's own /proc readers: the production fix needs its own live-descendant tick sum
// (a peer's children are the signal), and a name clash between the two would be a compile
// error rather than a test failure.
func procStatFields(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 || i+2 >= len(b) {
		return nil
	}
	f := strings.Fields(string(b[i+2:]))
	if len(f) < 2 {
		return nil
	}
	return f
}

// TestObserve_PendingLegEventAtExpiryIsProgress pins the confirmation rule the kill path
// depends on: a host-leg progress EVENT that is sitting unread in the depth-1 wake buffer
// at the moment the deadline expires counts as progress, so the call is not killed.
// touch() is a non-blocking depth-1 send, so a coalesced burst can leave exactly one
// token pending, and Go's select picks at RANDOM among ready cases — an expiry can thus
// win with an unread touch in the buffer. observe() must drain it.
func TestObserve_PendingLegEventAtExpiryIsProgress(t *testing.T) {
	a := newPluginActivity(0) // pid 0: the tree-work source contributes nothing here
	a.touch()                 // one pending event = the coalesced burst still waiting
	if _, _, ok := a.observe(a.wake, procWork{}, 0); !ok {
		t.Fatal("a pending host-leg event must count as progress at confirmation")
	}
	if a.legs.Load() != 1 {
		t.Fatalf("the drained event must be counted as a host leg, got %d", a.legs.Load())
	}
	// Second read: the buffer is empty, there is no tree-work source, and the producer has
	// attempted nothing NEW since the last re-arm — so nothing.
	if _, _, ok := a.observe(a.wake, procWork{}, a.touchAttempts()); ok {
		t.Fatal("an empty buffer with a frozen peer and no new attempt must not report progress")
	}
}

// TestObserve_AttemptedTouchIsProgress is the #913 unit-level form, and it pins the THIRD
// source on its own: no leg event is delivered and the peer burns no CPU, yet the producer
// ATTEMPTED a touch since the last re-arm — so the call is progressing and must not be
// killed. That is the state a starved producer leaves behind on a loaded machine, and
// without this source the guard reads a live producer as a wedged plugin (measured:
// `host reverse legs=4 (last 244ms ago); window=150ms`).
//
// Deterministic: pid 0 turns the tree-work source off, the buffer is drained explicitly, and
// the attempt count is advanced explicitly — nothing here depends on the scheduler.
func TestObserve_AttemptedTouchIsProgress(t *testing.T) {
	a := newPluginActivity(0)
	baseline := a.touchAttempts()

	// A producer that is alive but whose token never landed and whose tree is frozen.
	a.touch()
	if a.wake == nil {
		t.Fatal("the clock must have a live wake channel")
	}
	<-a.wake // consume it: now NOTHING is pending, exactly as after a coalesced burst

	if _, _, ok := a.observe(a.wake, procWork{}, baseline); !ok {
		t.Fatal("an ATTEMPTED touch since the last re-arm must count as progress even with no pending token and no tree-work advance — otherwise a starved producer is reported as idle")
	}

	// And with no new attempt at all, nothing is progress: the source has gone quiet.
	quiet := a.touchAttempts()
	if _, _, ok := a.observe(a.wake, procWork{}, quiet); ok {
		t.Fatal("with no delivery, no tree-work advance and no new attempt the guard must report NO progress, or it could never kill a wedged peer")
	}
}

// TestObserve_LiveChildCPUIsProgress is the unit-level form of the #699 gap: the CPU
// source must see the peer's LIVE CHILD, not only the peer's own (frozen) count.
func TestObserve_LiveChildCPUIsProgress(t *testing.T) {
	cmd := exec.Command("sh", "-c",
		"sh -c 'i=0; while [ $i -lt 100000000 ]; do i=$((i+1)); done' & wait")
	startInOwnGroup(t, cmd)

	peer := newPluginActivity(cmd.Process.Pid)
	var base procWork
	for i := 0; i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
		if base = peer.work(); base.cpuTicks > 0 {
			break
		}
	}
	if base.cpuTicks == 0 {
		t.Fatal("the peer tree's CPU never left 0 — the tree walk is not reading the child")
	}
	time.Sleep(200 * time.Millisecond)
	if _, _, ok := peer.observe(nil, base, peer.touchAttempts()); !ok {
		t.Fatalf("the peer's live child is burning CPU but observe reported no progress (high-water %d, tree now %d)",
			base.cpuTicks, peer.work().cpuTicks)
	}
}

// TestObserve_CompiledInPeerDescendantIOIsProgress pins the build half of #1088: a
// COMPILED-IN peer has no pid, so its clock watches the HOST's descendants, and a
// descendant that is MOVING BYTES is progress even when CPU barely advances — the shape
// of the client-side OCI export and the engine load that the default 90s window killed.
// The child writes a file continuously; nothing heartbeats the clock.
func TestObserve_CompiledInPeerDescendantIOIsProgress(t *testing.T) {
	withoutDaemonDiscovery(t)
	out := filepath.Join(t.TempDir(), "io")
	cmd := exec.Command("sh", "-c", `while :; do printf xxxxxxxxxxxxxxxx; done > "$1"`, "sh", out)
	startInOwnGroup(t, cmd)

	a := newCompiledInPluginActivity()
	base := a.work()
	deadline := time.Now().Add(2 * time.Second)
	for a.work().ioBytes <= base.ioBytes && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cur := a.work()
	if cur.ioBytes <= base.ioBytes {
		t.Fatalf("the host descendant's I/O never advanced (%d -> %d) — the compiled-in peer's work is not watched",
			base.ioBytes, cur.ioBytes)
	}
	if _, _, ok := a.observe(nil, base, a.touchAttempts()); !ok {
		t.Fatalf("a host descendant moving %d I/O bytes is real work, but observe reported no progress", cur.ioBytes-base.ioBytes)
	}
}

// TestIdleBoundedContext_CompiledInPeerDescendantWorkIsProgress is the R7 regression guard
// for opencharly/charly#1088 (item (c) of opencharly/.github#263): a compiled-in peer whose
// work is a HOST CHILD running past the no-progress window must not be killed, while the
// PRE-FIX source — pid 0, "no peer pid, watches nothing" — kills the very same call.
// The failing-without-the-fix arm is the pid-0 clock below; the child only writes a file
// (real I/O), so the surviving call is evidence of work, never a fabricated heartbeat.
func TestIdleBoundedContext_CompiledInPeerDescendantWorkIsProgress(t *testing.T) {
	withoutDaemonDiscovery(t)
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	out := filepath.Join(t.TempDir(), "io")
	cmd := exec.Command("sh", "-c", `while :; do printf xxxxxxxxxxxxxxxx; done > "$1"`, "sh", out)
	startInOwnGroup(t, cmd)

	// The fixed clock: the compiled-in peer's clock watches the host's descendants.
	a := newCompiledInPluginActivity()
	base := a.work()
	deadline := time.Now().Add(2 * time.Second)
	for a.work().ioBytes <= base.ioBytes && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.work().ioBytes <= base.ioBytes {
		t.Fatalf("the host descendant's I/O never advanced (%d -> %d) — the compiled-in peer's work is not watched",
			base.ioBytes, a.work().ioBytes)
	}
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	defer stop()
	select {
	case <-ctx.Done():
		t.Fatalf("a compiled-in peer whose host child is writing files must not be idle-killed: %v", context.Cause(ctx))
	case <-time.After(time.Second):
		// good: outlived >3x the window on the child's I/O alone
	}

	// The PRE-FIX source over the SAME live child: pid 0 watches no tree, so the
	// 300ms window trips. This is the control that FAILS without the change.
	legacy := newPluginActivity(0)
	legacyCtx, legacyStop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), legacy)
	defer legacyStop()
	select {
	case <-legacyCtx.Done():
		if !errors.Is(context.Cause(legacyCtx), errPluginCallIdle) {
			t.Fatalf("pre-fix source: expected errPluginCallIdle, got %v", context.Cause(legacyCtx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pid-0 source did not trip — the negative arm is not exercising the blindness it claims")
	}
}

// TestIdleBoundedContext_CompiledInPeerNoDescendantWorkTrips keeps the GENUINE-HANG arm:
// with the compiled-in source in place, a peer that spawns no working descendant still
// trips. The host ROOT is excluded from the sum for exactly this reason — the charly
// process's own runtime/GC ticks must not mask a wedged in-process call. Without this arm
// the positive test above could be satisfied by disabling the guard.
func TestIdleBoundedContext_CompiledInPeerNoDescendantWorkTrips(t *testing.T) {
	withoutDaemonDiscovery(t)
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	a := newCompiledInPluginActivity()
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	defer stop()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), errPluginCallIdle) {
			t.Fatalf("expected errPluginCallIdle, got %v", context.Cause(ctx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a compiled-in peer with no working descendant must still trip the idle guard")
	}
}

// TestCharlyDaemonPids_FindsProcessesUnderTheCharlyCacheRoot pins the discovery itself: a
// process whose argv names a path under charly's own cache root is a charly-owned daemon
// root. This is deliberately kind-agnostic (the argv path is the only signal), which is what
// lets a detached `buildkitd` be followed without a core kind word. The probe path comes from
// the SAME resolver the discovery uses, so the two cannot disagree about where the cache is.
func TestCharlyDaemonPids_FindsProcessesUnderTheCharlyCacheRoot(t *testing.T) {
	cache, err := storeroot.CacheDir()
	if err != nil {
		t.Skipf("no user cache dir: %v", err)
	}
	assertDaemonFound(t, filepath.Join(cache, "watchdog-probe-daemon"))
}

// TestCharlyDaemonPids_FollowsTheCacheDirOverride is the arm that FAILS without the change: a
// relocated store (`CHARLY_CACHE_DIR`) is where this charly puts its daemons, so a daemon whose
// argv names a path under the RELOCATED root must be found. The pre-change discovery composed
// `$XDG_CACHE_HOME/charly` by hand and ignored the override, so it saw only the default root —
// and on a relocated install it was blind to the very `buildkitd` its idle guard exists to
// follow. Both other candidate roots are pinned to unrelated temp dirs here, so the arm cannot
// pass by coincidence.
func TestCharlyDaemonPids_FollowsTheCacheDirOverride(t *testing.T) {
	base := t.TempDir()
	relocated := filepath.Join(base, "relocated-store")
	t.Setenv("CHARLY_CACHE_DIR", relocated)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "xdg-cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "xdg-run"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "xdg-state"))

	if got, err := storeroot.CacheDir(); err != nil || got != relocated {
		t.Fatalf("premise: storeroot.CacheDir() = %q, %v; want the override %q", got, err, relocated)
	}
	assertDaemonFound(t, filepath.Join(relocated, "watchdog-probe-daemon"))
}

// assertDaemonFound starts a process whose argv carries probePath and requires
// charlyDaemonPids to report its pid.
func assertDaemonFound(t *testing.T, probePath string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `while :; do sleep 1; done`, "sh", probePath)
	startInOwnGroup(t, cmd)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, pid := range charlyDaemonPids() {
			if pid == cmd.Process.Pid {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("charlyDaemonPids did not find a process whose argv names %s", probePath)
}

// TestIdleBoundedContext_CompiledInPeerDetachedDaemonTreeIsProgress is the second R7 arm
// for #1088: a compiled-in build's heavy phase runs in buildkitdrive's DETACHED daemon
// (rootlesskit RE-EXECs, so the tree is reparented to init), which is NOT a descendant of
// the charly process. The client can be silent longer than the window while the daemon
// works — measured on the final `pod-crabbox` RUN. This test double-forks a writer, proves
// it is not a host descendant, and requires the daemon-following source to see its I/O;
// with discovery OFF the same call trips, which is the arm that fails without the change.
func TestIdleBoundedContext_CompiledInPeerDetachedDaemonTreeIsProgress(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 300 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "daemon.pid")
	out := filepath.Join(dir, "io")
	// The outer shell starts the writer, records its pid, and exits — so the writer is
	// reparented AWAY from this test process, exactly like a detached daemon.
	cmd := exec.Command("sh", "-c",
		`sh -c 'echo $$ > "$1"; while :; do printf xxxxxxxxxxxxxxxx; done > "$2"' sh "$1" "$2" & exit 0`,
		"sh", pidFile, out)
	startInOwnGroup(t, cmd)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("detached writer's launcher: %v", err)
	}
	daemonPid := readPidFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(daemonPid, syscall.SIGKILL) })

	// Premise: the writer is NOT in this process's descendant tree — the shape that made
	// the descendant-only source still kill the build.
	for _, p := range procTreeProcs(os.Getpid()) {
		if p.pid == daemonPid {
			t.Skipf("pid %d reparented to the test process (subreaper) — the detached shape is not exercised", daemonPid)
		}
	}

	charlyDaemonPidsFn = func() []int { return []int{daemonPid} }
	defer func() { charlyDaemonPidsFn = charlyDaemonPids }()

	a := newCompiledInPluginActivity()
	base := a.work()
	deadline := time.Now().Add(2 * time.Second)
	for a.work().ioBytes <= base.ioBytes && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cur := a.work(); cur.ioBytes <= base.ioBytes {
		t.Fatalf("detached writer pid %d is moving bytes but the watched tree saw none (%d -> %d)",
			daemonPid, base.ioBytes, cur.ioBytes)
	}
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	select {
	case <-ctx.Done():
		t.Fatalf("a compiled-in peer whose DETACHED daemon is working must not be idle-killed: %v", context.Cause(ctx))
	case <-time.After(time.Second):
		// good: outlived >3x the window on the detached daemon's I/O
	}
	// JOIN the watcher before changing the discovery seam: stop() waits for the goroutine
	// to exit, so the negative arm's seam write cannot race the positive arm's last sample.
	stop()

	// The control: with daemon discovery OFF, the same live writer is invisible to the
	// clock, so the window trips. This is the arm that FAILS without the change.
	charlyDaemonPidsFn = func() []int { return nil }
	blind := newCompiledInPluginActivity()
	blindCtx, blindStop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), blind)
	defer blindStop()
	select {
	case <-blindCtx.Done():
		if !errors.Is(context.Cause(blindCtx), errPluginCallIdle) {
			t.Fatalf("descendant-only source: expected errPluginCallIdle, got %v", context.Cause(blindCtx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the descendant-only source did not trip — the control is not exercising the detached-daemon blindness")
	}
}

// readPidFile reads a pid a helper wrote, retrying because the writer starts asynchronously.
func readPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the detached writer never wrote a pid to %s", path)
	return 0
}

// TestResetDeadline_NonBlockingOnTheExpiryPath puts the timer in EXACTLY the state the
// watcher's expiry arm is in when it confirms progress at expiry: the deadline fired AND
// its value has already been received. resetDeadline must return from there without
// blocking. A bare `<-timer.C` drain — the shape the review warned about — blocks FOREVER
// in this state, so the goroutine below would never finish and the test fails on its
// deadline. That is the failure mode worth pinning: a wedged watcher never kills and never
// returns, which is the worst possible outcome for a guard.
func TestResetDeadline_NonBlockingOnTheExpiryPath(t *testing.T) {
	const d = 30 * time.Millisecond
	timer := time.NewTimer(d)
	defer timer.Stop()
	<-timer.C // the expiry arm has taken the value; Stop() now reports false

	done := make(chan struct{})
	go func() {
		resetDeadline(timer, d)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("resetDeadline blocked after the expiry value was consumed — a wedged watcher")
	}
	// It must also have RE-ARMED the deadline, not merely returned.
	select {
	case <-timer.C:
	case <-time.After(5 * time.Second):
		t.Fatal("resetDeadline returned but did not re-arm the timer")
	}
}

// TestResetDeadline_NonBlockingWithStalePendingExpiry is the OTHER state the reset can see:
// the timer expired but its value is still sitting in the channel UNREAD. The drain must
// consume it — without the drain, Reset leaves the stale value in place and the re-armed
// timer fires IMMEDIATELY, which would trip a spurious expiry on a call that just proved
// progress. The check right after the reset is non-blocking and load-independent: if the
// stale value were still there it would be available at once.
func TestResetDeadline_NonBlockingWithStalePendingExpiry(t *testing.T) {
	const d = 100 * time.Millisecond
	timer := time.NewTimer(d)
	defer timer.Stop()
	time.Sleep(3 * d) // expired; the value is pending and unread

	resetDeadline(timer, d)
	select {
	case <-timer.C:
		t.Fatal("the stale expiry was not drained — the re-armed timer fired immediately")
	default:
		// good: nothing pending, so the fresh window d is genuinely fresh
	}
	select {
	case <-timer.C: // the properly re-armed deadline, once, at ~d
	case <-time.After(5 * time.Second):
		t.Fatal("resetDeadline did not re-arm the timer")
	}
}

// TestProcTreeProcs_PreOrder pins the walk's ORDER contract. The doc claimed
// "breadth-first", but the implementation is a LIFO stack — depth-first pre-order. The
// property that matters is the one a tree-shaped report needs: every process PRECEDES its
// own descendants, so the root is first and no node is visited before its parent. It is
// asserted over the peer's REAL tree rather than a hand-built one, so it holds for whatever
// shape the kernel actually presents.
func TestProcTreeProcs_PreOrder(t *testing.T) {
	cmd := exec.Command("sh", "-c",
		"sh -c 'i=0; while [ $i -lt 100000000 ]; do i=$((i+1)); done' & wait")
	startInOwnGroup(t, cmd) // cleanup kills the WHOLE group, never orphaning the burner
	root := cmd.Process.Pid

	var procs []procInfo
	for i := 0; i < 100; i++ { // the child may not be visible on the very first read
		procs = procTreeProcs(root)
		if len(procs) > 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(procs) < 2 {
		t.Fatalf("the peer tree has %d process(es), need root + child — the reproduction's premise is broken", len(procs))
	}
	if procs[0].pid != root {
		t.Fatalf("the root must be visited first, got pid %d", procs[0].pid)
	}
	seen := map[int]bool{}
	for _, p := range procs {
		if p.pid != root && !seen[p.ppid] {
			t.Fatalf("pid %d (%s) is visited before its parent %d — the walk is not pre-order", p.pid, p.comm, p.ppid)
		}
		seen[p.pid] = true
	}
	if !procAlive(root) {
		t.Fatal("the peer died during the walk — the assertion above would be vacuous")
	}
}

// procAlive reports whether pid exists and is not a zombie. Through procCPUTicks alone a
// frozen tick count and a dead process are indistinguishable — both read 0 — so the test
// checks liveness separately and never lets a missing peer masquerade as the gap.
func procAlive(pid int) bool {
	f := procStatFields(pid)
	return f != nil && f[0] != "Z"
}

func liveChildPid(pid int) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n <= 0 {
			continue
		}
		f := procStatFields(n)
		if f == nil || f[0] == "Z" {
			continue
		}
		if pp, err := strconv.Atoi(f[1]); err == nil && pp == pid {
			return n
		}
	}
	return 0
}

// startInOwnGroup starts cmd as the leader of its OWN process group and registers a
// cleanup that kills the WHOLE group — the child AND every process it has forked.
//
// Killing only cmd.Process is not enough HERE, and that is not a hypothetical: the #699
// tests spawn a peer that forks a CPU-burning grandchild (`sh -c '…' & wait`), so
// cmd.Process.Kill() reaps the peer and ORPHANS the burner. The orphan is reparented to
// the init process and then spins at 100% of a core until its loop ends — minutes —
// invisible to the test that made it. Two of these tests in one `go test` run leave the
// host above the load a disposable bed requires: this change's own R10 bed has to run on
// an IDLE host, and a leaked burner poisons exactly that, which is the same
// starved-host confound the idle guard's kill report warns about. A process group is the
// only handle that still reaches a grandchild the peer has already forked.
func startInOwnGroup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %q: %v", strings.Join(cmd.Args, " "), err)
	}
	// Setpgid puts the child in a group whose id is its own pid, so -pgid names exactly
	// this child's tree and nothing else (never the test binary's own group).
	pgid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
}
