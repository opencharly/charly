package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// TestCandySourceDirs_OverrideAnchorsRemoteApk is the integration guard for the
// box/<distro> bed apk path: ScanAllCandyWithConfig + candyDirsFromScan under
// CHARLY_REPO_OVERRIDE (the dev-local local-candy override) MUST map the
// override-resolved remote android candy by its @github BARE REF — the exact key
// the baked check Origin carries — to a SourceDir under the override root, so
// resolveCheckApk anchors the committed `./tests/data/ApiDemos-debug.apk`. This
// proves the real scan keys remote candies the same way the runtime Origin does;
// the per-step Origin propagation that feeds it is guarded by
// TestRunPlan_StampsStepOrigin (apk_format_test.go).
//
// PHASE-4 shape: the android candy lives STANDALONE at the repo ROOT of
// opencharly/pod-android-emulator-layer (the candy de-submodule cutover), so the
// box composes the BARE ref `@github.com/opencharly/pod-android-emulator-layer`
// (no candy/ subpath) and the scan keys it by that bare ref. The test builds a
// SYNTHETIC pod checkout (root manifest + committed APK fixture) + a synthetic
// box bed composing the bare ref — hermetic, no network, no distro submodule.
func TestCandySourceDirs_OverrideAnchorsRemoteApk(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd) // .../av-charly — owns the committed APK fixture
	apkFixture := filepath.Join(repoRoot, "tests", "data", "ApiDemos-debug.apk")
	if _, err := os.Stat(apkFixture); err != nil {
		t.Skipf("committed APK fixture absent (%v)", apkFixture)
	}

	// Synthetic checkout of the STANDALONE pod repo: the candy manifest lives at
	// the repo ROOT (Phase-4 shape — a bare ref, no candy/ subpath), and the
	// committed APK fixture ships at tests/data/ under that root.
	podRoot := t.TempDir()
	podManifest := "version: 2026.248.1030\n" +
		"android-emulator-layer:\n" +
		"    candy:\n" +
		"        version: 2026.174.0700\n"
	if err := os.WriteFile(filepath.Join(podRoot, spec.UnifiedFileName), []byte(podManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(podRoot, "tests", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(apkFixture, filepath.Join(podRoot, "tests", "data", "ApiDemos-debug.apk")); err != nil {
		t.Fatalf("seed pod fixture: %v", err)
	}

	// Synthetic box bed composing the standalone pod candy by its BARE ref — the
	// post-cutover shape of the distro box/android-emulator candy list.
	boxDir := t.TempDir()
	boxManifest := "version: 2026.248.1030\n" +
		"synthetic-box:\n" +
		"    candy:\n" +
		"        candy:\n" +
		"            - '@github.com/opencharly/pod-android-emulator-layer:v2026.237.938'\n"
	if err := os.WriteFile(filepath.Join(boxDir, spec.UnifiedFileName), []byte(boxManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(proc.RepoOverrideEnv, "github.com/opencharly/pod-android-emulator-layer="+podRoot)

	uf, ok, err := LoadUnified(boxDir)
	if err != nil || !ok || uf == nil {
		t.Fatalf("LoadUnified(synthetic box): ok=%v err=%v", ok, err)
	}
	cfg := uf.ProjectConfig()
	scanned, scanErr := ScanAllCandyWithConfig(boxDir, cfg)
	if scanErr != nil {
		t.Fatalf("candySourceDirs scan failed: %v", scanErr)
	}
	dirs := candyDirsFromScan(scanned)

	// The BARE repo ref is the key — the override maps it to the synthetic pod root.
	const key = "github.com/opencharly/pod-android-emulator-layer"
	src, found := dirs[key]
	t.Logf("candySourceDirs entries: %d; android-emulator-layer present=%v src=%q", len(dirs), found, src)
	if !found || src == "" {
		t.Fatalf("candySourceDirs missing/empty SourceDir for %q under override — the check could not anchor the committed APK", key)
	}
	if src != podRoot {
		t.Fatalf("override SourceDir = %q, want the synthetic pod root %q", src, podRoot)
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

// copyFile copies src to dst (the committed test fixtures are binary artifacts).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
