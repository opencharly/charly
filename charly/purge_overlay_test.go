package main

import "testing"

// TestPurgeDeployArtifacts_DropsOverlay proves `charly remove --purge` drops the synthesized
// <deploy-key>-overlay images (the leak fix). Every disposable pod check bed tears down via
// `charly remove --purge` (check_bed_run.go's default cleanup), so without this drop the add_candy:
// overlay images accumulate (dozens leaked before the fix). Stubs the engine-facing drop so no live
// container engine is needed; the removeVolumes/removeEncryptedVolumes side effects are best-effort
// no-ops against a fixture deploy name that matches nothing.
func TestPurgeDeployArtifacts_DropsOverlay(t *testing.T) {
	var called bool
	var gotEngine, gotRef string
	prev := dropOverlayImagesByRef
	dropOverlayImagesByRef = func(engine, ref string) { called, gotEngine, gotRef = true, engine, ref }
	t.Cleanup(func() { dropOverlayImagesByRef = prev })

	purgeDeployArtifacts("podman", "check-addcandy-pod-unit-fixture", "")
	if !called {
		t.Fatal("--purge must drop the <name>-overlay image (dropOverlayImagesByRef was never called)")
	}
	if gotEngine != "podman" || gotRef != "check-addcandy-pod-unit-fixture-overlay" {
		t.Fatalf("overlay drop targeted the wrong ref: engine=%q ref=%q "+
			"(want podman / check-addcandy-pod-unit-fixture-overlay)", gotEngine, gotRef)
	}

	// Instance form: the overlay ref carries the deploy KEY (base/instance), matching the
	// DeployName build_overlay stamps onto the overlay image.
	gotRef = ""
	purgeDeployArtifacts("podman", "selkies", "work")
	if gotRef != "selkies/work-overlay" {
		t.Fatalf("instance overlay ref = %q, want selkies/work-overlay", gotRef)
	}
}
