package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// noopImageSeams are the resolveImage/ensureImage closures for a test that never reaches the
// aur/LocalPkg branch (dry-run or a pre-branch error) — deploykit.RunVenueBuilderStep takes them
// as explicit parameters (relocated #118 coneB-p8bremainder; TestResolveBuilderImage and
// TestRunVenueBuilderStepUnknown, which needed no project-loader fixture, moved with it to
// sdk/deploykit/venue_builder_test.go).
func noopImageSeams() (func(string) (string, error), func(context.Context, string) error) {
	return func(string) (string, error) { return "", nil }, func(context.Context, string) error { return nil }
}

// D3: npm/pixi/cargo are routed to the cross-host home-artifact builder by
// OUTPUT SHAPE (no LocalPkg + a phase.install.host cell), not by builder name.
// Verified via the dry-run path so no podman is spawned. Builder defs come from
// the REAL build.yml so the routing exercises the config-driven host cells — this is the
// ONE genuine core (project-loader) dependency the ported deploykit test can't replicate, so
// this integration-style test STAYS in charly. After target:vm externalized, the VM builder leg
// runs over the host-engine reverse channel (RunHostStep → deploykit.RunVenueBuilderStep, the
// SAME venue-agnostic helper the former in-proc VM-target builder leg used — R3), so the test
// exercises deploykit.RunVenueBuilderStep directly, with the SAME injected-closure shape
// plugin_executor_reverse.go's RunHostStep uses.
func TestRunVenueBuilderStepRoutesHomeBuilders(t *testing.T) {
	_, bc, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	resolveImage, ensureImage := noopImageSeams()
	for _, b := range []string{"npm", "pixi", "cargo"} {
		s := &spec.BuilderStep{Builder: b, CandyName: "x", CandyDir: "/tmp/x", BuilderDef: bc.Builder[b], BuilderImage: "test-builder:latest"}
		if err := deploykit.RunVenueBuilderStep(context.Background(), &recordingExec{}, "", resolveImage, ensureImage, s, spec.EmitOpts{DryRun: true}); err != nil {
			t.Errorf("RunVenueBuilderStep(%s) dry-run routed to home-artifact builder errored: %v", b, err)
		}
	}
}
