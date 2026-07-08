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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// acquireImageBuildLock serializes concurrent COLD builds of the SAME image —
// identified by its full registry/name ref, TAG-STRIPPED — across ALL charly
// processes AND projects, while letting DISTINCT image refs (the leaf fan-out)
// build in parallel. User-cache-keyed (sha256 of the ref, like
// acquireLocalPkgBuildLock — R3), NOT per-.build-dir: box/arch's `arch-builder`
// and box/cachyos's namespaced `arch.arch-builder` both resolve to
// ghcr.io/opencharly/arch-builder, so a per-dir lock let them cold-build the
// SAME ref concurrently into the ONE shared podman graphroot (a store-write
// race). Keying on the shared ref serializes them; a busy build blocks, then
// cache-hits.
func acquireImageBuildLock(fullTag string) (func() error, error) {
	path, err := imageBuildLockPath(fullTag)
	if err != nil {
		return nil, err
	}
	return acquireFileLock(path, true)
}

// imageBuildLockPath is the pure lock-key derivation: the user-cache lock file
// for an image ref, keyed on the TAG-STRIPPED registry/name so every tag of the
// same image (and every project producing that ref) shares one lock.
func imageBuildLockPath(fullTag string) (string, error) {
	ref := fullTag
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i] // strip :<tag>, preserving any registry:port colon
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("image build lock: %w", err)
	}
	dir := filepath.Join(cache, "charly", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("image build lock dir: %w", err)
	}
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(dir, "image-"+hex.EncodeToString(sum[:8])+".lock"), nil
}

// acquireVmImageFetchLock serializes concurrent fetches of the SAME cached VM image across
// charly processes (keyed by the content-addressed cache path). Two concurrent VM builds of
// beds sharing one cloud image otherwise race on the shared .part file — one renames it away
// mid-download under the other, and a resumed partial can mix bytes across an upstream
// rotation of a mutable `latest` URL.
func acquireVmImageFetchLock(cachePath string) (func() error, error) {
	return acquireFileLock(cachePath+".lock", true)
}

// acquireLocalPkgBuildLock serializes concurrent host localpkg builds of the SAME source dir
// (pkg/<fmt>) across charly processes — concurrent makepkg runs share the dir's src/ git
// working copies and corrupt each other. Keyed by sha256(srcDir) under the user cache so the
// lock file never pollutes the repo working tree.
func acquireLocalPkgBuildLock(srcDir string) (func() error, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("localpkg build lock: %w", err)
	}
	dir := filepath.Join(cache, "charly", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("localpkg build lock dir: %w", err)
	}
	sum := sha256.Sum256([]byte(srcDir))
	return acquireFileLock(filepath.Join(dir, "localpkg-"+hex.EncodeToString(sum[:8])+".lock"), true)
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
