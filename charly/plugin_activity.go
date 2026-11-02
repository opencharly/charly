package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// A SECOND source covers work a plugin does entirely on its own side, with no host leg
// at all: the watched process tree advancing in CPU OR I/O (see pluginActivity below).
// For a COMPILED-IN peer the tree is the HOST process's DESCENDANTS — the children the
// in-process build/deploy code spawns (buildctl, the engine's load, …) — because the
// peer has no pid of its own and its real work is those children, not the host root.
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
// distinguish a real hang from an ordinary timeout/transport error. Its text states only
// what the watchdog MEASURED — the three things it can see are host reverse legs, the
// peer tree's CPU, and the passage of time — and every kill wraps an evidence report
// (pluginActivity.stallReport) around it, which reaches the caller only because
// pluginInvokeErr propagates the cause instead of the bare sentinel. So a tripped bed names
// the silent source instead of asserting a cause the watchdog cannot prove. The former text
// ("hung plugin — the peer runs no host work") was exactly that unsupported claim, and it
// sent a week of investigation after the wrong mechanism (#699).
var errPluginCallIdle = errors.New("plugin call made no host-visible progress within the no-progress window (no host reverse leg and no CPU or I/O advance in the watched process tree)")

// Two INDEPENDENT test seams (never one var driving both bounds, which would let a
// leaf-cap test silently shrink the idle window): pluginInvokeNoProgressOverride for the
// idle/no-progress window, pluginLeafCapOverride for the host→plugin leaf total cap.
// A test seam only; production leaves both 0. Read before the watchdog starts.
var (
	pluginInvokeNoProgressOverride time.Duration
	pluginLeafCapOverride          time.Duration
)

// pluginActivity is one call's forward-progress clock. A nil *pluginActivity is safe:
// touch is a no-op and the clock is never watched, so a server without a clock never trips.
//
// TWO activity sources feed it:
//
//   - touch() from the host reverse legs (host-visible work), and
//
//   - work() polling the WATCHED PROCESS TREE. The second is load-bearing: a plugin can do
//     all its work PLUGIN-LOCALLY (the vm deploy's console bootstrap runs `virsh send-key`
//     and its ssh readiness retries as the plugin's OWN children), which touches no host
//     leg — measured: the idle guard false-killed a legitimately-booting ISO guest at
//     prepare-venue because of it.
//
//     It sums BOTH monotonic CPU ticks and /proc/<pid>/io bytes. CPU alone was a
//     false-positive class for a build: BuildKit's work lives in `buildkitd`, and the
//     client-side export/engine-load phases are I/O-bound, so the tree can move a large
//     archive while accruing almost no CPU — measured: a cold build phase was killed as
//     idle during OCI export, and again during `podman load`, at the default 90s window.
//     Bytes read/written are evidence the process is MOVING DATA, not a sleep-based beat.
//
//     The tree ROOT depends on placement: an out-of-process peer's own process (and its
//     descendants), or — for a COMPILED-IN peer, which has no pid of its own — the HOST
//     process's DESCENDANTS plus the charly-owned DETACHED daemon trees. The host root is
//     deliberately EXCLUDED: the charly process always burns some CPU (runtime, GC, other
//     goroutines), so counting it would mask a wedged in-process peer forever. Its
//     descendants are attributable: the in-process build code spawns `buildctl` and the
//     engine's load child, and those carry the work. The daemons matter because
//     buildkitdrive detaches `buildkitd` (rootlesskit RE-EXECs, reparented to init), so a
//     quiet BuildKit RUN is invisible to the descendant tree while the daemon installs
//     packages; the client can emit no line for longer than the window.
//
//     The reader used to sum the peer process's OWN counters alone, and that was a
//     false-positive class by construction (#699): /proc/<pid>/stat's
//     utime+stime+cutime+cstime folds in a descendant's CPU only AFTER that descendant
//     has been REAPED, so a peer parked in wait() on a live CPU-active child reads as
//     frozen for the child's entire life — the shape of a deploy plugin while its
//     overlay/engine children run. It now sums the whole live tree (procTree), which is
//     monotone for CPU: a reaped descendant's ticks move into its parent's cutime, itself
//     summed. (I/O bytes have no reap accounting, so the sum can DIP when a descendant
//     exits; a dip never counts as advance, so it cannot manufacture progress.)
type pluginActivity struct {
	// pid is the tree root: the peer plugin process whose process tree's CPU/I/O counts as
	// progress; 0 = no polling (an unplumbed pid). For a compiled-in peer this is the HOST
	// pid and excludeRoot is true (see newCompiledInPluginActivity).
	pid int
	// excludeRoot omits the root from the summed work. Set for a COMPILED-IN peer, whose
	// "peer pid" is the host: the host's own CPU is always ticking (runtime/GC), so
	// including it would make a wedged in-process call unkillable. The host's DESCENDANTS
	// (buildctl, the engine load child) are the attributable signal.
	excludeRoot bool
	// followCharlyDaemons adds the CHARLY-OWNED DETACHED daemon trees to the sum. A
	// compiled-in peer's heavy phase (a BuildKit RUN/export) is executed by `buildkitd`,
	// which `buildkitdrive.Ensure` detaches: rootlesskit RE-EXECs itself and the tree is
	// reparented to init, so it is NOT a descendant of this charly process. The client
	// `buildctl` can be silent for longer than the window while the daemon installs
	// packages or exports layers — measured: the default 90s window killed the final
	// `pod-crabbox` RUN after the client's last line. The daemon's tree is the only
	// progress evidence then. See charlyDaemonPids for the (kind-agnostic) discovery.
	followCharlyDaemons bool
	// wake is the PROGRESS EVENT channel: touch() signals it non-blockingly and the
	// idle watchdog RESETS its deadline on each signal. This is what makes the guard
	// event-driven rather than sample-driven on the EVENT path — a progress event
	// arriving before the deadline resets it, so the producer's touch cadence needs no
	// relationship to the watchdog's check cadence (the sampled form compared a stale
	// timestamp against the full window and false-tripped whenever the two cadences
	// were equal under load). Buffered size 1: coalesces bursts to a single pending
	// reset — which is also why the EXPIRY path must drain it (observe) rather than
	// trust that a pending event always got consumed: a coalesced burst can be DROPPED,
	// and a select picks at random among READY cases.
	wake chan struct{}
	// Source accounting for the KILL REPORT only — never for the decision. How many
	// resets each source contributed over the call's life, and when the last one
	// arrived, so a kill can say which source went quiet rather than guess (see
	// stallReport). Atomic because the watchdog writes them while a reporter reads.
	legs     atomic.Int64 // host reverse-leg events consumed
	works    atomic.Int64 // CPU/I/O-advance resets
	lastLeg  atomic.Int64 // unix nanos of the last host-leg event, 0 = never
	lastWork atomic.Int64 // unix nanos of the last tree work advance, 0 = never
}

