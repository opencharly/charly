package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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
	a := newPluginActivity(0) // pid 0: the CPU source contributes nothing here
	a.touch()                 // one pending event = the coalesced burst still waiting
	if _, ok := a.observe(a.wake, 0); !ok {
		t.Fatal("a pending host-leg event must count as progress at confirmation")
	}
	if a.legs.Load() != 1 {
		t.Fatalf("the drained event must be counted as a host leg, got %d", a.legs.Load())
	}
	// Second read: the buffer is now empty and there is no CPU source, so nothing.
	if _, ok := a.observe(a.wake, 0); ok {
		t.Fatal("an empty buffer with a frozen peer must not report progress")
	}
}

// TestObserve_LiveChildCPUIsProgress is the unit-level form of the #699 gap: the CPU
// source must see the peer's LIVE CHILD, not only the peer's own (frozen) count.
func TestObserve_LiveChildCPUIsProgress(t *testing.T) {
	cmd := exec.Command("sh", "-c",
		"sh -c 'i=0; while [ $i -lt 100000000 ]; do i=$((i+1)); done' & wait")
	startInOwnGroup(t, cmd)

	peer := newPluginActivity(cmd.Process.Pid)
	var base uint64
	for i := 0; i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
		if base = peer.cpu(); base > 0 {
			break
		}
	}
	if base == 0 {
		t.Fatal("the peer tree's CPU never left 0 — the tree walk is not reading the child")
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := peer.observe(nil, base); !ok {
		t.Fatalf("the peer's live child is burning CPU but observe reported no progress (high-water %d, tree now %d)",
			base, peer.cpu())
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
