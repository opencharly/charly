package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// plugin_activity.go — the per-call PLUGIN IDLE guard. It replaces the former
// total-duration cap on the plugin→plugin dispatch (InvokeProvider).
//
// WHY: `defaultPluginInvokeTimeout` (10m) was added as a HUNG-PLUGIN guard (#474):
// a plugin deadlock (the deploy-del futex_wait hang — plugins waiting on a peer
// that waits on them, 0% CPU) must fail fast rather than wedge the host forever.
// But applying it as a TOTAL-duration bound also guillotined calls that are long
// BY DESIGN and actively progressing: a full unattended OS reinstall
// (`charly update` → deploy-dispatch op=rebuild → plugin-deploy-vm) legitimately
// runs ~13 min and was cut off at exactly 10m with `context deadline exceeded`
// (measured: check-omarchy-iso-vm's R10 [update], 788 s, after 64/64 check-live).
//
// THE SIGNAL IS HOST-VISIBLE WORK, not wall-clock. Every reverse-channel LEG the
// host performs for the peer plugin (RunSystem/RunUser/PutFile/GetFile/RunCapture/
// RunStream/RunInteractive/RunHostStep/HostBuild) heartbeats the call's activity
// clock for its duration, and a long-running host
// CHILD (host_build_cli's `charly …`, where the install actually runs) heartbeats
// while it is alive. A call is IDLE only when no such activity occurred within the
// no-progress window:
//   - the WEDGED plugin produces none (its peers are all blocked; no host leaf, no
//     child) → trips the window, the #474 guarantee preserved;
//   - the INSTALLING plugin produces a steady stream (the host child is alive for
//     the whole install) → never trips, however long it runs.
//
// PER-CALL, never global: the clock lives in the invoke context and is attached to
// the reverse server that serves that call, so a hung call cannot be kept alive by
// a DIFFERENT call's progress (the concurrency mask a global clock would allow).
//
// NO ABSOLUTE CAP — matching ResolvedReadiness.WatchProgress for an intentionally
// unbounded long-runner: a wall-clock ceiling would re-introduce the exact false
// kill this replaces. The window is the project's readiness `no_progress` (the SAME
// field the executors' watchdogs use — R3), config-sourced, not a new literal.

// errPluginCallIdle is the sentinel the idle watchdog cancels with, so a caller can
// distinguish a real hang from an ordinary timeout/transport error.
var errPluginCallIdle = errors.New("plugin call made no host-visible progress within the no-progress window (hung plugin — the peer runs no host work)")

// Two INDEPENDENT test seams (never one var driving both bounds, which would let a
// leaf-cap test silently shrink the idle window): pluginInvokeNoProgressOverride for the
// idle/no-progress window, pluginLeafCapOverride for the host→plugin leaf total cap.
// A test seam only; production leaves both 0. Read before the watchdog starts.
var (
	pluginInvokeNoProgressOverride time.Duration
	pluginLeafCapOverride          time.Duration
)

// pluginActivity is one call's forward-progress clock. A nil *pluginActivity is safe:
// touch is a no-op and idleFor reports 0, so a server without a clock never trips.
//
// TWO activity sources feed it:
//   - touch() from the host reverse legs (host-visible work), and
//   - cpu() polling of the PEER PLUGIN PROCESS's monotonic CPU. The second is
//     load-bearing: a plugin can do all its work PLUGIN-LOCALLY (the vm deploy's
//     console bootstrap runs `virsh send-key` and its ssh readiness retries as the
//     plugin's OWN children), which touches no host leg — measured: the idle guard
//     false-killed a legitimately-booting ISO guest at prepare-venue because of it.
//     /proc/<pid>/stat's utime+stime+cutime+cstime is monotonic and AGGREGATES reaped
//     descendants' CPU, so a retry loop (ssh every few seconds) advances it while a
//     futex-wedged plugin (no work, no children) leaves it frozen.
type pluginActivity struct {
	nanos atomic.Int64
	// pid is the peer plugin process whose CPU counts as progress; 0 = no polling
	// (an in-proc/builtin peer on the host legs only, or an unplumbed pid).
	pid int
	// wake is the PROGRESS EVENT channel: touch() signals it non-blockingly and the
	// idle watchdog RESETS its deadline on each signal. This is what makes the guard
	// event-driven rather than sample-driven — a progress event arriving before the
	// deadline resets it, so there is NO race between the producer's touch cadence and
	// the watchdog's check cadence (the sampled form compared a stale timestamp against
	// the full window and false-tripped whenever the two cadences were equal under
	// load). Buffered size 1: coalesces bursts to a single pending reset.
	wake chan struct{}
}