// newPluginActivity builds a call's activity clock with its wake channel initialized.
// The zero value remains safe for a nil clock (touch is a no-op), but a clock actually
// watched by idleBoundedContext must be built here so touch() can signal the event.
func newPluginActivity(pid int) *pluginActivity {
	return &pluginActivity{pid: pid, wake: make(chan struct{}, 1)}
}

// newCompiledInPluginActivity builds the clock for a COMPILED-IN peer: the peer has no pid
// of its own, so the attributable work is the HOST process's descendants (buildctl, the
// engine's load child, …). The host root is excluded — see pluginActivity.excludeRoot.
func newCompiledInPluginActivity() *pluginActivity {
	a := newPluginActivity(os.Getpid())
	a.excludeRoot = true
	a.followCharlyDaemons = true
	return a
}

func (a *pluginActivity) touch() {
	if a == nil {
		return
	}
	// Signal the progress EVENT (non-blocking; a full buffer already holds a pending
	// reset, which is equivalent to this one).
	if a.wake != nil {
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
}

// work reads the watched trees' monotonic progress: CPU ticks and /proc/<pid>/io bytes
// summed over the root tree (or over its DESCENDANTS when excludeRoot is set — a
// compiled-in peer's host root is not attributable), plus the charly-owned detached
// daemon trees when followCharlyDaemons is set. CPU covers local compute and the #699
// child case; I/O covers a build's export/load phase, where bytes move but CPU barely
// does; the daemon tree covers a quiet BuildKit RUN that the client cannot see.
// Callers only ever compare it for ADVANCE, never as an absolute.
func (a *pluginActivity) work() procWork {
	roots := a.watchedRoots()
	if len(roots) == 0 {
		return procWork{}
	}
	procs := procTrees(roots)
	if procs == nil {
		// /proc itself is unreadable (non-Linux). For an out-of-process peer, degrade to
		// the root-only reader rather than reporting a confident 0. For a compiled-in peer
		// the host root is NOT a substitute for its descendants/daemons — returning it would
		// be exactly the always-ticking masking excludeRoot avoids — so the tree source is
		// simply absent (host legs still guard the call).
		if a.excludeRoot {
			return procWork{}
		}
		if t := procCPUTicks(a.pid); t > 0 {
			return procWork{cpuTicks: t}
		}
		return procWork{}
	}
	w, _ := treeWork(procs, a.excludePID())
	return w
}

// watchedRoots is the exact root set the clock sums: the peer/host root, plus — for a
// compiled-in peer — every discovered charly-owned detached daemon. stallReport renders
// this same set, so the report cannot describe a different tree than the decision used.
func (a *pluginActivity) watchedRoots() []int {
	if a == nil || a.pid <= 0 {
		return nil
	}
	roots := []int{a.pid}
	if a.followCharlyDaemons {
		roots = append(roots, charlyDaemonPidsFn()...)
	}
	return roots
}

// excludePID is the ONE pid omitted from the sum (0 = omit none): the host root for a
// compiled-in peer. Daemon roots and descendants are never excluded.
func (a *pluginActivity) excludePID() int {
	if a == nil || !a.excludeRoot {
		return 0
	}
	return a.pid
}

// stallReport is the kill report: the evidence the watchdog actually holds at the moment
// it cancels, carried on the cause errPluginCallIdle is wrapped in (and therefore reaching
// a caller only through pluginInvokeErr, which propagates the CAUSE and not the bare
// sentinel). It exists because the sentinel's text alone is a CLAIM about the peer that a
// no-progress watchdog cannot support — the things it observes are host reverse legs,
// CPU/I/O in the watched process trees (including the charly-owned detached daemons), and
// elapsed time, and a starved host or a descendant that double-forked (reparented to init,
// so no longer attributed to the peer) makes "the peer runs no host work" false while the
// plugin is working. This is measurement, not
// diagnosis: it prints the source counts and the tree it can see, and names each member it
// saw, so the reader can tell the guard's OWN covered case (a sleeping parent waiting on a
// running child, both in the tree) from a reparented worker (absent) or an uninterruptible
// I/O state the guard cannot help. #699 needed exactly this and had nothing but the old
// assertion.
func (a *pluginActivity) stallReport(noProgress time.Duration) string {
	roots := a.watchedRoots()
	procs := procTrees(roots)
	w, live := treeWork(procs, a.excludePID())
	// Three different reasons for "no attributable tree", three different sentences: a clock
	// with no peer pid watches none, a compiled-in peer watches the host's DESCENDANTS (the
	// host root itself is not attributable), and a walkable tree can simply be empty. Collapsing
	// them would be the same class of unsupported claim the report exists to replace.
	members := treeMembers(procs)
	rootNote, treeNote := "", "peer tree"
	switch {
	case a.pid <= 0:
		members = " (no peer pid — this clock watches no process tree)"
	case a.excludeRoot:
		rootNote = fmt.Sprintf(" (compiled-in peer: the HOST process %d; its own work is not attributable, so only its descendants and charly-owned daemon trees are summed)", a.pid)
		treeNote = "watched tree"
		if len(roots) > 1 {
			rootNote += fmt.Sprintf("; followed daemon root(s)=%v", roots[1:])
		}
	}
	return fmt.Sprintf("peer pid=%d%s; over the call: host reverse legs=%d (last %s), work advances=%d (last %s; CPU ticks=%d, I/O bytes=%d); %s at kill: %d live process(es) summing %d CPU ticks and %d I/O bytes (root-only reader %d ticks)%s; window=%s. A starved host, a double-forked descendant and a genuinely wedged peer all read this way — if the tree shows live processes, re-run on an idle host before reading it as a hang",
		a.pid, rootNote,
		a.legs.Load(), agoAt(a.lastLeg.Load()),
		a.works.Load(), agoAt(a.lastWork.Load()),
		w.cpuTicks, w.ioBytes,
		treeNote, live, w.cpuTicks, w.ioBytes, procCPUTicks(a.pid), members, noProgress)
}

// treeMemberMax caps how many tree members a kill report names, so a large tree cannot
// flood the error the CLI prints.
const treeMemberMax = 6

// treeMembers renders the WALKED tree for the kill report: pid(state,comm) per process,
// capped at treeMemberMax. It takes the walk procTreeProcs already made, so the report
// describes the very tree the guard measured and can never drift from it. nil (tree not
// walkable at all) and empty (walkable, but the peer is gone) are different facts and are
// rendered differently — the first says so, the second says nothing.
func treeMembers(procs []procInfo) string {
	if procs == nil {
		return " (tree unreadable)"
	}
	var b strings.Builder
	for i, p := range procs {
		if i == treeMemberMax {
			fmt.Fprintf(&b, ", +%d more", len(procs)-treeMemberMax)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d(%s,%s)", p.pid, p.state, p.comm)
	}
	if b.Len() == 0 {
		return ""
	}
	return ": " + b.String()
}

// agoAt renders a unix-nanos stamp as an age, or "never" when nothing was recorded.
func agoAt(nanos int64) string {
	if nanos <= 0 {
		return "never"
	}
	return time.Since(time.Unix(0, nanos)).Truncate(time.Millisecond).String() + " ago"
}

// observe is the ONE progress measurement the watchdog makes, and the reason a kill
// cannot happen on a single unconfirmed expiry. It reports whether EITHER source shows
// progress right now, draining a pending host-leg event if one is waiting, and returns
// the (possibly advanced) tree-work high-water.
//
// It must be able to see a leg event that the deadline's expiry raced: touch() is a
// NON-BLOCKING depth-1 send, so a burst of touches coalesces to one token that can be
// DROPPED when the buffer is already full, and Go's select picks at RANDOM among READY
// cases — so `case <-timer.C` can win while a token sits unread in the buffer. Draining
// here is what makes the kill decision sound. (The doc above the wake field claims a
// progress event "always resets" the deadline and that there is therefore no race; that
// is true of the EVENT path and false of the EXPIRY path, which is why this exists.)
//
// Passing wake == nil is legal — a nil channel never fires in a select, so the drain is
// skipped and only the tree-work source is measured — but the watcher never does: BOTH
// of its calls pass the LIVE channel, so a touch that raced either tick is drained and
// counted rather than left pending.
func (a *pluginActivity) observe(wake <-chan struct{}, last procWork) (procWork, bool) {
	select {
	case <-wake:
		a.legs.Add(1)
		a.lastLeg.Store(time.Now().UnixNano())
		return last, true
	default:
	}
	if cur := a.work(); cur.advancedFrom(last) {
		a.works.Add(1)
		a.lastWork.Store(time.Now().UnixNano())
		return cur, true
	}
	return last, false
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
//
// RAISED to 2h. The former 10m was the hung-plugin guard from #474, and as a TOTAL
// bound it guillotined the work that most needs a leaf: a COLD image build. Measured
// on a fresh base digest, `box build openwebui` (multi-hundred-MB base pull + full
// package install + a pixi/pip app layer) died at exactly 10m with `signal: killed`
// on buildctl and `context canceled` inside the mount callback — twice, before the
// cap was found by reading this constant. The same build completes well inside an
// hour with the cap raised.
//
// Why 2h rather than 1h: the longest legitimate leaf is a cold multi-stage image build
// or a full assembled bed with its rebuild legs, both of which can pass an hour on a
// slow venue or a cold cache. Why finite at all: a leaf has no progress signal, so a
// wedged plugin must still fail rather than wedge the host forever — an hour-scale
// bound preserves the #474 guarantee while a 10m bound broke the normal case. The
// plugin→plugin path keeps the precise IDLE guard (pluginInvokeNoProgress), because it
// does have a progress signal. Operators on unusual venues still override with
// CHARLY_PLUGIN_LEAF_CAP, and the expiry now NAMES the bound and the knob.
const pluginLeafCapDefault = 2 * time.Hour

// pluginLeafCap resolves the leaf bound: the test override, else pluginLeafCapDefault.
func pluginLeafCap() time.Duration {
	if pluginLeafCapOverride > 0 {
		return pluginLeafCapOverride
	}
	return pluginLeafCapDefault
}

// newProviderActivity builds the progress clock for one peer provider dispatch. An
// out-of-process provider watches its own pid (and descendants). A COMPILED-IN provider
// has no pid: it runs in this process, and its real work is the host children it spawns
// (`buildctl`, the engine's load, …), so it watches the HOST's descendants with the root
// excluded (newCompiledInPluginActivity).
func newProviderActivity(prov Provider) *pluginActivity {
	if gp, ok := prov.(*grpcProvider); ok {
		return newPluginActivity(gp.pid)
	}
	return newCompiledInPluginActivity()
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

// pluginInvokeNoProgressFor returns the idle window for one dispatch class. The
// STEP providers run installs (pacman/apt/buildah/system-packages) that go minutes
// between host-visible legs — the same sparseness the build steps have — so they
// get the heavy per-attempt window instead of the probe no_progress. Measured: the
// CachyOS VM bed's deploy tripped the 90s probe window on step:system-packages after
// a single reverse leg while pacman was upgrading packages (no cpu advance from the
// peer tree, no host leg). A genuine hang still trips at the heavy window.
func pluginInvokeNoProgressFor(class ProviderClass) time.Duration {
	if class == ClassStep {
		return loadedReadiness().PerAttemptHeavy
	}
	return pluginInvokeNoProgress()
}

// idleBoundedContext returns a context cancelled with errPluginCallIdle when the
// call makes no progress for noProgress, plus a stop func. Progress is EITHER a host
// reverse leg (a.touch) OR CPU/I/O advancing in the watched LIVE process trees
// (a.work → procTrees): the peer's own tree for an out-of-process plugin, the HOST's
// descendants plus the charly-owned detached daemon trees for a compiled-in one. The
// second covers work the plugin does entirely locally — the virsh/ssh children it forks
// and waits on, the `buildctl`/engine-load children the compiled-in build spawns, or the
// detached `buildkitd` installing packages on its behalf. NO wall-clock cap (see the file
// header). Applied only when the caller supplied no deadline of its own.
//
// EVENT-DRIVEN for the host leg, SAMPLED for the tree. The watchdog arms a deadline of
// `noProgress` and RESETS it whenever a host-leg event arrives on a.wake or the watched
// tree's CPU/I/O has advanced. A host-leg event that arrives before the deadline does reset
// it — but the producer's touch is a NON-BLOCKING depth-1 send whose token can be
// DROPPED, and Go's select picks at RANDOM among ready cases, so an expiry can win
// while an unread event sits in the buffer. The deadline expiring is therefore NOT by
// itself proof of idleness: every expiry re-measures BOTH sources through observe and
// cancels only when neither has anything. That confirmation is what a bare timer lacks
// — one that compared a timestamp against the full window at its own tick false-tripped
// on equal cadences under load, which is the bug this rewrite fixes.
func idleBoundedContext(ctx context.Context, noProgress time.Duration, a *pluginActivity) (context.Context, context.CancelFunc) {
	a.touch() // the dispatch itself is baseline activity
	// Guarantee a live PROGRESS EVENT channel before the watcher starts: a clock not
	// built by newPluginActivity would otherwise have a nil wake, and `case <-a.wake`
	// would then never fire — silently reverting the guard to a bare timer that
	// false-kills a progressing call (the exact bug this rewrite fixes). The clock is
	// armed here BEFORE the call is dispatched, so no concurrent touch races this
	// assignment (touches only happen on reverse legs during the in-flight call).
	if a.wake == nil {
		a.wake = make(chan struct{}, 1)
	}
	wake := a.wake
	cctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	// done lets the returned stop function JOIN the watcher. Without the join, a caller
	// that restores a package seam (the tests do) can race the watcher's next sample —
	// and, more importantly, a returned "stopped" clock could still be reading /proc.
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(noProgress)
		defer timer.Stop()
		// Tree poll cadence — a fraction of the window; the CPU/I/O signal has no event
		// source, so it must be sampled, unlike the host-leg touch.
		poll := noProgress / 4
		if poll < 10*time.Millisecond {
			poll = 10 * time.Millisecond
		}
		workPoll := time.NewTicker(poll)
		defer workPoll.Stop()
		last := a.work()
		// reset re-arms the deadline for the full window. It runs on ALL THREE progress
		// paths — including the EXPIRY path itself, where progress was just confirmed —
		// so it must never block. resetDeadline exists to make that provable: the drain
		// is a select-with-default rather than a bare receive, because on the expiry
		// path the value has already been consumed and a bare `<-timer.C` would block
		// forever and wedge the watcher.
		reset := func() { resetDeadline(timer, noProgress) }
		for {
			select {
			case <-stop:
				return
			case <-cctx.Done():
				return
			case <-wake:
				// A host-leg touch is a progress EVENT: reset the deadline.
				a.legs.Add(1)
				a.lastLeg.Store(time.Now().UnixNano())
				reset()
			case <-workPoll.C:
				// CPU/I/O advancing is progress: refresh the clock + high-water and reset
				// the deadline, so a plugin busy on its own children never trips. The
				// LIVE wake channel is passed, never nil: a nil channel never fires in
				// a select, so passing it would make observe's drain a no-op and leave
				// a host-leg touch this tick raced pending for the expiry to trip over.
				if cur, ok := a.observe(wake, last); ok {
					last = cur
					reset()
				}
			case <-timer.C:
				// CONFIRM before killing, do not kill on a single expiry. Two reasons
				// the first expiry is not trustworthy: a burst of host-leg touches
				// coalesces to one depth-1 token that can be DROPPED, and Go's select
				// chooses at random among READY cases, so an expiry can win while a
				// progress event sits unread in the buffer; and the tree-work source is
				// only SAMPLED, so its last observation can predate progress by up to a
				// poll interval. Re-measure BOTH sources once; cancel only when neither
				// has anything.
				if cur, ok := a.observe(wake, last); ok {
					last = cur
					reset()
					continue
				}
				cancel(fmt.Errorf("%w: %s", errPluginCallIdle, a.stallReport(noProgress)))
				return
			}
		}
	}()
	return cctx, func() { close(stop); <-done; cancel(nil) }
}

// resetDeadline re-arms timer for the full window d, discarding a stale expiry if one is
// already pending, so the call can NEVER block.
//
// That non-blocking property is load-bearing on the EXPIRY path: the watcher calls reset
// from `case <-timer.C` when it confirms progress at expiry, and there the value has
// already been consumed, so Stop reports false and a bare `<-timer.C` drain would block
// forever and wedge the watchdog (a wedged watcher never kills and never returns — the
// worst possible failure for a guard). The drain is therefore a select-with-default, which
// returns immediately whether or not a value is pending. Both states are pinned by
// TestResetDeadline_*; it is a package-level func rather than a closure so a test can put
// the timer in exactly the state each path produces.
func resetDeadline(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

// procInfo is one /proc/<pid>/stat sample: the process's parent, its CPU ticks
// (utime + stime + cutime + cstime, fields 14-17), its state letter and its comm name. It
// is both what the reader returns and what the kill report renders, so the two cannot
// describe different things. ioBytes is filled only for TREE MEMBERS (by procTreeProcs,
// after the walk has chosen them), so a walk over all of /proc does not open an io file
// for every process on the host.
type procInfo struct {
	pid     int
	ppid    int
	ticks   uint64
	ioBytes uint64 // /proc/<pid>/io rchar+wchar, for tree members only
	state   string // /proc state letter: R running, S sleeping, D uninterruptible I/O, Z zombie, …
	comm    string // the executable name, from the parenthesised comm field
	zombie  bool
}

// readProcStat parses /proc/<pid>/stat ONCE. ok=false on any error (process gone,
// non-Linux, malformed) — callers only ever compare ticks for ADVANCE, so a transient
// failure can never look like progress.
func readProcStat(pid int) (procInfo, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, false
	}
	s := string(b)
	// The comm field is parenthesized and may itself contain spaces AND parens, so the
	// fields that can be trusted are the ones after the LAST ')'; the name is what sits
	// between the FIRST '(' and that last ')'.
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open || end+2 >= len(s) {
		return procInfo{}, false
	}
	st := procInfo{pid: pid, comm: s[open+1 : end]}
	fields := strings.Fields(s[end+2:])
	// After comm, field 1 is state; utime is field 14 overall => index 11 here
	// (fields[0]=state=field3). utime,stime,cutime,cstime = indices 11,12,13,14.
	if len(fields) < 15 {
		return procInfo{}, false
	}
	pp, err := strconv.Atoi(fields[1])
	if err != nil {
		return procInfo{}, false
	}
	st.ppid, st.state, st.zombie = pp, fields[0], fields[0] == "Z"
	for _, idx := range []int{11, 12, 13, 14} {
		v, err := strconv.ParseUint(fields[idx], 10, 64)
		if err != nil {
			return procInfo{}, false
		}
		st.ticks += v
	}
	return st, true
}

// procCPUTicks reads ONE process's monotonic CPU ticks from /proc/<pid>/stat: utime +
// stime + cutime + cstime. cutime/cstime aggregate REAPED descendants' CPU, which is
// exactly the signal for a plugin whose work is its own retry children. 0 on any error
// (process gone, non-Linux, malformed) — the caller only compares it for ADVANCE, so a
// transient 0 can never look like progress.
func procCPUTicks(pid int) uint64 {
	if st, ok := readProcStat(pid); ok {
		return st.ticks
	}
	return 0
}

// procTrees walks /proc ONCE and returns the UNION of the watched trees: for each root,
// the root first and then its descendants DEPTH-FIRST pre-order (a LIFO stack, so the
// child pushed last is visited next). The order is presentation only (treeWork sums,
// treeMembers renders); what the walk guarantees is that within each root every process
// PRECEDES its own descendants, so the report reads as a tree. It is the ONE walker:
// work sums what it returns, and the kill report renders it, so the report can never
// describe a different tree than the one the guard measured. Duplicate pids across roots
// (a daemon that happens to also be a host descendant) are emitted once.
//
// A ZOMBIE is included (its ticks still count toward the sum) and flagged, because skipping
// it outright would make the sum DROP the instant a child exits and rise again when the
// parent reaps it — a dip is not a false kill, but it would hide a real advance and so could
// manufacture one. Summing until the reap keeps the sum non-decreasing. A descendant that
// double-forks is reparented to init and is NOT attributable to the peer; it is absent from
// the walk, which is one of the things the kill report exists to reveal.
//
// nil means /proc could not be walked at all (non-Linux). An EMPTY, non-nil slice means
// /proc was read fine but none of the roots is live — a walkable tree with nothing left
// in it. The distinction matters to the caller's fallback and to the report's wording.
func procTrees(roots []int) []procInfo {
	if len(roots) == 0 {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	nodes := make(map[int]procInfo, len(entries))
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n <= 0 {
			continue
		}
		if st, ok := readProcStat(n); ok {
			nodes[n] = st
		}
	}
	children := make(map[int][]int, len(nodes))
	for n, nd := range nodes {
		children[nd.ppid] = append(children[nd.ppid], n)
	}
	// The ppid links form a forest, so this walk terminates on every input. It is a LIFO
	// STACK (pop and push at the same end), i.e. depth-first pre-order: each node is
	// emitted before the children it then pushes, so a parent always precedes its own
	// descendants in the report. A FIFO queue would make it breadth-first instead;
	// nothing depends on which, so the order is documented rather than implied.
	out := make([]procInfo, 0, len(nodes))
	seen := make(map[int]bool, len(nodes))
	for _, root := range roots {
		if root <= 0 {
			continue
		}
		if st, ok := nodes[root]; !ok || st.zombie {
			continue
		}
		stack := []int{root}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, nodes[n])
			stack = append(stack, children[n]...)
		}
	}
	// Read /proc/<pid>/io for the TREE MEMBERS only, not for every process the walk
	// visited on its way to them: io is a second file open per sample, and the members are
	// the only processes whose bytes are summed or reported.
	for i := range out {
		out[i].ioBytes = readProcIO(out[i].pid)
	}
	return out
}

// procTreeProcs is the ONE-ROOT form, kept for the tests and the walk-order contract.
// pid <= 0 is "no root" (nil, distinct from a walkable-but-empty tree).
func procTreeProcs(pid int) []procInfo {
	if pid <= 0 {
		return nil
	}
	return procTrees([]int{pid})
}

// procWork is one sample of the watched tree's progress: CPU ticks plus I/O bytes. CPU is
// monotone across a reap (cutime/cstime); I/O bytes can dip when a descendant exits, so
// advancedFrom treats a dip as NO advance and only a rise as progress — a dip can never
// manufacture a reset.
type procWork struct {
	cpuTicks uint64
	ioBytes  uint64
}

func (w procWork) advancedFrom(prev procWork) bool {
	return w.cpuTicks > prev.cpuTicks || w.ioBytes > prev.ioBytes
}

// readProcIO returns a process's cumulative I/O bytes: rchar + wchar from
// /proc/<pid>/io, the bytes it has read and written through syscalls (sockets and pipes
// included). That is the signal for a build phase whose work is data movement rather than
// CPU — buildctl streaming an OCI export and the engine's load reading it. 0 on any error
// (a foreign uid, a process already gone, a non-Linux /proc): the caller only compares it
// for ADVANCE, so an unreadable sample can never look like progress.
func readProcIO(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/io")
	if err != nil {
		return 0
	}
	var total uint64
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if key != "rchar" && key != "wchar" {
			continue
		}
		if v, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
			total += v
		}
	}
	return total
}

