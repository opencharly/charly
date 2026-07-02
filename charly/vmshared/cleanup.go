package vmshared

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Temp-file kill-survivability: a small two-mechanism system that
// complements (does not replace) the existing `defer os.Remove(...)`
// pattern at every tempfile creation site.
//
//   1. RegisterTempCleanup + InstallSignalHandler — catch SIGTERM /
//      SIGINT / SIGHUP and remove every registered path before exit.
//      Closes the gap between "graceful exit (defer runs)" and "user
//      hit Ctrl-C (defer doesn't run)".
//
//   2. SweepStaleTemps — at every fresh `charly` invocation, scan
//      `/tmp/charly-*` for known patterns and remove anything not held by
//      a running process AND older than a safety floor. Closes the
//      gap left by SIGKILL / OOM / kernel panic, none of which a
//      process can intercept.
//
// See plans/can-you-make-a-abundant-fog.md for the design and the
// 13.7 GB leftover-tar incident that motivated this.

var (
	tempCleanupsMu sync.Mutex
	tempCleanups   = map[string]struct{}{}
	signalOnce     sync.Once

	shutdownHooksMu sync.Mutex
	shutdownHooks   []func()
)

// RegisterShutdownHook registers fn to run on a catchable shutdown signal
// (SIGTERM/SIGINT/SIGHUP), in the signal handler, before it re-raises the
// signal and the process exits. It is the package-boundary seam that lets
// package main reap resources the vmshared signal handler cannot reference
// directly — notably the connected out-of-process plugin clients
// (providerRegistry.Close): a Ctrl-C'd / `systemctl stop`ped charly kills its
// plugin servers instead of orphaning them (the 77h-orphan leak). Hooks run in
// registration order; each MUST be best-effort and bounded (the handler is
// synchronous). A nil fn is a no-op.
func RegisterShutdownHook(fn func()) {
	if fn == nil {
		return
	}
	shutdownHooksMu.Lock()
	shutdownHooks = append(shutdownHooks, fn)
	shutdownHooksMu.Unlock()
}

// runShutdownHooks runs every registered shutdown hook. Called from the signal
// handler alongside runRegisteredCleanups (a snapshot is taken under the lock so
// a hook may itself register without deadlocking).
func runShutdownHooks() {
	shutdownHooksMu.Lock()
	hooks := append([]func(){}, shutdownHooks...)
	shutdownHooksMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// RegisterTempCleanup registers path for cleanup on graceful shutdown
// (SIGTERM/SIGINT/SIGHUP). Existing `defer os.Remove(...)` callers keep
// working for normal exit; this is a SUPPLEMENTAL safety net for the
// ungraceful-but-catchable signal path. Empty path is a no-op.
func RegisterTempCleanup(path string) {
	if path == "" {
		return
	}
	tempCleanupsMu.Lock()
	tempCleanups[path] = struct{}{}
	tempCleanupsMu.Unlock()
}

// UnregisterTempCleanup removes a path from the registry. Called by
// the existing defer-cleanup paths after they successfully os.Remove
// the file, so the registry doesn't accumulate stale entries in long-
// running charly processes (e.g. `charly mcp serve`).
func UnregisterTempCleanup(path string) {
	tempCleanupsMu.Lock()
	delete(tempCleanups, path)
	tempCleanupsMu.Unlock()
}

// runRegisteredCleanups removes every registered path. Best-effort:
// errors are silently ignored (the file may already be gone via the
// normal defer path). Called from the signal handler.
func runRegisteredCleanups() {
	tempCleanupsMu.Lock()
	paths := make([]string, 0, len(tempCleanups))
	for p := range tempCleanups {
		paths = append(paths, p)
	}
	tempCleanupsMu.Unlock()
	for _, p := range paths {
		// RemoveAll handles both files (CreateTemp) and dirs (MkdirTemp).
		_ = os.RemoveAll(p)
	}
}

// InstallSignalHandler arms the global signal handler exactly once.
// Subsequent calls are no-ops. On SIGTERM/SIGINT/SIGHUP it runs the
// registered temp cleanups AND the registered shutdown hooks (plugin-client
// reaping, etc.), then re-raises the signal so the process exits with the
// conventional 128+signum status code.
//
// SIGKILL is not catchable (kernel-level). Leftover temps from a
// SIGKILL'd charly are caught by the next invocation's SweepStaleTemps;
// orphaned plugin servers from a SIGKILL'd charly self-terminate via the
// plugin SDK's parent-death watch (see plugin/sdk/parentwatch.go).
func InstallSignalHandler() {
	signalOnce.Do(func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		go func() {
			sig := <-ch
			runRegisteredCleanups()
			runShutdownHooks()
			signal.Reset(sig)
			_ = syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
		}()
	})
}

// sweepablePatterns lists `/tmp/<prefix>*` glob roots for stale temps.
// Each prefix matches both files (`os.CreateTemp`) and directories
// (`os.MkdirTemp`). Patterns that SHOULD persist across charly invocations
// (e.g. `charly-tunnel-*.sock` for long-lived SSH forwards declared in
// ssh_tunnel.go) are NOT in this list.
var sweepablePatterns = []string{
	"charly-merge-",              // merge.go saveAndLoad + saveImageToDaemon
	"charly-extpass-",            // candy/plugin-enc encExtpassArgs
	"charly-oldpass-",            // candy/plugin-enc passwdVolumes (gocryptfs -passwd old-pass script)
	"charly-secrets-",            // secrets_gpg.go
	"charly-libvirt-screenshot-", // libvirt_ops.go
	"charly-cidata-",             // cloud_init_iso.go
	"charly-aur-",                // deploy_host_helpers.go
	"charly-priv-",               // privileged_runner.go
}

// sweepSafetyFloor — temp must be at least this old before the sweep
// considers it stale. Guards against racing with a concurrent `charly`
// process that just created the temp (e.g. parallel `charly box build`
// invocations).
const sweepSafetyFloor = 5 * time.Minute

// SweepStaleTemps removes /tmp/<prefix>* entries that are (a) NOT held
// open by any running process AND (b) older than sweepSafetyFloor AND
// (c) owned by the current user. Best-effort: never errors, never
// blocks. Called once from main().
func SweepStaleTemps() {
	uid := os.Getuid()
	heldPaths := openedFilesByAnyProcess()

	for _, prefix := range sweepablePatterns {
		matches, _ := filepath.Glob("/tmp/" + prefix + "*")
		for _, p := range matches {
			info, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if statUid(info) != uid {
				continue // not our temp
			}
			if time.Since(info.ModTime()) < sweepSafetyFloor {
				continue // too recent — concurrent charly may still need it
			}
			if heldPaths[p] {
				continue // a process has it open
			}
			_ = os.RemoveAll(p)
		}
	}
}

// openedFilesByAnyProcess returns the set of /tmp/* paths currently
// held by any running process the caller can read about. Walks
// /proc/<pid>/fd/* symlinks. Best-effort: missing/restricted /proc
// entries are silently skipped.
func openedFilesByAnyProcess() map[string]bool {
	out := map[string]bool{}
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, pe := range procEntries {
		if !pe.IsDir() {
			continue
		}
		if !allDigits(pe.Name()) {
			continue
		}
		fdDir := filepath.Join("/proc", pe.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			tgt, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(tgt, "/tmp/") {
				out[tgt] = true
			}
		}
	}
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func statUid(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
