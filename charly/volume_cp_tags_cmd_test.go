package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestMatchImageGlob_FullRefAndLastSegment relocated to
// candy/plugin-clean/retention_test.go (K1-alpha core-minimization: matchImageGlob
// moved with the rest of the retention engine).

func TestSidecarContainerNameInstance_Shape(t *testing.T) {
	if got := spec.SidecarContainerNameInstance("selkies-labwc", "", "tailscale"); got != "charly-selkies-labwc-tailscale" {
		t.Errorf("base sidecar name = %q", got)
	}
	if got := spec.SidecarContainerNameInstance("selkies-labwc", "82.1.2.3", "tailscale"); got != "charly-selkies-labwc-82.1.2.3-tailscale" {
		t.Errorf("instance sidecar name = %q", got)
	}
}