// treeWork sums a walked union: CPU ticks and I/O bytes for every process except
// excludePID (0 = exclude none), plus how many are not yet reaped. The ONE definition of
// the sum, so work and the kill report cannot drift apart.
func treeWork(procs []procInfo, excludePID int) (procWork, int) {
	var w procWork
	live := 0
	for _, p := range procs {
		if excludePID > 0 && p.pid == excludePID {
			continue
		}
		if !p.zombie {
			live++
		}
		w.cpuTicks += p.ticks
		w.ioBytes += p.ioBytes
	}
	return w, live
}

// charlyDaemonPids discovers the CHARLY-OWNED DETACHED daemons: live processes whose argv
// names a path under this user's charly cache or runtime directory. The compiled-in build's
// heavy phase runs in `buildkitd`, which buildkitdrive detaches (rootlesskit RE-EXECs and
// the tree is reparented to init), so the daemon is NOT in this charly process's descendant
// tree; its state root and socket ARE under those two directories. The discovery is
// deliberately kind-agnostic: it knows only where charly puts its daemons, never a daemon's
// name, so a new detached charly daemon is covered without a core kind word.
//
// A daemon is SHARED by every charly call in this user's store. Summing it can therefore
// mask a wedged call while another call keeps the daemon busy; that is the correct trade
// here — a false kill destroys real work, and the daemon's work is real work — and the
// genuine-hang arm (no descendant, no daemon) still trips.
func charlyDaemonPids() []int {
	var roots []string
	if cache, err := os.UserCacheDir(); err == nil {
		roots = append(roots, filepath.Join(cache, "charly"))
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		roots = append(roots, filepath.Join(rt, "charly"))
	}
	if len(roots) == 0 {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(b), "\x00", " ")
		for _, root := range roots {
			if strings.Contains(cmd, root) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// charlyDaemonPidsFn is the discovery seam: production uses charlyDaemonPids, while a test
// that must isolate the clock (a genuine-hang arm on a developer host that happens to be
// running a build) can substitute a fixed root set. A test seam only.
var charlyDaemonPidsFn = charlyDaemonPids

// withActivityHeartbeat runs fn while heartbeating the clock at the WINDOW-DERIVED beat
// startPluginActivityHeartbeat computes (window/4, capped at 5s — never a fixed 5s), so a
// host reverse leg that itself runs longer than the no-progress window (a
// RunSystem/RunHostStep doing a multi-minute build, say) is seen as progressing for its
// whole duration. The plugin is blocked in the RPC and its own CPU is frozen then, so the
// LEG'S duration is the signal.
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

// pluginInvokeErr maps the idle watchdog's cancellation onto the REASON it cancelled, so a
// hung plugin reports WHY, not a bare "context canceled": the sentinel, and the evidence
// report the watchdog wrapped in it (pluginActivity.stallReport).
//
// Propagating the CAUSE — not re-wrapping the bare sentinel — is load-bearing. The watchdog
// cancels with fmt.Errorf("%w: %s", errPluginCallIdle, stallReport), so the report travels on
// the cause; wrapping errPluginCallIdle again here would type-check identically, satisfy
// errors.Is identically, and silently THROW THE MEASUREMENT AWAY, leaving a tripped bed with
// a conclusion and no evidence — which is precisely what #699 was, and what the first R10
// run of this very change reproduced (a kill message with the new sentinel text and no
// report). errors.Is(err, errPluginCallIdle) still holds for callers, because the cause
// wraps the sentinel.
func pluginInvokeErr(ctx context.Context, what string, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, errPluginCallIdle) {
		return fmt.Errorf("%s: %w", what, cause)
	}
	return err
}

// errPluginLeafCap marks a host→plugin LEAF call that hit the TOTAL-duration bound.
// Distinct from errPluginCallIdle: that one says "the peer produced no host-visible
// work", this one says "the peer may have been working the whole time, but a leaf has
// no progress signal to prove it, so the total bound is what stopped it".
var errPluginLeafCap = errors.New("plugin leaf cap exceeded (a host→plugin leaf call has no host-visible progress to guard on, so the total-duration bound is the only guard; raise it with CHARLY_PLUGIN_LEAF_CAP)")

// leafCapErr makes a leaf-cap expiry IDENTIFY ITSELF. Without this the operator sees
// only `context canceled` / `signal: killed` from inside the plugin's children — where
// the real cause (a time bound nobody mentioned) is invisible, which is exactly how a
// cold image build got misread as a crash.
func leafCapErr(ctx context.Context, capped bool, what string, err error) error {
	if !capped || !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		return err
	}
	// BOTH identities survive: the sentinel says "this was the bound", and the underlying
	// error keeps its own (the plugin's ctx.Err() is usually context.DeadlineExceeded, which
	// callers and tests already match on).
	return fmt.Errorf("%s after %s: %w: %w", what, pluginLeafCap(), errPluginLeafCap, err)
}

