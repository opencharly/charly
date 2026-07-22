package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// build_overlay_test.go — unit coverage for the HOST-SIDE pod-overlay build ENGINE that STAYS core
// (M4). The pod LIFECYCLE (ArtifactKey/PostApply/Start/Stop/Status/Logs/Shell/Rebuild/PostTeardown)
// moved out to candy/plugin-deploy-pod and is exercised end-to-end by the check-pod R10 bed; only
// the overlay engine + its live-input carrier remain here.

// TestOverlayHostBuilderRegistered proves the overlay build goes through the uniform F10
// hostBuilders registry: the "overlay" kind (the pod-substrate sibling of "image"/"containerfiles"/
// "cli") is registered at package-var init. The externalized pod plugin's HostBuild("overlay")
// resolves it; a missing registration fails hard. The kind must be a generic action noun, not a
// provider WORD (the F11 uniform-API gate).
func TestOverlayHostBuilderRegistered(t *testing.T) {
	if _, ok := hostBuilderFor(overlayBuilderKind); !ok {
		t.Fatalf("the %q host-builder must be registered on the F10 hostBuilders registry", overlayBuilderKind)
	}
	if universe := buildProviderWordUniverse(); universe[overlayBuilderKind] {
		t.Fatalf("hostBuilders kind %q is a provider word — the F11 uniform-API gate forbids one on this surface", overlayBuilderKind)
	}
}

// TestCliHostBuilderRegistered proves the generic "cli" host-builder (M4) is registered and is a
// generic action noun (not a provider word) — the lifecycle plugins' Start/Stop/… legs resolve it.
func TestCliHostBuilderRegistered(t *testing.T) {
	if _, ok := hostBuilderFor(cliBuilderKind); !ok {
		t.Fatalf("the %q host-builder must be registered on the F10 hostBuilders registry", cliBuilderKind)
	}
	if universe := buildProviderWordUniverse(); universe[cliBuilderKind] {
		t.Fatalf("hostBuilders kind %q is a provider word — the F11 uniform-API gate forbids one", cliBuilderKind)
	}
}

// TestOverlayBuildInputsCtxRoundTrip proves the live-input carrier: the pod lifecycle proxy threads
// the compiled plans + the nested-venue ParentExec/ParentNode on the ctx (a live executor cannot
// ride the serializable []byte OverlayBuildRequest), and the host-builder reads them back unchanged.
func TestOverlayBuildInputsCtxRoundTrip(t *testing.T) {
	if got := overlayBuildInputsFrom(context.Background()); got != nil {
		t.Fatalf("overlayBuildInputsFrom on a bare ctx = %v, want nil", got)
	}
	plans := []*deploykit.InstallPlan{{Candy: "marker", AddCandies: []string{"marker"}}}
	node := &spec.BundleNode{Image: "base"}
	exec := kit.ShellExecutor{}
	ctx := withOverlayBuildInputs(context.Background(), &overlayBuildInputs{plans: plans, parentExec: exec, parentNode: node})
	got := overlayBuildInputsFrom(ctx)
	if got == nil {
		t.Fatal("overlayBuildInputsFrom returned nil after withOverlayBuildInputs")
	}
	if len(got.plans) != 1 || got.plans[0].Candy != "marker" {
		t.Errorf("plans not round-tripped: %v", got.plans)
	}
	if got.parentExec == nil {
		t.Error("parentExec not round-tripped")
	}
	if got.parentNode != node {
		t.Error("parentNode not round-tripped")
	}
}
