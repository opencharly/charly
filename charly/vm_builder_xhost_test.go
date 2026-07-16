package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// D3: builder-image resolution order — override > compiled step, hard-error when
// none resolves (the dead resolver-fallback tier was removed in C5). builderStepImage
// is the venue-agnostic free helper shared by the VM target + the F3 build channel (R3).
func TestResolveBuilderImage(t *testing.T) {
	if img, _ := builderStepImage(&deploykit.BuilderStep{Builder: "npm", BuilderImage: "from-step"}, deploykit.EmitOpts{BuilderImageOverride: "from-override"}); img != "from-override" {
		t.Errorf("override should win, got %q", img)
	}
	if img, _ := builderStepImage(&deploykit.BuilderStep{Builder: "npm", BuilderImage: "from-step"}, deploykit.EmitOpts{}); img != "from-step" {
		t.Errorf("compiled step image should win, got %q", img)
	}
	if _, err := builderStepImage(&deploykit.BuilderStep{Builder: "npm", CandyName: "claude-code"}, deploykit.EmitOpts{}); err == nil {
		t.Error("no image resolvable → expected error")
	}
}

// D3: npm/pixi/cargo are routed to the cross-host home-artifact builder by
// OUTPUT SHAPE (no LocalPkg + a phase.install.host cell), not by builder name.
// Verified via the dry-run path so no podman is spawned. Builder defs come from
// the REAL build.yml so the routing exercises the config-driven host cells. After
// target:vm externalized, the VM builder leg runs over the host-engine reverse channel
// (RunHostStep → runVenueBuilderStep, the SAME venue-agnostic helper the former in-proc
// VM-target builder leg used — R3), so the test exercises runVenueBuilderStep directly.
func TestRunVenueBuilderStepRoutesHomeBuilders(t *testing.T) {
	_, bc, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	for _, b := range []string{"npm", "pixi", "cargo"} {
		s := &deploykit.BuilderStep{Builder: b, CandyName: "x", CandyDir: "/tmp/x", BuilderDef: bc.Builder[b], BuilderImage: "test-builder:latest"}
		if err := runVenueBuilderStep(context.Background(), &recordingExec{}, "", buildEngineContext{}, s, deploykit.EmitOpts{DryRun: true}); err != nil {
			t.Errorf("runVenueBuilderStep(%s) dry-run routed to home-artifact builder errored: %v", b, err)
		}
	}
}

// D3: a builder with no phase.install.host cell (no resolved BuilderDef) honors
// --skip-incompatible, and hard-errors otherwise pointing at the missing host
// cell. Routing is by output shape (no LocalPkg → home-artifact path; no host
// cell there → unsupported), not a hardcoded builder-name list.
func TestRunVenueBuilderStepUnknown(t *testing.T) {
	s := &deploykit.BuilderStep{Builder: "bogus", CandyName: "x"}

	if err := runVenueBuilderStep(context.Background(), &recordingExec{}, "", buildEngineContext{}, s, deploykit.EmitOpts{SkipIncompatible: true}); err != nil {
		t.Errorf("unknown builder with --skip-incompatible should be skipped, got %v", err)
	}
	err := runVenueBuilderStep(context.Background(), &recordingExec{}, "", buildEngineContext{}, s, deploykit.EmitOpts{})
	if err == nil || !strings.Contains(err.Error(), "phase.install.host") {
		t.Errorf("unknown builder without skip should error pointing at the missing host cell, got %v", err)
	}
}