// newPluginActivity builds a call's activity clock with its wake channel initialized.
// The zero value remains safe for a nil clock (touch is a no-op, idleFor reports 0),
// but a clock actually watched by idleBoundedContext must be built here so touch() can
// signal the event.
func newPluginActivity(pid int) *pluginActivity {
	return &pluginActivity{pid: pid, wake: make(chan struct{}, 1)}
}

func (a *pluginActivity) touch() {
	if a == nil {
		return
	}
	a.nanos.Store(time.Now().UnixNano())
	// Signal the progress EVENT (non-blocking; a full buffer already holds a pending
	// reset, which is equivalent to this one).
	if a.wake != nil {
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
}

// cpu reads the plugin process's monotonic CPU ticks (utime+stime+cutime+cstime) from
// /proc/<pid>/stat; 0 when unavailable (no pid, non-Linux, process gone). The caller
// only ever compares it for ADVANCE, never as an absolute.
func (a *pluginActivity) cpu() uint64 {
	if a == nil || a.pid <= 0 {
		return 0
	}
	return procCPUTicks(a.pid)
}

func (a *pluginActivity) idleFor() time.Duration {
	if a == nil {
		return 0
	}
	last := a.nanos.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

// pluginActivityKey carries the per-call clock on the invoke context, so the reverse
// server created for the peer (plugin_grpc.go InvokeWithExecutor) and every host leg
// reach the SAME clock without a global.
type pluginActivityKey struct{}

func withPluginActivity(ctx context.Context, a *pluginActivity) context.Context {
	return context.WithValue(ctx, pluginActivityKey{}, a)
}

func pluginActivityFrom(ctx context.Context) *pluginActivity {
	a, _ := ctx.Value(pluginActivityKey{}).(*pluginActivity)
	return a
}

// pluginLeafCapDefault is the total-duration bound for a host→plugin LEAF call — one
// that attaches NO reverse channel, so the plugin performs no host-visible work the
// host could use as a progress signal, leaving a total cap as the only correct guard.
// The VALUE IS THE FORMER HARDCODED 10m, UNCHANGED: this cutover fixes the
// plugin→plugin long-call false kill, and must not silently loosen the leaf guard.
const pluginLeafCapDefault = 10 * time.Minute

// pluginLeafCap resolves the leaf bound: the test override, else pluginLeafCapDefault.
func pluginLeafCap() time.Duration {
	if pluginLeafCapOverride > 0 {
		return pluginLeafCapOverride
	}
	return pluginLeafCapDefault
}

// providerPid returns the OS pid of an out-of-process provider's plugin process (0 for
// an in-proc/builtin provider, which has none). The idle guard polls it for CPU progress.
func providerPid(prov Provider) int {
	if gp, ok := prov.(*grpcProvider); ok {
		return gp.pid
	}
	return 0
}

// pluginInvokeNoProgress resolves the no-progress window: the test override, else the
// project readiness `no_progress`. That field is ALWAYS populated — poll.ResolveReadiness
// fills it from readinessNoProgressFallback and ValidateOrdering rejects a non-positive
// set — so there is no second literal here (R3); loadedReadiness() is sync.Once-cached
// and re-entrancy-guarded (built-in defaults mid-load), so this is safe anywhere.
func pluginInvokeNoProgress() time.Duration {
	if pluginInvokeNoProgressOverride > 0 {
		return pluginInvokeNoProgressOverride
	}
	return loadedReadiness().NoProgress
}

// idleBoundedContext returns a context cancelled with errPluginCallIdle when the
// call makes no progress for noProgress, plus a stop func. Progress is EITHER a host
// reverse leg (a.touch) OR the peer plugin process's CPU advancing (a.cpu) — the
// second covers work the plugin does entirely locally (virsh/ssh children). NO
// wall-clock cap (see the file header). Applied only when the caller supplied no
// deadline of its own.
//
// EVENT-DRIVEN, not sampled: the watchdog arms a deadline of `noProgress` and RESETS
// it whenever a progress event arrives on a.wake (a host-leg touch) or the peer's CPU
// advances. Because a progress event that arrives before the deadline always resets
// it, there is NO race between the producer's touch cadence and this watchdog's check
// cadence — the failure a fixed sample interval cannot avoid (it compared a timestamp
// against the full window at its own tick, so equal cadences under load false-tripped).
// CPU is still polled (it has no event source), but a CPU advance also resets the
// deadline, and the poll interval is a small fraction of the window so a busy peer is
// never mistaken for idle.
func idleBoundedContext(ctx context.Context, noProgress time.Duration, a *pluginActivity) (context.Context, context.CancelFunc) {
	a.touch() // the dispatch itself is baseline activity
	cctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(noProgress)
		defer timer.Stop()
		// CPU poll cadence — a fraction of the window; the CPU signal has no event
		// source, so it must be sampled, unlike the host-leg touch.
		poll := noProgress / 4
		if poll < 10*time.Millisecond {
			poll = 10 * time.Millisecond
		}
		cpu := time.NewTicker(poll)
		defer cpu.Stop()
		lastCPU := a.cpu()
		reset := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(noProgress)
		}
		for {
			select {
			case <-stop:
				return
			case <-cctx.Done():
				return
			case <-a.wake:
				// A host-leg touch is a progress EVENT: reset the deadline.
				reset()
			case <-cpu.C:
				// CPU advancing is progress: refresh the clock + high-water and reset
				// the deadline, so a plugin busy on its own children never trips.
				if cur := a.cpu(); cur > lastCPU {
					lastCPU = cur
					a.touch()
					reset()
				}
			case <-timer.C:
				cancel(errPluginCallIdle)
				return
			}
		}
	}()
	return cctx, func() { close(stop); cancel(nil) }
}

