package main

// Pins the #Box CUE schema's `readiness:` field (added by the unified-readiness
// cutover so the `defaults.readiness:` block — a BoxConfig field — is modeled per
// the #Box completeness invariant). A valid readiness block is ACCEPTED; a typo'd
// sub-key (closedness) and a non-duration value (the #Duration regex) are
// REJECTED. Without the #Readiness schema addition the closed #Box rejects the
// `readiness` key outright, so the ACCEPT case fails — that is what makes this
// test fail in the absence of the change (R10 check-coverage gate).

import (
	"testing"

	"cuelang.org/go/cue"
)

func validateBoxEntityCUE(t *testing.T, yaml string) error {
	t.Helper()
	doc, err := cueDocFromYAML("test.yml", []byte(yaml))
	if err != nil {
		return err
	}
	return validateEntityCUE("box", "test.yml", doc.LookupPath(cue.ParsePath("box")))
}

func TestBoxCUESchema_Readiness(t *testing.T) {
	// A minimal concrete box sets `base:` (collapses the base/from disjunction) so
	// the readiness block is the ONLY variable across these cases.
	const head = "box:\n  name: testbox\n  base: fedora\n"

	// ACCEPT: a fully-populated, valid readiness block. FAILS without #Readiness.
	valid := head + "  readiness:\n" +
		"    poll_interval_local: 250ms\n" +
		"    poll_interval_remote: 3s\n" +
		"    poll_interval_heavy: 15s\n" +
		"    per_attempt: 2m\n" +
		"    no_progress: 90s\n" +
		"    absolute_cap: 30m\n" +
		"    stop_grace: 3m\n"
	if err := validateBoxEntityCUE(t, valid); err != nil {
		t.Fatalf("a valid readiness block must be ACCEPTED, got: %v", err)
	}

	// REJECT cases — the schema field has teeth.
	reject := []struct {
		name string
		yaml string
	}{
		{"typo'd readiness sub-key (closedness)", head + "  readiness:\n    no_progres: 90s\n"},
		{"non-duration value (#Duration regex)", head + "  readiness:\n    no_progress: ninety\n"},
		{"readiness is not a scalar", head + "  readiness: 5m\n"},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBoxEntityCUE(t, tc.yaml); err == nil {
				t.Errorf("expected CUE to REJECT %q, but it passed", tc.name)
			}
		})
	}
}
