package main

import (
	"errors"
	"testing"
)

// build_overlay_baseref_test.go — the two arms of overlayBaseRef (charly#968).
//
// The tagged arm is proven end to end by TestHostBuildOverlay_NamespacedBoxNamesTheBuiltImage.
// The untagged arm cannot be driven end to end in a unit test because production resolves the
// newest local CalVer through the container engine; its DECISION is proven here with an injected
// resolver, and its identity (which name the resolver is asked about) comes from the SAME
// resolveBoxImageRegistryName helper the tagged arm's test exercises.

// TestOverlayBaseRef_UntaggedAsksTheRepoName proves the local-CalVer resolver is asked about the
// build's REPOSITORY name (`mybox`), never the namespace-qualified box ADDRESS (`ns.mybox`) — the
// address matches no local repo, so asking about it would refuse (or elect the wrong image).
func TestOverlayBaseRef_UntaggedAsksTheRepoName(t *testing.T) {
	var asked []string
	got, err := overlayBaseRef("ghcr.io/example", "mybox", "", func(short string) (string, error) {
		asked = append(asked, short)
		return "ghcr.io/example/mybox:2026.307.2144", nil
	})
	if err != nil {
		t.Fatalf("overlayBaseRef: %v", err)
	}
	if len(asked) != 1 || asked[0] != "mybox" {
		t.Fatalf("the local resolver was asked about %v, want exactly [\"mybox\"] (the build's repository name, not the box address)", asked)
	}
	if want := "ghcr.io/example/mybox:2026.307.2144"; got != want {
		t.Fatalf("overlayBaseRef = %q, want %q", got, want)
	}
}

// TestOverlayBaseRef_UntaggedRefusalPropagates proves the untagged arm still refuses a bare
// repository name the resolver cannot resolve instead of returning it as a success (charly#867).
func TestOverlayBaseRef_UntaggedRefusalPropagates(t *testing.T) {
	refusal := errors.New("image not local")
	got, err := overlayBaseRef("ghcr.io/example", "mybox", "", func(string) (string, error) {
		return "", refusal
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("overlayBaseRef = (%q, %v), want the resolver's refusal", got, err)
	}
}

// TestOverlayBaseRef_TaggedUsesTheBuildNamingRule proves the tagged arm composes registry + repo
// + tag through the build path's own helper, for a namespaced box's resolved identity.
func TestOverlayBaseRef_TaggedUsesTheBuildNamingRule(t *testing.T) {
	got, err := overlayBaseRef("ghcr.io/example", "mybox", "bed-2026.307.2144", nil)
	if err != nil {
		t.Fatalf("overlayBaseRef: %v", err)
	}
	if want := "ghcr.io/example/mybox:bed-2026.307.2144"; got != want {
		t.Fatalf("overlayBaseRef = %q, want %q", got, want)
	}
}
