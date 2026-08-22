package container

import (
	"strings"
	"testing"
)

// TestResolveDeliverableRef_ExplicitLocalRefIsUsedAsAuthored — naming a tag IS the choice,
// so an explicit ref that exists locally must come back untouched rather than being
// re-elected to some newer build.
func TestResolveDeliverableRef_ExplicitLocalRefIsUsedAsAuthored(t *testing.T) {
	orig := LocalImageExists
	t.Cleanup(func() { LocalImageExists = orig })
	LocalImageExists = func(engine, ref string) bool { return ref == "localhost/pinned:v1" }

	got, err := ResolveDeliverableRef("podman", "localhost/pinned:v1")
	if err != nil {
		t.Fatalf("ResolveDeliverableRef: %v", err)
	}
	if got != "localhost/pinned:v1" {
		t.Fatalf("got %q, want the authored ref back unchanged", got)
	}
}

// TestResolveDeliverableRef_UnknownRefIsAnError — the failure must NAME the input and tell
// the operator how to produce it. A delivery verb that fails vaguely sends them looking at
// the venue instead of at their own build.
func TestResolveDeliverableRef_UnknownRefIsAnError(t *testing.T) {
	orig := LocalImageExists
	t.Cleanup(func() { LocalImageExists = orig })
	LocalImageExists = func(engine, ref string) bool { return false }

	_, err := ResolveDeliverableRef("podman", "no-such-box")
	if err == nil {
		t.Fatal("ResolveDeliverableRef returned nil for an unresolvable ref")
	}
	if !strings.Contains(err.Error(), "no-such-box") || !strings.Contains(err.Error(), "charly box build") {
		t.Fatalf("error %q names neither the input nor the remedy", err)
	}
}
