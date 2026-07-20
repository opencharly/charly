package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// TestRelocatedPackageVerb_DispatchesViaKit proves the THREE-role `package` verb —
// relocated to candy/plugin-package (a compiled-in kit candy) — resolves as a
// CheckVerbProvider AND a ProvisionActor AND a TypedStepProvider. CHECK: rpm/dpkg/pacman
// probe via the executor. ACT: the install shell. STEP: materialize into a
// SystemPackagesStep with the image format + cross-distro-resolved name.
func TestRelocatedPackageVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("package")
	if !ok {
		t.Fatal("package verb not registered — compiled-in kit candy (candy/plugin-package) failed")
	}

	// CHECK role: rpm -q reports installed (exit 0); installed:true → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("package provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "rpm -q", stdout: "INSTALLED", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive, "fedora"),
		&spec.Op{PluginInput: map[string]any{"package": "bash", "installed": true}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: render the install shell.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("package provider does not implement ProvisionActor: %T", prov)
	}
	script, ok := pa.RenderProvisionScript(&spec.Op{PluginInput: map[string]any{"package": "bash"}}, []string{"fedora"})
	if !ok || !strings.Contains(script, "dnf install") || !strings.Contains(script, "pacman -S") {
		t.Fatalf("act: want an install shell, got ok=%v %q", ok, script)
	}

	// STEP role: lower into a SystemPackagesStep with the image format + cross-distro name.
	sp, ok := prov.(TypedStepProvider)
	if !ok {
		t.Fatalf("package provider does not implement TypedStepProvider: %T", prov)
	}
	if sp.LowersTo() != spec.StepKindSystemPackages {
		t.Fatalf("LowersTo = %v, want StepKindSystemPackages", sp.LowersTo())
	}
	op := &spec.Op{PluginInput: map[string]any{"package": "openssh", "package_map": map[string]any{"fedora": "openssh-server"}}}
	step := sp.ConstructStep(op, &Candy{Name: "net"}, &buildkit.ResolvedBox{Pkg: "rpm", Tags: []string{"fedora:43", "fedora"}})
	sps, ok := step.(*deploykit.SystemPackagesStep)
	if !ok {
		t.Fatalf("ConstructStep returned %T, want *SystemPackagesStep", step)
	}
	if sps.Format != "rpm" || sps.Phase != spec.PhaseInstall || len(sps.Packages) != 1 || sps.Packages[0] != "openssh-server" {
		t.Fatalf("SystemPackagesStep = %+v, want Format=rpm Phase=Install Packages=[openssh-server] (cross-distro map applied)", sps)
	}
}

// TestPackageVerb_InfraFailureNotContentFalse proves the package verb
// distinguishes a genuine "not installed" (the probe ran, printed ABSENT) from
// an EXEC/INFRA failure (the podman exec died — store-write error exit 255,
// killed signal — so no INSTALLED/ABSENT token was printed). The infra failure
// must surface as such, NEVER as the false content verdict "installed=false,
// want true" (the check-{debian,jupyter-ml}-coder store-contention mislabel).
func TestPackageVerb_InfraFailureNotContentFalse(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("package")
	if !ok {
		t.Fatal("package verb not registered")
	}
	cv := prov.(CheckVerbProvider)
	run := func(fe *fakeExecutor, wantInstalled bool) CheckResult {
		return cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive, "arch"),
			&spec.Op{PluginInput: map[string]any{"package": "bash", "installed": wantInstalled}})
	}

	// Genuine absent: probe printed ABSENT, exit 0. installed:false → pass.
	if res := run(&fakeExecutor{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "ABSENT", exit: 0}}}, false); res.Status != TestPass {
		t.Fatalf("absent-as-expected: want pass, got %v: %s", res.Status, res.Message)
	}
	// Genuine absent but wanted installed → a real content FAIL (installed=false).
	if res := run(&fakeExecutor{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "ABSENT", exit: 0}}}, true); res.Status != TestFail || !strings.Contains(res.Message, "installed=false") {
		t.Fatalf("absent-but-wanted: want a content fail 'installed=false', got %v: %s", res.Status, res.Message)
	}
	// INFRA failure: exec died (exit 255, store error, no token). Must NOT be
	// reported as installed=false — surfaced as an exec/infra failure.
	res := run(&fakeExecutor{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "", exit: 255, stderr: "saving container state: writing container"}}}, true)
	if res.Status != TestFail {
		t.Fatalf("infra failure: want fail, got %v", res.Status)
	}
	if strings.Contains(res.Message, "installed=false") {
		t.Fatalf("infra failure MUST NOT be a false content verdict; got: %s", res.Message)
	}
	if !strings.Contains(res.Message, "exec/infra failure") {
		t.Fatalf("infra failure must be labeled as such; got: %s", res.Message)
	}
}
