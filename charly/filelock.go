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
	"os"
	"path/filepath"
	"time"

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

// acquireVmBuildLock serializes concurrent `charly vm build <entity>` of the SAME entity across
// charly processes, keyed by the entity's output disk dir (output/qcow2/<entity>/.build.lock). N
// check beds sharing one kind:vm entity each preflight `vm build <entity>`; without this they race
// on output/qcow2/<entity>/disk.qcow2 (concurrent overlay-create + resize) AND a second build could
// rewrite the base a live per-domain overlay already backs onto — mutating a supposed read-only
// backing file. Blocking (LOCK_EX): the first builds, the rest wait then skip-if-fresh. The lock is
// held ONLY for the duration of `charly vm build` and released before `charly vm create`, so the
// per-domain overlay-creates run UNSERIALIZED (the whole point of per-deploy isolation — P33).
func acquireVmBuildLock(outputDir string) (func() error, error) {
	return acquireFileLock(filepath.Join(outputDir, ".build.lock"), true)
}

// buildActivityDir is the user-scope directory of LIVE build-activity locks —
// one flocked nonce file per in-flight `charly box build` engine run.
func buildActivityDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("build-activity dir: %w", err)
	}
	dir := filepath.Join(cache, "charly", "locks", "builds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("build-activity dir: %w", err)
	}
	return dir, nil
}

// acquireBuildActivityLock registers this build invocation as LIVE for its whole
// duration: a flocked nonce file whose CONTENT is the build's generate CalVer —
// the floor of every FROM pin its generated Containerfiles carry. Image-tag
// retention (pruneImagesByRetention) consults the live set so a completing
// sibling build can never untag a pin an in-flight build still resolves — the
// retention-untag race the concurrent bed fan-out surfaced.
func acquireBuildActivityLock(calver string) (func() error, error) {
	dir, err := buildActivityDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("build-%d-%d.lock", os.Getpid(), time.Now().UnixNano()))
	release, err := acquireFileLock(path, true)
	if err != nil {
		return nil, fmt.Errorf("build-activity lock: %w", err)
	}
	if err := os.WriteFile(path, []byte(calver+"\n"), 0o644); err != nil {
		_ = release()
		return nil, fmt.Errorf("build-activity lock: record calver: %w", err)
	}
	return func() error {
		err := release()
		_ = os.Remove(path)
		return err
	}, nil
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
