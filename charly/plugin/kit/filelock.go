package kit

// filelock.go — the ONE advisory-flock primitive, shared by charly core (filelock.go's
// specialized wrappers) AND the compiled-in candy/plugin-preempt (the resource-arbiter's
// per-acquire ledger lock). It lives in kit so there is a SINGLE implementation across the
// module boundary (R3) — kit imports only the stdlib, so both sides link the same code.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLockBusy is returned by a NON-blocking AcquireFileLock when another holder already owns the
// lock. Callers detect it with errors.Is to render a precise "already in progress" message.
var ErrLockBusy = errors.New("file lock held by another process")

// AcquireFileLock takes an advisory flock on path (creating the file + parent dirs on demand) and
// returns a release closure that unlocks + closes.
//
// blocking selects the contention behavior:
//   - true  → LOCK_EX: wait until the lock is free (serialize, never fail).
//   - false → LOCK_EX|LOCK_NB: return ErrLockBusy immediately when another holder exists.
//
// The lock file is deliberately NOT unlinked on release (unlinking a held lock races a waiter
// that already opened the prior inode). flock is per-open-file-description, so two acquires of the
// same path — even within ONE process — contend, which the duplicate-run guard relies on.
func AcquireFileLock(path string, blocking bool) (release func() error, err error) {
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return nil, fmt.Errorf("create lock dir %s: %w", filepath.Dir(path), mkErr)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	how := syscall.LOCK_EX
	if !blocking {
		how |= syscall.LOCK_NB
	}
	if flockErr := syscall.Flock(int(f.Fd()), how); flockErr != nil {
		_ = f.Close()
		if !blocking {
			return nil, fmt.Errorf("%s: %w", path, ErrLockBusy)
		}
		return nil, fmt.Errorf("flock %s: %w", path, flockErr)
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	return func() error {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return f.Close()
	}, nil
}
