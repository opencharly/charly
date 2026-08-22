package proc

import (
	"os"
	"syscall"
)

// temphold.go — kernel-guaranteed liveness for a temp the stale-temp sweep can see.
//
// WHY. SweepStaleTemps (cleanup.go) removes a temp when it is older than the safety floor and no
// process holds it open. Both of those guards fail for a LIVE long-running build, and the roster
// proved it by killing two beds mid-flight:
//
//   - The age guard reads the temp ROOT's mtime, and a directory's mtime only changes when its OWN
//     entries change. A makepkg stage tree writes into src/ and pkg/ subdirectories, so the root's
//     mtime stays frozen at creation and the tree looks "old" within minutes of being created.
//   - The open-fd guard walks /proc/<pid>/fd. A build process holds its stage tree as its CWD, not
//     as an open descriptor, and a CWD is invisible there (it is /proc/<pid>/cwd).
//
// Both were verified directly on the host before this fix was written: a deep write leaves the root
// mtime unchanged, and a process whose CWD is the directory contributes zero matching fd entries.
// The observed damage was a cross-process RemoveAll landing inside a running makepkg — one bed died
// at stage age 5m05s with its source tree gone, another watched its files vanish during package().
//
// WHY NOT A BIGGER FLOOR. Raising sweepSafetyFloor is the workaround R4 forbids: it does not make
// the sweep correct, it only moves the threshold past whatever build was measured last, and the
// next slower build (or a loaded host) walks into the same deletion. The floor cannot distinguish
// "abandoned" from "slow" because age is not the property being asked about — liveness is.
//
// THE MECHANISM. The creator opens the temp root and holds an exclusive flock on that descriptor
// for the operation's lifetime; the sweep probes with a NON-BLOCKING flock and skips anything held.
// The kernel releases an flock when the holding descriptor closes, including when the process dies
// without running any cleanup — which is exactly the SIGKILL / OOM / panic case the sweep exists
// for. So the sweep still reaps a killed build's leftovers on the very next charly invocation,
// while a live build of any duration is untouchable. Verified on a throwaway spike before this was
// written: FREE with no holder, BUSY while a live process holds it, FREE again after that process
// was killed without unlocking.
//
// WHAT EACH GUARD ACTUALLY CONTRIBUTES — stated precisely, because the two overlap. Routing a
// creator through MkdirTempHeld fixes the deletion twice over: the hold keeps a descriptor OPEN on
// the temp root, which is the very thing the pre-existing /proc/<pid>/fd guard was looking for and
// never found (the old creators opened nothing), AND the flock gives an explicit liveness answer.
// Measured: removing the TempIsHeld probe from the sweep does NOT resurrect the defect on a host
// with a readable /proc, because the open descriptor alone satisfies the older guard. The probe
// earns its place by not depending on /proc being readable or unnamespaced — a build running inside
// a container against a bind-mounted host stage dir is exactly the case where an fd walk can come up
// empty while the file lock still answers correctly — and by saying what it means rather than
// inferring liveness from an incidental descriptor. The decisive control for the fix as a whole is
// therefore the CREATOR shape, not the probe: TestSweepStaleTemps_UnheldCreatorIsSwept shows a bare
// os.MkdirTemp temp being deleted exactly as the roster's builds were.
//
// Best-effort by design: if the lock cannot be taken the temp is still created and still registered
// for cleanup, and behavior degrades to exactly what it was before this file existed. A build must
// never fail because a lock could not be acquired.

// holdPath opens path (a directory or a regular file) and takes an exclusive, non-blocking flock on
// the descriptor, returning a release func that drops the lock by closing it. The returned func is
// never nil, so a caller can always defer it.
//
// The *os.File is captured by the closure, which is what keeps the descriptor — and therefore the
// lock — alive for the operation's lifetime.
func holdPath(path string) func() {
	f, err := os.Open(path)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Someone else holds it (or the filesystem has no flock support). Either way this
		// caller proceeds unheld — the pre-existing behavior, never a build failure.
		_ = f.Close()
		return func() {}
	}
	return func() { _ = f.Close() }
}

// TempIsHeld reports whether some process is holding path with the flock MkdirTempHeld /
// CreateTempHeld take — i.e. whether an operation is still live inside it. Used by SweepStaleTemps
// to skip a busy temp.
//
// An unreadable path answers false: the sweep's other guards (ownership, age, open descriptors)
// still apply, so a path this probe cannot open is treated exactly as it was before.
func TempIsHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// MkdirTempHeld is os.MkdirTemp plus the two things every sweepable-namespace creator already did
// by hand: register the path for signal-driven cleanup, and — new here — hold it against the sweep
// for the operation's lifetime.
//
// release drops ONLY the hold. It deliberately does not delete the temp and does not unregister the
// cleanup, because those two policies differ per caller and folding them in would silently change
// behavior: a site that keeps its artifacts on success (the localpkg and pkgdep builders) keeps the
// cleanup registration too, so a later SIGTERM still reaps them. Callers keep their existing
// RemoveAll / UnregisterTempCleanup defers exactly as they were; only the hold is new.
//
// Defer it immediately after creation, before the caller's own removal defer, so that LIFO ordering
// deletes the tree while the hold is still active and releases afterwards.
//
// This is the single creation path for every swept namespace (R3). Before it existed each site
// paired a bare os.MkdirTemp with its own RegisterTempCleanup and nothing held the tree, which is
// how a live build could be deleted underneath itself.
func MkdirTempHeld(dir, pattern string) (path string, release func(), err error) {
	path, err = os.MkdirTemp(dir, pattern)
	if err != nil {
		return "", func() {}, err
	}
	drop := holdPath(path)
	RegisterTempCleanup(path)
	return path, drop, nil
}

// CreateTempHeld is the file twin of MkdirTempHeld. The hold rides a SEPARATE descriptor from the
// returned *os.File on purpose: callers routinely write the file, close it, and then hand the PATH
// to a subprocess that reads it much later (a gocryptfs extpass script sitting at a passphrase
// prompt, a multi-GB image tarball being loaded back). Locking the caller's own descriptor would
// release the moment they closed it — precisely when the exposure begins.
//
// As with MkdirTempHeld, release drops only the hold; closing, deleting and unregistering the file
// remain the caller's, unchanged.
func CreateTempHeld(dir, pattern string) (f *os.File, release func(), err error) {
	f, err = os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, func() {}, err
	}
	name := f.Name()
	drop := holdPath(name)
	RegisterTempCleanup(name)
	return f, drop, nil
}
