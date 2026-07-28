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

	"github.com/opencharly/sdk/kit"
)

// errLockBusy is kit.ErrLockBusy — the non-blocking-contention sentinel core callers match with
// errors.Is (check_bed_run / check_runlocal_cmd).
var errLockBusy = kit.ErrLockBusy

// acquireFileLock is the core alias of the shared kit primitive.
func acquireFileLock(path string, blocking bool) (release func() error, err error) {
	return kit.AcquireFileLock(path, blocking)
}

// acquireVmImageFetchLock serializes concurrent fetches of the SAME cached VM image across
// charly processes (keyed by the content-addressed cache path). Two concurrent VM builds of
// beds sharing one cloud image otherwise race on the shared .part file — one renames it away
// mid-download under the other, and a resumed partial can mix bytes across an upstream
// rotation of a mutable `latest` URL.

// acquireLocalPkgBuildLock serializes concurrent host localpkg builds of the SAME source dir
// (pkg/<fmt>) across charly processes — concurrent makepkg runs share the dir's src/ git
// working copies and corrupt each other. Keyed by sha256(srcDir) under the user cache so the
// lock file never pollutes the repo working tree.

// The build-activity lock (formerly core's acquireBuildActivityLock) moved to
// candy/plugin-box's dispatchBuild in P8b, reconstructed there from the SAME shared
// kit primitives (kit.BuildActivityDir + kit.AcquireFileLock) so the plugin's
// `charly box build` drive marks its invocation LIVE for the externalized retention
// engine (candy/plugin-clean) to respect — no core copy remains.

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
