package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// A tagged registry image ref used AS a deploy name produces a charly.yml key with registry-host
// dots, which the loader rejects (dots are reserved for dotted-path addressing) — so config
// setup / start must fail FAST with guidance instead of writing a config that hard-fails on the
// next load. The guard is gated on BOTH a dot and an image `:tag`, so a github repo ref (pinned
// with @version) and a bare dotted-path address are untouched.
func TestRejectImageRefAsDeployName(t *testing.T) {
	reject := []string{
		"ghcr.io/opencharly/check-selkies-kde-pod:2026.185.0441",
		"docker.io/library/alpine:latest",
		"ghcr.io/org/img:v1",
	}
	for _, r := range reject {
		if err := deploykit.RejectImageRefAsDeployName(r); err == nil {
			t.Errorf("rejectImageRefAsDeployName(%q) = nil; want a fail-fast error", r)
		} else if !strings.Contains(err.Error(), "charly bundle add") {
			t.Errorf("rejectImageRefAsDeployName(%q) error missing the `bundle add` guidance: %v", r, err)
		}
	}

	allow := []string{
		"check-selkies-kde-pod",                   // short name
		"github.com/opencharly/charly/box/fedora", // github REPO ref — no :tag
		"github.com/opencharly/charly/box@v1",     // github ref pinned with @version
		"myapp:latest",                            // bare image:tag — no registry host, no invalid key
		"a.b.c",                                   // dotted-path deploy address — no / and no :tag
		"redis/prod",                              // Pattern A base/instance
	}
	for _, a := range allow {
		if err := deploykit.RejectImageRefAsDeployName(a); err != nil {
			t.Errorf("rejectImageRefAsDeployName(%q) = %v; want nil (not a tagged registry image ref)", a, err)
		}
	}
}
