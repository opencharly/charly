package deploypod

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestPrepareVenueState_DecodesIntoTheHostsWireType is the regression witness for the third
// image-crossing defect: the overlay ref a PrepareVenue reply carries must actually SURVIVE the
// host's decode. The assertion is deliberately end-to-end over the wire contract — marshal here,
// unmarshal into the exact type the host uses (spec.SaveDeployStateInput, whose consumer is
// deploykit.applyDeployState) — because the pre-fix code produced perfectly valid JSON that
// decoded to an empty struct, so any test that stopped at "the blob is non-empty" would have
// passed while the overlay went unpersisted.
func TestPrepareVenueState_DecodesIntoTheHostsWireType(t *testing.T) {
	const (
		overlay = "check-stepkind-emit-pod-overlay:0fafbf994885"
		base    = "ghcr.io/opencharly/check-pod:2026.216.2119"
	)
	state := prepareVenueState(overlay, base)
	if len(state) == 0 {
		t.Fatal("prepareVenueState returned no state for a built overlay")
	}
	var in spec.SaveDeployStateInput
	if err := json.Unmarshal(state, &in); err != nil {
		t.Fatalf("host decode of the state blob: %v", err)
	}
	if in.ResolvedImage != overlay {
		t.Fatalf("decoded ResolvedImage = %q, want %q — the state blob does not use the wire key the host decodes (`resolved_image`), so the overlay ref is silently dropped and the deploy runs the BASE image", in.ResolvedImage, overlay)
	}

	// The pre-fix shape, kept as an executable statement of WHY: a hand-built map keyed on the Go
	// FIELD name marshals fine and decodes to nothing.
	handBuilt, err := json.Marshal(map[string]any{"ResolvedImage": overlay})
	if err != nil {
		t.Fatalf("marshal hand-built map: %v", err)
	}
	var stale spec.SaveDeployStateInput
	if err := json.Unmarshal(handBuilt, &stale); err != nil {
		t.Fatalf("decode hand-built map: %v", err)
	}
	if stale.ResolvedImage != "" {
		t.Fatalf("the Go-field-name key %s now decodes to %q; if the wire contract gained that alias this test's premise is stale — re-check prepareVenueState's doc comment", handBuilt, stale.ResolvedImage)
	}
}

// TestPrepareVenueState_NoOverlayWritesNothing: a deploy with no add_candy overlay resolves to the
// base image itself, and must persist NO resolved_image — otherwise the base ref would be pinned
// into the overlay slot and survive later rebuilds.
func TestPrepareVenueState_NoOverlayWritesNothing(t *testing.T) {
	const base = "ghcr.io/opencharly/check-pod:2026.216.2119"
	if got := prepareVenueState(base, base); got != nil {
		t.Fatalf("prepareVenueState(base, base) = %s, want nil", got)
	}
	if got := prepareVenueState("", base); got != nil {
		t.Fatalf("prepareVenueState(\"\", base) = %s, want nil", got)
	}
}
