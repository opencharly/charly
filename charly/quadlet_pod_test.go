package main

import "testing"

func TestPodQuadletFilename(t *testing.T) {
	if got := podQuadletFilename("my-app"); got != "charly-my-app.pod" {
		t.Errorf("got %q, want charly-my-app.pod", got)
	}
}

func TestSidecarQuadletFilename(t *testing.T) {
	if got := sidecarQuadletFilename("my-app", "tailscale"); got != "charly-my-app-tailscale.container" {
		t.Errorf("got %q, want charly-my-app-tailscale.container", got)
	}
}
