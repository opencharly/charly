package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCandySourceDirs_OverrideAnchorsRemoteApk is the integration guard for the
// box/<distro> bed apk path: ScanAllCandyWithConfig(box/cachyos, cfg) + candyDirsFromScan
// under CHARLY_REPO_OVERRIDE (the dev-localpkg local-candy override) MUST map the
// override-resolved remote android candy by its @github BARE REF — the exact key
// the baked check Origin carries — to a SourceDir under the override root, so
// resolveCheckApk anchors the committed `./tests/data/ApiDemos-debug.apk`. This
// proves the real scan keys remote candies the same way the runtime Origin does;
// the per-step Origin propagation that feeds it is guarded by
// TestRunPlan_StampsStepOrigin (apk_format_test.go).
func TestCandySourceDirs_OverrideAnchorsRemoteApk(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd) // .../av-charly
	boxCachyos := filepath.Join(repoRoot, "box", "cachyos")
	apkFixture := filepath.Join(repoRoot, "tests", "data", "ApiDemos-debug.apk")
	// An un-inited box/cachyos submodule leaves an EMPTY gitlink placeholder dir,
	// so os.Stat(boxCachyos) succeeds and LoadUnified then hard-fails on the
	// missing manifest; detect "checked out" by the charly.yml manifest instead
	// (boxSubmoduleCheckedOut) so a box-less checkout skips gracefully.
	if !boxSubmoduleCheckedOut(boxCachyos) {
		t.Skip("box/cachyos submodule not checked out (no charly.yml) — box-less checkout")
	}
	if _, err := os.Stat(apkFixture); err != nil {
		t.Skipf("committed APK fixture absent (%v)", apkFixture)
	}

	t.Setenv(RepoOverrideEnv, "github.com/opencharly/charly="+repoRoot)

	uf, ok, err := LoadUnified(boxCachyos)
	if err != nil || !ok || uf == nil {
		t.Fatalf("LoadUnified(box/cachyos): ok=%v err=%v", ok, err)
	}
	cfg := uf.ProjectConfig()
	candyMap, scanErr := ScanAllCandyWithConfig(boxCachyos, cfg)
	if scanErr != nil {
		t.Fatalf("candySourceDirs scan failed: %v", scanErr)
	}
	dirs := candyDirsFromScan(candyMap)

	const key = "github.com/opencharly/charly/candy/android-emulator-layer"
	src, found := dirs[key]
	t.Logf("candySourceDirs entries: %d; android-emulator-layer present=%v src=%q", len(dirs), found, src)
	if !found || src == "" {
		t.Fatalf("candySourceDirs missing/empty SourceDir for %q under override — the check could not anchor the committed APK", key)
	}

	// The committed apk path must resolve against that SourceDir (walking up to the repo root).
	r := hostVerbResolverWithCandyDirs(dirs, nil)
	resolved, err := r.resolveCheckApk("./tests/data/ApiDemos-debug.apk", "candy:"+key)
	if err != nil {
		t.Fatalf("resolveCheckApk errored: %v", err)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("resolveCheckApk did not resolve the committed APK: got %q (stat: %v)", resolved, err)
	}
	t.Logf("resolved apk -> %q", resolved)
}
