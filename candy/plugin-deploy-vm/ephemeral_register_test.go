package deployvm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/opencharly/sdk"
)

// TestIsEphemeralPanicError is the regression test for the FINAL/K5 unit 6a RCA #5 finding #2:
// dispatchVmEphemeralRegister must distinguish a PANIC-CLASS error (sdk.EphemeralPanicMarker —
// candy/plugin-fleet's recoverEphemeralOpPanic converts a recovered panic into this marker) from
// an ORDINARY registration error (e.g. systemd-run missing), since only the former is fatal to the
// Add. A bed-caught nil-map panic inside persistEphemeralRuntime previously vanished silently —
// "a panicking registration must fail the add, not vanish." Ported verbatim from the deleted
// charly/host_build_ephemeral_register_test.go when the OpEphemeralRegister dispatch moved
// plugin-side (the teardown twin's mirror).
func TestIsEphemeralPanicError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"ordinary error — not fatal", errors.New("systemd-run not in PATH; TTL safety net disabled"), false},
		{"panic-marker error — fatal", fmt.Errorf("fleet ephemeral-register: %s panic: assignment to entry in nil map", sdk.EphemeralPanicMarker), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEphemeralPanicError(tc.err); got != tc.want {
				t.Errorf("isEphemeralPanicError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
