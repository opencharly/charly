package main

// filelock.go — charly core's advisory-flock ENTRY. The primitive itself lives in
// sdk/kit (kit.AcquireFileLock) so it is shared, byte-identical, with the compiled-in
// candy/plugin-preempt (the resource arbiter's ledger lock) across the module boundary (R3).
// This file keeps the core alias + the two charly-specific wrappers whose lock paths depend on
// package-main config resolution the kit primitive cannot reach.
//
// Contention semantics (kit.AcquireFileLock's `blocking` arg):
//   - per-bed check lock      .check/<bed>/.lock                    (fail-fast)
//   - AI-harness run lock     .check/<score>/.lock                 (fail-fast)
//   - deploy-config write     ~/.config/charly/charly.yml.lock     (blocking)
//   - install ledger          ~/.config/opencharly/installed/.lock (blocking)
//   - resource-arbiter ledger ~/.local/share/charly/preemption/.lock (blocking, IN the plugin)

import (
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
)

// errLockBusy is kit.ErrLockBusy — the non-blocking-contention sentinel core callers match with
// errors.Is (check_bed_run / check_runlocal_cmd).
var errLockBusy = kit.ErrLockBusy

// acquireFileLock is the core alias of the shared kit primitive.
func acquireFileLock(path string, blocking bool) (release func() error, err error) {
	return kit.AcquireFileLock(path, blocking)
}

// acquireImageBuildLock serializes concurrent builds of the SAME image across charly processes
// while letting DISTINCT images build in PARALLEL (keyed by image name under .build/.locks/).
func acquireImageBuildLock(buildDir, image string) (func() error, error) {
	return acquireFileLock(filepath.Join(buildDir, ".locks", image+".lock"), true)
}

// acquireDeployConfigLock serializes the read-modify-write of the per-host deploy overlay
// (~/.config/charly/charly.yml) across concurrent charly processes. Blocking (a config write is
// brief, so serialize rather than fail).
func acquireDeployConfigLock() (func() error, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, fmt.Errorf("deploy-config lock path: %w", err)
	}
	return acquireFileLock(path+".lock", true)
}
