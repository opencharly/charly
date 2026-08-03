package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// The install-retry race remedy (installWithRetry) moved out of core with the deploy
// ORCHESTRATION in the F1 android-substrate externalization; it now lives in
// candy/plugin-adb (deploy.go), which drives the device install loop out-of-process.

// TestCompileApkStep verifies the candy `apk:` package format compiles into a
// single ApkInstallStep carrying every entry, and that an empty apk: list
// compiles to nothing.
func TestCompileApkStep(t *testing.T) {
	none := testCandy("no-apk", spec.CandyModel{}, spec.CandyView{})
	if step := deploykit.CompileApkStep(none); step != nil {
		t.Errorf("candy with no apk: should compile to nil step, got %T", step)
	}

	l := testCandy("test-apps", spec.CandyModel{
		SourceDir: "/layers/test-apps",
		Apk: []vmshared.ApkPackageSpec{
			{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
			{Apk: "tests/data/x.apk"},
		},
	}, spec.CandyView{})
	step := deploykit.CompileApkStep(l)
	if step == nil {
		t.Fatal("compileApkStep returned nil for a candy with apk: entries")
	}
	apk, ok := step.(*spec.ApkInstallStep)
	if !ok {
		t.Fatalf("compileApkStep returned %T, want *ApkInstallStep", step)
	}
	if apk.Kind() != spec.StepKindApkInstall {
		t.Errorf("Kind() = %q, want %q", apk.Kind(), spec.StepKindApkInstall)
	}
	if len(apk.Packages) != 2 {
		t.Errorf("Packages len = %d, want 2", len(apk.Packages))
	}
	if apk.CandyName != "test-apps" || apk.CandyDir != "/layers/test-apps" {
		t.Errorf("CandyName/CandyDir = %q/%q", apk.CandyName, apk.CandyDir)
	}
	if apk.Reverse() != nil {
		t.Errorf("ApkInstallStep.Reverse() should be nil (android teardown ops are dynamic, recorded from the deploy:android plugin reply)")
	}
}

// TestOCITargetSkipsApkInstall proves apk installs are SKIPPED at image-build (there is no device
// at build time): ociEmitStep routes ApkInstallStep through spliceClassStepEmit, which sees the
// step's Emits=false contract + returns "" — so the dispatch emits nothing.
func TestOCITargetSkipsApkInstall(t *testing.T) {
	step := &spec.ApkInstallStep{
		Packages:  []vmshared.ApkPackageSpec{{Package: "org.fdroid.fdroid"}},
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

// TestPopulateCandyApk verifies the candy manifest `apk:` field flows through the
// scan pipeline onto the resulting spec.CandyReader.
func TestPopulateCandyApk(t *testing.T) {
	ly := &spec.CandyYAML{
		Apk: []vmshared.ApkPackageSpec{
			{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		},
	}
	m, v, _ := loaderkit.ScanInlineCandy("test-apps", "", ly)
	l := testCandy("test-apps", m, v)
	if len(l.Apk()) != 1 || l.Apk()[0].Package != "org.fdroid.fdroid" {
		t.Errorf("Apk() = %+v", l.Apk())
	}
}

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
