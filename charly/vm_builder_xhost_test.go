package main

import (
	"testing"
)

// TestRunVenueBuilderStepRoutesHomeBuilders_LoaderShape is the CHARLY-LOADER half of the D3
// routing property (#55 final-tail split-by-assertion round, team-lead directive 2026-08-03):
// npm/pixi/cargo are routed to the cross-host home-artifact builder by OUTPUT SHAPE (no LocalPkg
// + a phase.install.host cell), not by builder name — this test proves the REAL committed
// build.yml actually produces that shape for all three builders (LoadBuildConfigForBox is
// charly's own project loader, a genuine core dependency with no sdk equivalent). The ROUTING
// decision itself (given a BuilderDef with this shape, deploykit.RunVenueBuilderStep dispatches
// to the home-artifact builder) is proven directly, with a literal fixture, by
// sdk/deploykit/venue_builder_test.go's TestRunVenueBuilderStepRoutesHomeBuilders_LiteralFixture
// — the two tests are complementary (config-parsing vs mechanism-routing), not duplicates, and
// together restore the original single test's full coverage with zero sdk import here.
func TestRunVenueBuilderStepRoutesHomeBuilders_LoaderShape(t *testing.T) {
	_, bc, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	for _, b := range []string{"npm", "pixi", "cargo"} {
		def := bc.Builder[b]
		if def == nil {
			t.Fatalf("builder %q missing from build.yml", b)
		}
		// LocalPkg (the OTHER half of the routing condition, s.LocalPkg == nil) is not part of
		// the loaded BuilderDef at all — it comes from the candy's own `localpkg:` declaration,
		// compiled separately by the deploy-plan compiler; npm/pixi/cargo candies simply never
		// declare one, which is exactly why they take the home-artifact path.
		if def.Phases == nil || def.Phases.Install == nil || def.Phases.Install.Host == "" {
			t.Errorf("builder %q has no phase.install.host cell in build.yml — the home-artifact routing decision has nothing to dispatch on", b)
		}
	}
}
