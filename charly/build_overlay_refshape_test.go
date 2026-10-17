package main

import "testing"

// TestIsQualifiedRef pins the distinction charly#867 turned on: a fully-qualified image ref
// (registry/repo path and/or tag) vs a bare box/short name.
//
// This is a UNIT test of the predicate, and it is deliberately honest about its reach: the
// predicate is what decides which arm `hostBuildOverlay`'s `default:` branch takes, and the
// behavioural proof that a bare name now PROPAGATES the resolution refusal — rather than
// letting an untaggable name reach the deploy-name alias tag and surface as
// `command exited 125` — is a live deploy-path run (the R10 bed), not this test.
//
// Cases are the real ones from the issue: `desk-schema` is the bare name that died, and
// `ghcr.io/opencharly/check-desktop:<calver>` is the fully-qualified ref that succeeded.
func TestIsQualifiedRef(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
		why  string
	}{
		// Bare box/short names: no registry, no repo path, no tag. These CANNOT be tagged
		// as-is — the local CalVer build is the only ref that names a real image for them.
		{"desk-schema", false, "the bare name from charly#867"},
		{"jupyter", false, "a bare box name"},
		{"check-desktop", false, "a bare name with a hyphen is still bare"},
		{"", false, "empty is not a ref at all"},

		// Fully-qualified refs: these need no local CalVer resolution and are already
		// taggable, so the resolution refusal is the expected answer for them.
		{"ghcr.io/opencharly/check-desktop:2026.289.1441", true, "the qualified ref that succeeded in charly#867"},
		{"ghcr.io/opencharly/jupyter:latest", true, "registry + repo + tag"},
		{"ghcr.io/opencharly/jupyter", true, "registry + repo, untagged"},
		{"localhost:5000/jupyter", true, "a registry with a port"},
		{"jupyter:latest", true, "a repo-less name WITH a tag is still taggable"},
	} {
		if got := isQualifiedRef(tc.ref); got != tc.want {
			t.Errorf("isQualifiedRef(%q) = %v, want %v (%s)", tc.ref, got, tc.want, tc.why)
		}
	}
}
