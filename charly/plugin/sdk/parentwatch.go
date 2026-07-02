package sdk

import (
	"os"
	"time"
)

// parentWatchInterval is how often the orphan backstop polls the parent PID.
// Named (not a magic literal) per CLAUDE.md R4; it bounds the worst-case delay
// between the host dying and an orphaned plugin self-terminating.
const parentWatchInterval = 2 * time.Second

// watchParentDeath starts the orphan backstop: a goroutine that self-exits this
// plugin process when its parent — the charly host that spawned it — dies.
//
// WHY this exists. charly spawns every external plugin over go-plugin's exec
// model (LocalTransport runs `<plugin> __plugin serve` as a direct child), and
// go-plugin's server (v1.8.0) has NO parent-death detection of its own: it eats
// SIGINT and blocks until grpc.Server.Serve returns, which only happens when the
// host sends the gRPC Shutdown control RPC — and the host sends that ONLY from
// client.Kill(). If the host exits WITHOUT killing its clients (a crash, a
// SIGKILL, an os.Exit on an error path, or simply never calling
// providerRegistry.Close), the child is reparented to PID 1 and blocks forever.
// That is the leak: ~6,000 orphaned `__plugin serve` processes accumulating over
// days → 78k threads → "newosproc: failed to create new OS thread". This watch
// reaps those orphans even when the host-side reaping never runs.
//
// WHY it does NOT break the unbounded credential await-unlock RPC. The watch
// fires ONLY on ACTUAL parent death (getppid changes from the value captured at
// startup — typically to 1 once the process is reparented). While the parent
// lives, getppid is stable, so a LIVE host doing an unbounded await is never
// disturbed; the wait blocks as long as the user takes. And once the parent is
// gone, the await result has nowhere to return anyway — self-exiting is the
// correct outcome, not a regression. The go-plugin local-socket transport keeps
// its no-keepalive property (this watch adds no gRPC keepalive / idle timeout),
// so the unbounded-RPC contract the credential await-unlock relies on is intact.
func watchParentDeath() {
	ppid := os.Getppid()
	// ppid <= 1 means there is no live parent to watch: the process is already an
	// orphan, was launched directly by PID 1, or runs under a test harness. There
	// is nothing to back-stop and watching would misfire — skip.
	if ppid <= 1 {
		return
	}
	go runParentWatch(ppid, parentWatchInterval, os.Getppid, func() { os.Exit(0) })
}

// runParentWatch polls getppid every interval and calls onOrphaned exactly once,
// the moment the parent PID differs from startPPID (the parent died → this
// process was reparented). Extracted from watchParentDeath so tests drive it
// with injected fakes — no real fork required.
func runParentWatch(startPPID int, interval time.Duration, getppid func() int, onOrphaned func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		if getppid() != startPPID {
			onOrphaned()
			return
		}
	}
}
