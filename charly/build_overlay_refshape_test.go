package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestIsQualifiedRef pins the distinction charly#867 turned on: a fully-qualified image ref
// (registry/repo path and/or tag) vs a bare box/short name.
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

// TestOverlayBaseRefFromResolution drives the DECISION hostBuildOverlay makes for an untagged
// overlay build, which is the branch charly#867 changed. It is the behavioural half that
// TestIsQualifiedRef cannot provide: every case here reaches the elector the overlay build
// actually calls, so a regression in the three-arm choice fails this test rather than only a
// predicate test.
//
// The case that matters most is `bare name, nil error, no ref`. Returning that pair unchanged
// yields an EMPTY OverlayBuildReply with a NIL error — which reads downstream as a successful
// build of nothing. That is the defect the validator found on PR #1099, and this case fails
// without the synthesized refusal.
func TestOverlayBaseRefFromResolution(t *testing.T) {
	refusal := fmt.Errorf("%w: %s", spec.ErrImageNotLocal, "desk-schema")

	t.Run("resolver elected a ref", func(t *testing.T) {
		got, err := overlayBaseRefFromResolution("jupyter", "ghcr.io/opencharly/jupyter:2026.289.1441", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "ghcr.io/opencharly/jupyter:2026.289.1441" {
			t.Fatalf("got %q, want the elected ref", got)
		}
	})

	t.Run("qualified ref uses itself when the resolver refuses", func(t *testing.T) {
		got, err := overlayBaseRefFromResolution("ghcr.io/opencharly/check-desktop:2026.289.1441", "", refusal)
		if err != nil {
			t.Fatalf("a qualified ref must not fail on a resolver refusal: %v", err)
		}
		if got != "ghcr.io/opencharly/check-desktop:2026.289.1441" {
			t.Fatalf("got %q, want the qualified base", got)
		}
	})

	t.Run("bare name propagates the resolver's refusal", func(t *testing.T) {
		got, err := overlayBaseRefFromResolution("desk-schema", "", refusal)
		if err == nil {
			t.Fatalf("a bare name the resolver refused must fail, got ref %q and no error", got)
		}
		if !errors.Is(err, spec.ErrImageNotLocal) {
			t.Fatalf("the resolver's own refusal must reach the caller, got %v", err)
		}
		if got != "" {
			t.Fatalf("got ref %q, want none", got)
		}
	})

	t.Run("bare name with no ref AND no error is a refusal, not a silent success", func(t *testing.T) {
		got, err := overlayBaseRefFromResolution("desk-schema", "", nil)
		if err == nil {
			t.Fatalf("(\"\", nil) from the resolver must not become a nil error: got ref %q, nil error — an empty build reported as a success", got)
		}
		if got != "" {
			t.Fatalf("got ref %q, want none", got)
		}
		if !strings.Contains(err.Error(), "desk-schema") {
			t.Fatalf("the refusal must name the image that failed, got %v", err)
		}
	})
}