// procCPUTicks reads a process's monotonic CPU ticks from /proc/<pid>/stat: utime +
// stime + cutime + cstime (fields 14-17). cutime/cstime aggregate REAPED descendants'
// CPU, which is exactly the signal for a plugin whose work is its own retry children.
// 0 on any error (process gone, non-Linux, malformed) — the caller only compares for
// ADVANCE, so a transient 0 can never look like progress.
func procCPUTicks(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The comm field is parenthesized and may contain spaces: parse from the LAST ')'.
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 || i+2 >= len(b) {
		return 0
	}
	fields := strings.Fields(string(b[i+2:]))
	// After comm, field 1 is state; utime is field 14 overall => index 11 here
	// (fields[0]=state=field3). utime,stime,cutime,cstime = indices 11,12,13,14.
	if len(fields) < 15 {
		return 0
	}
	var sum uint64
	for _, idx := range []int{11, 12, 13, 14} {
		v, err := strconv.ParseUint(fields[idx], 10, 64)
		if err != nil {
			return 0
		}
		sum += v
	}
	return sum
}

// withActivityHeartbeat runs fn while heartbeating the clock every 5s, so a host reverse
// leg that itself runs longer than the no-progress window (a RunSystem/RunHostStep doing
// a multi-minute build, say) is seen as progressing for its whole duration. The plugin is
// blocked in the RPC and its own CPU is frozen then, so the LEG'S duration is the signal.
func withActivityHeartbeat[T any](a *pluginActivity, fn func() (T, error)) (T, error) {
	stop := startPluginActivityHeartbeat(a)
	defer stop()
	return fn()
}

// withActivityHeartbeatErr is withActivityHeartbeat for an error-only operation.
func withActivityHeartbeatErr(a *pluginActivity, fn func() error) error {
	stop := startPluginActivityHeartbeat(a)
	defer stop()
	return fn()
}

// startPluginActivityHeartbeat touches the clock every beat until stopped, where beat
// is DERIVED from the no-progress window (never a fixed 5s): a host leg / host child
// that runs longer than the window must be heartbeated WITHIN it, else the guard would
// still kill it. host_build_cli and the host reverse legs wrap their blocking work with
// this so the work being ALIVE is forward progress for its whole (possibly ~13min)
// duration — the signal that distinguishes a progressing operation from a wedged plugin.
func startPluginActivityHeartbeat(a *pluginActivity) func() {
	if a == nil {
		return func() {}
	}
	interval := pluginInvokeNoProgress() / 4
	if interval <= 0 || interval > 5*time.Second {
		interval = 5 * time.Second // cap the beat; still window-derived when the window is small
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.touch()
			}
		}
	}()
	return func() { close(stop) }
}

// pluginInvokeErr maps the idle watchdog's cancellation onto the sentinel so a hung
// plugin reports WHY, not a bare "context canceled".
func pluginInvokeErr(ctx context.Context, what string, err error) error {
	if errors.Is(context.Cause(ctx), errPluginCallIdle) {
		return fmt.Errorf("%s: %w", what, errPluginCallIdle)
	}
	return err
}
