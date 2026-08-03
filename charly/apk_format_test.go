package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The install-retry race remedy (installWithRetry) moved out of core with the deploy
// ORCHESTRATION in the F1 android-substrate externalization; it now lives in
// candy/plugin-adb (deploy.go), which drives the device install loop out-of-process.

// TestCompileApkStep moved to sdk/deploykit/apk_format_test.go (#55 K3 Cone 4) — it exercises
// deploykit.CompileApkStep directly against literal spec fixtures, no charly loader needed.

// TestOCITargetSkipsApkInstall proves apk installs are SKIPPED at image-build (there is no device
// at build time): ociEmitStep routes ApkInstallStep through spliceClassStepEmit, which sees the
// step's Emits=false contract + returns "" — so the dispatch emits nothing.
func TestOCITargetSkipsApkInstall(t *testing.T) {
	step := &spec.ApkInstallStep{
		Packages:  []spec.ApkPackageSpec{{Package: "org.fdroid.fdroid"}},
		CandyName: "test-apps",
	}
	frag, err := ociEmitStep(step, &spec.InstallPlan{}, nil, buildEngineContext{})
	if err != nil {
		t.Fatalf("ociEmitStep(ApkInstallStep) = %v, want nil (skip)", err)
	}
	if frag != "" {
		t.Errorf("ociEmitStep emitted %q for an apk step; should emit nothing", frag)
	}
}

// TestPopulateCandyApk moved to sdk/loaderkit/scan_candy_test.go (#55 K3 Cone 4) — it
// exercises loaderkit.ScanInlineCandy directly against a literal spec.CandyYAML fixture,
// no charly loader needed.

// TestResolveApkPath moved to sdk/kit/apk_path_test.go (FINAL/K5 unit 6a) — the
// resolveApkPath implementation relocated to kit.ResolveApkPath, shared by this file's
// TestResolveCheckApk (host-side, core) AND candy/plugin-adb's deploy:android install-spec
// collector (preresolve.go).

// TestResolveCheckApk covers the check-verb path resolution (adb: install /
// appium: install-app). It anchors a relative committed-APK ref against the
// AUTHORING candy's source dir (CandyDirs[origin-key]) and FAILS HARD on every
// condition where it cannot — a non-candy origin, an absent CandyDirs entry, or
// a missing file. There is NO fallback and NO silent cwd-relative pass-through.
func TestResolveCheckApk(t *testing.T) {
	repo := t.TempDir()
	apk := filepath.Join(repo, "tests", "data", "x.apk") // project-root fixture
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	authorDir := filepath.Join(repo, "candy", "android-emulator-layer")
	siblingDir := filepath.Join(repo, "candy", "sshd")
	for _, d := range []string{authorDir, siblingDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// LOCAL candy: map key == bare name. Origin "candy:<name>" → resolves.
	r := hostVerbResolverWithCandyDirs(map[string]string{"android-emulator-layer": authorDir, "sshd": siblingDir}, nil)
	if got, err := r.resolveCheckApk("./tests/data/x.apk", "candy:android-emulator-layer"); err != nil || got != apk {
		t.Errorf("local-candy resolve = (%q,%v), want (%q,nil)", got, err, apk)
	}
	// FETCHED candy: map key == bare @github ref, and the step Origin is stamped
	// with that same ref (description_run.go op.Origin = fs.origin). CandyDirs[ref]
	// must match.
	const ref = "github.com/owner/repo/candy/android-emulator-layer"
	rRemote := hostVerbResolverWithCandyDirs(map[string]string{ref: authorDir}, nil)
	if got, err := rRemote.resolveCheckApk("./tests/data/x.apk", "candy:"+ref); err != nil || got != apk {
		t.Errorf("fetched-candy (ref-keyed) resolve = (%q,%v), want (%q,nil)", got, err, apk)
	}
	// Authoring candy NOT in CandyDirs → HARD ERROR (no fallback to a sibling).
	r2 := hostVerbResolverWithCandyDirs(map[string]string{"sshd": siblingDir}, nil)
	if _, err := r2.resolveCheckApk("./tests/data/x.apk", "candy:android-emulator-layer"); err == nil {
		t.Error("unknown candy must error, got nil")
	}
	// A scan error surfaces as the root cause (not a misleading not-found).
	r2boom := hostVerbResolverWithCandyDirs(map[string]string{"sshd": siblingDir}, errors.New("boom"))
	if _, err := r2boom.resolveCheckApk("./tests/data/x.apk", "candy:android-emulator-layer"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("scan-error path = %v, want error mentioning the scan failure", err)
	}
	// Non-candy origin (the step's candy Origin was lost) → HARD ERROR.
	if _, err := r2.resolveCheckApk("./tests/data/x.apk", "box:android-emulator"); err == nil {
		t.Error("non-candy origin must error, got nil")
	}
	// Absolute passes through (no anchoring needed).
	if got, err := r2.resolveCheckApk("/abs/y.apk", "candy:foo"); err != nil || got != "/abs/y.apk" {
		t.Errorf("absolute = (%q,%v), want (/abs/y.apk,nil)", got, err)
	}
}

// TestRunPlan_StampsStepOrigin (the per-step Origin re-stamping regression guard) moved to
// sdk/kit/planrun_dispatch_test.go (#55 decoupling cone, Batch D) — the assertion subject is
// kit.RunPlan's OWN stamping mechanism (op.Origin = fs.origin), a kit capability, verified
// there directly against a stub VerbResolver rather than round-tripping through a stub
// external adb provider + a scan-error sentinel message.
