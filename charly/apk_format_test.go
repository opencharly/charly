package main

import (
	"context"
	"errors"
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// The install-retry race remedy (installWithRetry) moved out of core with the deploy
// ORCHESTRATION in the F1 android-substrate externalization; it now lives in
// candy/plugin-adb (deploy.go), which drives the device install loop out-of-process.

// TestCompileApkStep verifies the candy `apk:` package format compiles into a
// single ApkInstallStep carrying every entry, and that an empty apk: list
// compiles to nothing.
func TestCompileApkStep(t *testing.T) {
	none := &Candy{Name: "no-apk"}
	if step := compileApkStep(none); step != nil {
		t.Errorf("candy with no apk: should compile to nil step, got %T", step)
	}

	l := &Candy{Name: "test-apps", SourceDir: "/layers/test-apps"}
	l.apk = []ApkPackageSpec{
		{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		{Apk: "tests/data/x.apk"},
	}
	step := compileApkStep(l)
	if step == nil {
		t.Fatal("compileApkStep returned nil for a candy with apk: entries")
	}
	apk, ok := step.(*ApkInstallStep)
	if !ok {
		t.Fatalf("compileApkStep returned %T, want *ApkInstallStep", step)
	}
	if apk.Kind() != StepKindApkInstall {
		t.Errorf("Kind() = %q, want %q", apk.Kind(), StepKindApkInstall)
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
	step := &ApkInstallStep{
		Packages:  []ApkPackageSpec{{Package: "org.fdroid.fdroid"}},
		CandyName: "test-apps",
	}
	frag, err := ociEmitStep(step, &InstallPlan{}, nil, buildEngineContext{})
	if err != nil {
		t.Fatalf("ociEmitStep(ApkInstallStep) = %v, want nil (skip)", err)
	}
	if frag != "" {
		t.Errorf("ociEmitStep emitted %q for an apk step; should emit nothing", frag)
	}
}

// TestPopulateCandyApk verifies the candy manifest `apk:` field flows through the
// populator onto the resolved Candy.
func TestPopulateCandyApk(t *testing.T) {
	ly := &spec.CandyYAML{
		Apk: []ApkPackageSpec{
			{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		},
	}
	l := &Candy{Name: "test-apps"}
	populateCandyFromYAML(l, ly)
	if !l.HasApk() {
		t.Fatal("HasApk() = false after populating apk: entries")
	}
	if len(l.Apk()) != 1 || l.Apk()[0].Package != "org.fdroid.fdroid" {
		t.Errorf("Apk() = %+v", l.Apk())
	}
}

// TestResolveApkPath checks committed-APK path resolution: absolute verbatim,
// candy-relative when present, project-root-relative via walk-up (the @github
// fetched-candy case), and a HARD ERROR when nothing resolves (no silent
// cwd-relative pass-through).
func TestResolveApkPath(t *testing.T) {
	if got, err := resolveApkPath("/abs/x.apk", "/layers/foo"); err != nil || got != "/abs/x.apk" {
		t.Errorf("absolute path = (%q,%v), want (/abs/x.apk,nil)", got, err)
	}
	// No anchor resolves (candyDir + ancestors lack the file) → HARD ERROR.
	if _, err := resolveApkPath("tests/data/x.apk", "/nonexistent-layer-dir"); err == nil {
		t.Error("missing file under candyDir must error, got nil")
	}
	// No candy dir at all → HARD ERROR (cannot anchor a relative ref).
	if _, err := resolveApkPath("tests/data/x.apk", ""); err == nil {
		t.Error("empty candyDir for a relative ref must error, got nil")
	}

	// Fetched-candy layout: <repo>/tests/data/x.apk exists, candyDir is
	// <repo>/candy/android-apidemos (the file is NOT under candyDir). The walk-up
	// must resolve the project-root-relative ref to <repo>/tests/data/x.apk.
	repo := t.TempDir()
	candyDir := filepath.Join(repo, "candy", "android-apidemos")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	apk := filepath.Join(repo, "tests", "data", "x.apk")
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveApkPath("tests/data/x.apk", candyDir); err != nil || got != apk {
		t.Errorf("project-root walk-up = (%q,%v), want (%q,nil)", got, err, apk)
	}

	// Candy-relative takes priority (closest anchor wins) when the file sits
	// directly under candyDir.
	localApk := filepath.Join(candyDir, "local.apk")
	if err := os.WriteFile(localApk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveApkPath("local.apk", candyDir); err != nil || got != localApk {
		t.Errorf("candy-relative = (%q,%v), want (%q,nil)", got, err, localApk)
	}
}

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

// stubAdbExternalVerb is an out-of-process-style `adb` verb Provider (NOT a
// CheckVerbProvider) — the shape adb takes after the adb → external-plugin dep-shed.
// runOne dispatches it via the else-branch (invokeVerbProvider), where the committed-APK
// Origin is consumed by resolveCheckApk host-side BEFORE the wire Invoke (which the stub
// never reaches here, because resolveCheckApk errors first).
type stubAdbExternalVerb struct{}

func (stubAdbExternalVerb) Reserved() string     { return "adb" }
func (stubAdbExternalVerb) Class() ProviderClass { return ClassVerb }
func (stubAdbExternalVerb) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{JSON: []byte(`{"status":"pass","message":"stub"}`)}, nil
}

// TestRunPlan_StampsStepOrigin is the regression guard for the per-step Origin
// propagation (description_run.go: op.Origin = fs.origin). The candy-group
// Origin lives ONCE on the LabeledDescription and is NOT baked per-step in the
// OCI label; RunPlan must re-stamp it onto each dispatched Op, or an adb/appium
// committed-APK check sees an empty c.Origin and cannot anchor its fixture —
// the live android-emulator-pod "no such file" bug.
//
// adb is now an EXTERNAL-CHARLY-VERB, so the committed-APK anchoring (resolveCheckApk)
// runs inside invokeVerbProvider (the out-of-process dispatch path): we register a stub
// adb provider so runOne reaches it, and author the explicit runtime `context:` a real
// external-verb step carries (the VerbCatalog default-context left core with the verb).
// We then drive an `adb: install` apk check whose CandyDirs is empty with a sentinel
// CandyScanErr set. WITH the origin stamped, resolveCheckApk reaches the CandyDirs-miss
// branch and reports the SCAN ERROR; WITHOUT it, c.Origin is empty and resolveCheckApk
// fails with "not a candy origin" instead. Asserting the sentinel appears in the step
// result proves the origin reached the Op.
func TestRunPlan_StampsStepOrigin(t *testing.T) {
	// Snapshot BEFORE any registration so the stub adb provider AND its served schema are
	// undone on cleanup — the ONE reset pattern, hermetic under `-count>1`.
	t.Cleanup(snapshotProviderState())
	// Register the stub external adb provider for the duration of this test (adb is no
	// longer a builtin, so the global registry slot is free).
	if err := providerRegistry.register(stubAdbExternalVerb{}, "test:stub-adb"); err != nil {
		t.Fatalf("register stub adb provider: %v", err)
	}
	// Post schema-compaction, dispatch validates plugin_input against the verb's
	// registered input def BEFORE invoking (validateAuthoredPluginInput) — serve a
	// permissive stub def for verb:adb the way the real plugin's Describe would
	// (it must declare the frozen `method` primary; the trailing `...` admits the
	// per-method modifiers like apk).
	if err := registerPluginUnitSchema("test:stub-adb", PluginSchema{
		CueSource: "#StubAdbInput: {method: string, ...}\n",
		InputDefs: map[string]string{"verb:adb": "#StubAdbInput"},
	}); err != nil {
		t.Fatalf("register stub adb schema: %v", err)
	}

	// resolveCheckApk errors before any subprocess; Box satisfies the image-context guard.
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive, Box: "android-emulator", CandyScanErr: errors.New("scan-sentinel-boom")})
	set := &LabelDescriptionSet{
		Candy: []LabeledDescription{{
			Origin:      "candy:github.com/owner/repo/candy/android-emulator-layer",
			Description: "android apps install",
			Plan: []spec.Step{{
				Op: Op{ID: "adb-install-apidemos", Plugin: "adb", PluginInput: map[string]any{"method": "install", "apk": "./tests/data/ApiDemos-debug.apk"}, Context: []string{"runtime"}},
			}},
		}},
	}
	res := RunPlan(context.Background(), r, set, nil, false)
	if len(res) != 1 {
		t.Fatalf("want 1 step result, got %d", len(res))
	}
	msg := res[0].Result.Message
	if !strings.Contains(msg, "scan-sentinel-boom") {
		t.Fatalf("step Origin was not stamped onto the dispatched Op — resolveCheckApk "+
			"did not reach the candy-keyed branch (got message: %q)", msg)
	}
}
