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
// A SECOND source covers work a plugin does entirely on its own side, with no host leg
// at all: the peer's process tree advancing in CPU (see pluginActivity below).
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
var errPluginCallIdle = errors.New("plugin call made no host-visible progress within the no-progress window (no host reverse leg and no CPU advance from the peer process or any of its live descendants)")

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
//   - cpu() polling of the PEER's whole LIVE PROCESS TREE. The second is load-bearing:
//     a plugin can do all its work PLUGIN-LOCALLY (the vm deploy's console bootstrap
//     runs `virsh send-key` and its ssh readiness retries as the plugin's OWN children),
//     which touches no host leg — measured: the idle guard false-killed a legitimately-
//     booting ISO guest at prepare-venue because of it.
//
//     cpu() used to read the peer process's OWN counters alone, and that was a
//     false-positive class by construction (#699): /proc/<pid>/stat's
//     utime+stime+cutime+cstime folds in a descendant's CPU only AFTER that descendant
//     has been REAPED, so a peer parked in wait() on a live CPU-active child reads as
//     frozen for the child's entire life — the shape of a deploy plugin while its
//     overlay/engine children run. It now sums the whole live tree (procTree), which is
//     monotone: a reaped descendant's ticks move into its parent's cutime, itself summed.
type pluginActivity struct {
	// pid is the peer plugin process whose process tree's CPU counts as progress; 0 = no
	// polling (an in-proc/builtin peer on the host legs only, or an unplumbed pid).
	pid int
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
	legs    atomic.Int64 // host reverse-leg events consumed
	cpus    atomic.Int64 // CPU-advance resets
	lastLeg atomic.Int64 // unix nanos of the last host-leg event, 0 = never
	lastCPU atomic.Int64 // unix nanos of the last CPU advance, 0 = never
}