// init lifts the two host→plugin call bounds out of the binary so a LONG-RUNNING operation
// (an image build, whose work happens in buildkitd's process tree rather than the plugin's
// descendants) is not killed for being slow or for looking idle.
//
// Measured: `charly box build strata` (3 GB retrieval + 6.7 GB install) died every time at
// the 10-minute leaf cap with `context canceled` in buildkitd, and later at the no-progress
// window while package installation ran inside the daemon. Both bounds are per-VENUE
// operational limits, not product constants, so they are operator-settable:
//
//	CHARLY_PLUGIN_LEAF_CAP     total-duration bound for one host→plugin call (default 2h)
//	CHARLY_PLUGIN_NO_PROGRESS  no-host-visible-progress window (default: readiness config)
func applyCallBoundEnv(getenv func(string) string) {
	for _, knob := range []struct {
		env string
		dst *time.Duration
	}{
		{"CHARLY_PLUGIN_LEAF_CAP", &pluginLeafCapOverride},
		{"CHARLY_PLUGIN_NO_PROGRESS", &pluginInvokeNoProgressOverride},
	} {
		v := getenv(knob.env)
		if v == "" {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			// A malformed non-empty value is a typo'd deployment, not an absence: say so
			// and keep the default rather than silently running with a bound nobody chose.
			fmt.Fprintf(os.Stderr, "charly: ignoring %s=%q (not a duration: %v); keeping the default\n", knob.env, v, err)
			continue
		}
		*knob.dst = d
	}
}

func init() { applyCallBoundEnv(os.Getenv) }
