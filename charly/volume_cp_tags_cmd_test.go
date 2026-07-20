package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

func TestMatchImageGlob_FullRefAndLastSegment(t *testing.T) {
	ref := "ghcr.io/opencharly/charly-fedora-2026-abc:2026.160.0100"
	cases := []struct {
		glob string
		want bool
	}{
		{"charly-fedora-2*", true},                     // last-segment glob (the documented cache-invalidation form)
		{"ghcr.io/opencharly/charly-fedora-2*", true},  // full-ref glob
		{"charly-fedora-2026-abc:2026.160.0100", true}, // exact last segment
		{"charly-debian-*", false},                     // different box
		{"*selkies*", false},                           // path.Match: '*' does not cross unmatched text boundaries here
	}
	for _, c := range cases {
		if got := matchImageGlob(c.glob, ref); got != c.want {
			t.Errorf("matchImageGlob(%q, %q) = %v, want %v", c.glob, ref, got, c.want)
		}
	}
}

func TestSidecarContainerNameInstance_Shape(t *testing.T) {
	if got := kit.SidecarContainerNameInstance("selkies-labwc", "", "tailscale"); got != "charly-selkies-labwc-tailscale" {
		t.Errorf("base sidecar name = %q", got)
	}
	if got := kit.SidecarContainerNameInstance("selkies-labwc", "82.1.2.3", "tailscale"); got != "charly-selkies-labwc-82.1.2.3-tailscale" {
		t.Errorf("instance sidecar name = %q", got)
	}
}
