package main

import (
	"context"
	"strings"
	"testing"
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
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "rpm -q", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: fe, Mode: RunModeLive, Distros: []string{"fedora"}},
		&Op{PluginInput: map[string]any{"package": "bash", "installed": true}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: render the install shell.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("package provider does not implement ProvisionActor: %T", prov)
	}
	script, ok := pa.RenderProvisionScript(&Op{PluginInput: map[string]any{"package": "bash"}}, []string{"fedora"})
	if !ok || !strings.Contains(script, "dnf install") || !strings.Contains(script, "pacman -S") {
		t.Fatalf("act: want an install shell, got ok=%v %q", ok, script)
	}

	// STEP role: lower into a SystemPackagesStep with the image format + cross-distro name.
	sp, ok := prov.(TypedStepProvider)
	if !ok {
		t.Fatalf("package provider does not implement TypedStepProvider: %T", prov)
	}
	if sp.LowersTo() != StepKindSystemPackages {
		t.Fatalf("LowersTo = %v, want StepKindSystemPackages", sp.LowersTo())
	}
	op := &Op{PluginInput: map[string]any{"package": "openssh", "package_map": map[string]any{"fedora": "openssh-server"}}}
	step := sp.ConstructStep(op, &Candy{Name: "net"}, &ResolvedBox{Pkg: "rpm", Tags: []string{"fedora:43", "fedora"}})
	sps, ok := step.(*SystemPackagesStep)
	if !ok {
		t.Fatalf("ConstructStep returned %T, want *SystemPackagesStep", step)
	}
	if sps.Format != "rpm" || sps.Phase != PhaseInstall || len(sps.Packages) != 1 || sps.Packages[0] != "openssh-server" {
		t.Fatalf("SystemPackagesStep = %+v, want Format=rpm Phase=Install Packages=[openssh-server] (cross-distro map applied)", sps)
	}
}