// newPluginActivity builds a call's activity clock with its wake channel initialized.
// The zero value remains safe for a nil clock (touch is a no-op), but a clock actually
// watched by idleBoundedContext must be built here so touch() can signal the event.
func newPluginActivity(pid int) *pluginActivity {
	return &pluginActivity{pid: pid, wake: make(chan struct{}, 1)}
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

// cpu reads the peer plugin process's monotonic CPU as its WHOLE LIVE PROCESS TREE —
// the process itself plus every descendant still alive. The root-only form was a
// false-positive class by construction (#699): cutime/cstime aggregate a descendant's
// CPU only after it has been REAPED, so a peer that forks a long-lived child and blocks
// in wait() reports a FROZEN tick count for that child's entire life. That is the shape
// of a deploy plugin while its overlay/engine children run — a call doing real work,
// read as idle. Callers only ever compare it for ADVANCE, never as an absolute.
func (a *pluginActivity) cpu() uint64 {
	if a == nil || a.pid <= 0 {
		return 0
	}
	ticks, _ := procTree(a.pid)
	return ticks
}

// stallReport is the kill report: the evidence the watchdog actually holds at the moment
// it cancels, carried on the cause errPluginCallIdle is wrapped in (and therefore reaching
// a caller only through pluginInvokeErr, which propagates the CAUSE and not the bare
// sentinel). It exists because the sentinel's text alone is a CLAIM about the peer that a
// no-progress watchdog cannot support — the only three things it can observe are host
// reverse legs, the peer tree's CPU, and elapsed time, and a starved host or a descendant
// that double-forked (reparented to init, so no longer attributed to the peer) makes "the
// peer runs no host work" false while the plugin is working. This is measurement, not
// diagnosis: it prints the source counts and the tree it can see, and names each member it
// saw, so the reader can tell the guard's OWN covered case (a sleeping parent waiting on a
// running child, both in the tree) from a reparented worker (absent) or an uninterruptible
// I/O state the guard cannot help. #699 needed exactly this and had nothing but the old
// assertion.
func (a *pluginActivity) stallReport(noProgress time.Duration) string {
	procs := procTreeProcs(a.pid)
	ticks, live := treeTicks(procs)
	// Three different reasons for "no tree", three different sentences: a clock with no
	// peer pid watches none (an in-proc/builtin target), a non-Linux /proc cannot be walked,
	// and a walkable tree can simply be empty because the peer is gone. Collapsing them
	// would be the same class of unsupported claim the report exists to replace.
	members := treeMembers(procs)
	if a.pid <= 0 {
		members = " (no peer pid — this clock watches no process tree)"
	}
	return fmt.Sprintf("peer pid=%d; over the call: host reverse legs=%d (last %s), CPU advances=%d (last %s); peer tree at kill: %d live process(es) summing %d ticks (root-only reader %d)%s; window=%s. A starved host, a double-forked descendant and a genuinely wedged peer all read this way — if the tree shows live processes, re-run on an idle host before reading it as a hang",
		a.pid,
		a.legs.Load(), agoAt(a.lastLeg.Load()),
		a.cpus.Load(), agoAt(a.lastCPU.Load()),
		live, ticks, procCPUTicks(a.pid), members, noProgress)
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
// the (possibly advanced) CPU high-water.
//
// It must be able to see a leg event that the deadline's expiry raced: touch() is a
// NON-BLOCKING depth-1 send, so a burst of touches coalesces to one token that can be
// DROPPED when the buffer is already full, and Go's select picks at RANDOM among READY
// cases — so `case <-timer.C` can win while a token sits unread in the buffer. Draining
// here is what makes the kill decision sound. (The doc above the wake field claims a
// progress event "always resets" the deadline and that there is therefore no race; that
// is true of the EVENT path and false of the EXPIRY path, which is why this exists.)
//
// A nil wake channel never fires in a select, so the CPU-only callers pass nil.
func (a *pluginActivity) observe(wake <-chan struct{}, lastCPU uint64) (uint64, bool) {
	select {
	case <-wake:
		a.legs.Add(1)
		a.lastLeg.Store(time.Now().UnixNano())
		return lastCPU, true
	default:
	}
	if cur := a.cpu(); cur > lastCPU {
		a.cpus.Add(1)
		a.lastCPU.Store(time.Now().UnixNano())
		return cur, true
	}
	return lastCPU, false
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
// reverse leg (a.touch) OR CPU advancing anywhere in the peer's LIVE process tree
// (a.cpu → procTree) — the second covers work the plugin does entirely locally, in the
// virsh/ssh children it forks and waits on. NO wall-clock cap (see the file header).
// Applied only when the caller supplied no deadline of its own.
//
// EVENT-DRIVEN for the host leg, SAMPLED for CPU. The watchdog arms a deadline of
// `noProgress` and RESETS it whenever a host-leg event arrives on a.wake or the peer
// tree's CPU has advanced. A host-leg event that arrives before the deadline does reset
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
			case <-wake:
				// A host-leg touch is a progress EVENT: reset the deadline.
				a.legs.Add(1)
				a.lastLeg.Store(time.Now().UnixNano())
				reset()
			case <-cpu.C:
				// CPU advancing is progress: refresh the clock + high-water and reset
				// the deadline, so a plugin busy on its own children never trips.
				// observe drains a pending leg token too, so a touch that this tick
				// raced is counted rather than left for the expiry to trip over.
				if cur, ok := a.observe(nil, lastCPU); ok {
					lastCPU = cur
					reset()
				}
			case <-timer.C:
				// CONFIRM before killing, do not kill on a single expiry. Two reasons
				// the first expiry is not trustworthy: a burst of host-leg touches
				// coalesces to one depth-1 token that can be DROPPED, and Go's select
				// chooses at random among READY cases, so an expiry can win while a
				// progress event sits unread in the buffer; and the CPU source is only
				// SAMPLED, so its last observation can predate progress by up to a poll
				// interval. Re-measure BOTH sources once; cancel only when neither has
				// anything.
				if cur, ok := a.observe(wake, lastCPU); ok {
					lastCPU = cur
					reset()
					continue
				}
				cancel(fmt.Errorf("%w: %s", errPluginCallIdle, a.stallReport(noProgress)))
				return
			}
		}
	}()
	return cctx, func() { close(stop); cancel(nil) }
}

