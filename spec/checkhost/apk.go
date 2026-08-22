package checkhost

// apk.go — the shared committed-APK path resolver (#55 CHECK-ENGINE cone Option A: relocated from
// sdk/kit's apk_path.go). Used by BOTH charly core's resolveCheckApk (the adb:/appium: check-verb
// fixture anchor) and candy/plugin-adb's deploy:android install-spec collector — one shared
// implementation, R3, since neither context is LoadUnified-coupled: both already hold the resolved
// candy source DIRECTORY and only need the pure filesystem walk-up. Homed in this check-host fabric
// slice so charly core reaches it importing zero kit; sdk/kit re-exports both symbols.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveApkPath resolves a committed-APK reference against the candy's SOURCE tree. Absolute
// paths are used verbatim; a relative path anchors candy-dir-relative first, then each ancestor up
// to the candy's project / repo root (first existing match wins). This resolves a path like
// `tests/data/ApiDemos-debug.apk` identically whether the candy is LOCAL (candyDir under the
// consuming project root) or fetched via @github (candyDir under the cloned-repo cache, where a
// project-root-relative file lives at <repo-root>/tests/data/... several levels above candyDir).
//
// It FAILS HARD when a relative ref has no candy dir to anchor against, or when the file is not
// found anywhere up the tree — the caller surfaces that, never silently passing an unresolvable
// path downstream.
func ResolveApkPath(ref, candyDir string) (string, error) {
	if filepath.IsAbs(ref) {
		return ref, nil
	}
	if candyDir == "" {
		return "", fmt.Errorf("cannot resolve relative committed APK %q: no candy source dir to anchor against", ref)
	}
	for dir := candyDir; ; {
		cand := filepath.Join(dir, ref)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("committed APK %q not found under candy source tree %q (searched every ancestor up to the filesystem root)", ref, candyDir)
		}
		dir = parent
	}
}

// ResolveCommittedApk resolves a relative committed-APK path (the adb/appium `apk: ./tests/data/...`
// fixture) against the ORIGINATING candy's source tree, given the caller's own candy-name→source-dir
// map (candyDirs). origin must be "candy:<key>" — the check's Origin form CollectDescriptions stamps,
// and <key> must match a key in candyDirs.
//
// It FAILS HARD on every condition where the fixture cannot be anchored — a non-candy origin, an
// absent candyDirs entry, or a file missing under the candy tree. There is NO fallback and NO silent
// cwd-relative pass-through.
func ResolveCommittedApk(apk, origin string, candyDirs map[string]string, candyScanErr error) (string, error) {
	if apk == "" || filepath.IsAbs(apk) {
		return apk, nil
	}
	key, ok := strings.CutPrefix(origin, "candy:")
	if !ok {
		return "", fmt.Errorf("committed APK %q has origin %q, not a candy origin — cannot anchor it to a candy source tree (the step's candy Origin was not propagated)", apk, origin)
	}
	dir := candyDirs[key]
	if dir == "" {
		if candyScanErr != nil {
			return "", fmt.Errorf("committed APK %q (candy %q): candy source-dir scan failed: %w", apk, key, candyScanErr)
		}
		return "", fmt.Errorf("committed APK %q: candy %q is absent from the source scan (%d candies scanned) — cannot anchor the fixture", apk, key, len(candyDirs))
	}
	return ResolveApkPath(apk, dir)
}
