package main

import (
	"context"
	"errors"
	"fmt"
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
// THE SIGNAL IS HOST-VISIBLE WORK, not wall-clock. Every reverse-channel LEAF the
// host performs for the peer plugin (RunSystem/RunUser/PutFile/GetFile/RunCapture/
// RunHostStep/HostBuild) touches the call's activity clock, and a long-running host
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

// pluginInvokeNoProgressOverride, when > 0, replaces the config-sourced window. A test
// seam only; production leaves it 0. Read before the watchdog starts.
var pluginInvokeNoProgressOverride time.Duration

// pluginActivity is one call's forward-progress clock. A nil *pluginActivity is safe:
// touch is a no-op and idleFor reports 0, so a server without a clock never trips.
type pluginActivity struct{ nanos atomic.Int64 }

func (a *pluginActivity) touch() {
	if a == nil {
		return
	}
	a.nanos.Store(time.Now().UnixNano())
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
	if pluginInvokeNoProgressOverride > 0 {
		return pluginInvokeNoProgressOverride
	}
	return pluginLeafCapDefault
}

// pluginInvokeNoProgress resolves the no-progress window: test override, else the
// project readiness `no_progress`, else the built-in fallback. loadedReadiness() is
// sync.Once-cached + re-entrancy-guarded (it returns built-in defaults mid-load), so
// this is safe from any call site and cheap after the first.
func pluginInvokeNoProgress() time.Duration {
	if pluginInvokeNoProgressOverride > 0 {
		return pluginInvokeNoProgressOverride
	}
	if np := loadedReadiness().NoProgress; np > 0 {
		return np
	}
	return 90 * time.Second
}

// idleBoundedContext returns a context cancelled with errPluginCallIdle when the
// call's activity clock stays untouched for noProgress, plus a stop func. It carries
// NO wall-clock cap (see the file header). Applied only when the caller supplied no
// deadline of its own.
func idleBoundedContext(ctx context.Context, noProgress time.Duration, a *pluginActivity) (context.Context, context.CancelFunc) {
	a.touch() // the dispatch itself is baseline activity
	cctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	go func() {
		interval := noProgress / 4
		if interval < 10*time.Millisecond {
			interval = 10 * time.Millisecond
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-cctx.Done():
				return
			case <-t.C:
				if a.idleFor() >= noProgress {
					cancel(errPluginCallIdle)
					return
				}
			}
		}
	}()
	return cctx, func() { close(stop); cancel(nil) }
}

// startPluginActivityHeartbeat touches the clock every 5s until stopped. host_build_cli
// wraps its blocking host-child run with this so the child being ALIVE is forward
// progress for the whole (possibly ~13min) duration — the signal that distinguishes a
// progressing install from a wedged plugin.
func startPluginActivityHeartbeat(a *pluginActivity) func() {
	if a == nil {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
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
