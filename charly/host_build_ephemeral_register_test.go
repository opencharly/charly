package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/opencharly/sdk"
)

// TestIsEphemeralPanicError is the regression test for the FINAL/K5 unit 6a RCA #5 finding #2:
// registerEphemeralIfMarked must distinguish a PANIC-CLASS error (sdk.EphemeralPanicMarker —
// candy/plugin-bundle's recoverEphemeralOpPanic converts a recovered panic into this marker) from
// an ORDINARY registration error (e.g. systemd-run missing), since only the former is fatal to
// the Add. A bed-caught nil-map panic inside persistEphemeralRuntime previously vanished
// silently — "a panicking registration must fail the add, not vanish." Relocated verbatim from
// the deleted charly/deploy_add_shared_test.go (Cone A shape 3).
func TestIsEphemeralPanicError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"ordinary error — not fatal", errors.New("systemd-run not in PATH; TTL safety net disabled"), false},
		{"panic-marker error — fatal", fmt.Errorf("bundle ephemeral-register: %s panic: assignment to entry in nil map", sdk.EphemeralPanicMarker), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEphemeralPanicError(tc.err); got != tc.want {
				t.Errorf("isEphemeralPanicError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