// procInfo is one /proc/<pid>/stat sample: the process's parent, its CPU ticks
// (utime + stime + cutime + cstime, fields 14-17), its state letter and its comm name. It
// is both what the reader returns and what the kill report renders, so the two cannot
// describe different things.
type procInfo struct {
	pid    int
	ppid   int
	ticks  uint64
	state  string // /proc state letter: R running, S sleeping, D uninterruptible I/O, Z zombie, …
	comm   string // the executable name, from the parenthesised comm field
	zombie bool
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

// procTree returns the summed CPU ticks of the LIVE process tree rooted at pid, and how
// many live processes that tree contains (0, 0 when pid is not live/readable). Each
// process contributes its own utime+stime+cutime+cstime, so the sum is MONOTONE: as a
// descendant is reaped its ticks leave the node's own utime/stime and reappear in its
// parent's cutime, which is itself summed — nothing is lost and nothing is counted twice.
//
// This is the reader the idle guard uses, because the root-only form is a false positive
// by construction: cutime/cstime move only on a REAP, so a peer blocked in wait() on a
// long-lived CPU-active child reports a frozen count for the child's whole life (#699).
func procTree(pid int) (ticks uint64, live int) {
	procs := procTreeProcs(pid)
	if procs == nil {
		// /proc itself is unreadable (non-Linux): degrade to the root-only reader rather
		// than reporting a confident 0. A root that is merely gone or a zombie is NOT
		// this case — procTreeProcs returns an empty tree for it, and an empty tree
		// correctly sums to (0, 0).
		if t := procCPUTicks(pid); t > 0 {
			return t, 1
		}
		return 0, 0
	}
	return treeTicks(procs)
}

// procTreeProcs walks /proc ONCE and returns the peer's tree — the root first, then its
// descendants breadth-first — in the SAME order and by the SAME rules the CPU sum uses. It
// is the ONE walker: procTree sums what it returns, and the kill report renders it, so the
// report can never describe a different tree than the one the guard measured.
//
// A ZOMBIE is included (its ticks still count toward the sum) and flagged, because skipping
// it outright would make the sum DROP the instant a child exits and rise again when the
// parent reaps it — a dip is not a false kill, but it would hide a real advance and so could
// manufacture one. Summing until the reap keeps the sum non-decreasing. A descendant that
// double-forks is reparented to init and is NOT attributable to the peer; it is absent from
// the walk, which is one of the things the kill report exists to reveal.
//
// nil means the tree could not be walked at all: pid <= 0, or /proc is unreadable
// (non-Linux). An EMPTY, non-nil slice means /proc was read fine but the root is gone or
// already a zombie — a walkable tree with nothing left in it. The distinction matters to
// the caller's fallback and to the report's wording, so it is deliberate.
func procTreeProcs(pid int) []procInfo {
	if pid <= 0 {
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
	if root, ok := nodes[pid]; !ok || root.zombie {
		return []procInfo{}
	}
	children := make(map[int][]int, len(nodes))
	for n, nd := range nodes {
		if n != pid {
			children[nd.ppid] = append(children[nd.ppid], n)
		}
	}
	// The ppid links form a forest, so this walk terminates on every input.
	out := make([]procInfo, 0, len(nodes))
	queue := []int{pid}
	for len(queue) > 0 {
		n := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		out = append(out, nodes[n])
		queue = append(queue, children[n]...)
	}
	return out
}

// treeTicks sums a walked tree: every process's ticks, and how many are not yet reaped.
// The ONE definition of the sum, so procTree and the kill report cannot drift apart.
func treeTicks(procs []procInfo) (ticks uint64, live int) {
	for _, p := range procs {
		ticks += p.ticks
		if !p.zombie {
			live++
		}
	}
	return ticks, live
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
