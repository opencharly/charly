package main

import (
	"testing"
)

// TestCapabilityLabelCompleteness verifies every exported field on
// BoxMetadata (aliased as Capabilities) has a CapabilityLabelMap entry.
// Adding a new capability field without a label mapping is a build break —
// enforces the Part G invariant "every capability lives in an OCI label" so
// that `charly bundle from-box` (Part F.10) can reconstruct the full contract
// from a pushed image.
func TestCapabilityLabelCompleteness(t *testing.T) {
	if err := checkCapabilityLabelCompleteness(); err != nil {
		t.Fatal(err)
	}
}

// TestCapabilitiesIsImageMetadataAlias asserts the type alias is zero-cost —
// existing BoxMetadata consumers see the same struct under the
// Capabilities name.
func TestCapabilitiesIsImageMetadataAlias(t *testing.T) {
	var c Capabilities
	c.Box = "test"
	var m BoxMetadata = c //nolint:staticcheck // explicit type asserts Capabilities == BoxMetadata alias
	if m.Box != "test" {
		t.Errorf("alias lost field value: %q", m.Box)
	}
}
