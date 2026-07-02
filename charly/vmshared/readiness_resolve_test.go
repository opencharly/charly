package vmshared

import (
	"strings"
	"testing"
)

// TestResolveReadiness_PluginEnvRoundTrips proves the FU-7 threading mechanism at its shared home:
// a RESOLVED readiness emitted as CHARLY_READINESS_* env (PluginEnv) and re-read through
// ResolveReadiness (the SAME path the out-of-process plugin runs in its spawned process) yields the
// identical bounds. That round-trip is how the plugin — which cannot LoadUnified to see the project's
// defaults.readiness — inherits the host-resolved readiness via its spawn env. Fails without the
// emitter (PluginEnv) or if a CHARLY_READINESS_* name diverges between READ (ResolveReadiness) and
// WRITE (PluginEnv) — the R3 reason both draw from the single readinessSpecs table.
func TestResolveReadiness_PluginEnvRoundTrips(t *testing.T) {
	want, err := ResolveReadiness(nil)
	if err != nil {
		t.Fatalf("ResolveReadiness(nil): %v", err)
	}

	env := want.PluginEnv()
	if len(env) != len(readinessSpecs) {
		t.Fatalf("PluginEnv: emitted %d entries, want %d (one per readiness field)", len(env), len(readinessSpecs))
	}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, "CHARLY_READINESS_") {
			t.Fatalf("malformed readiness env entry %q", kv)
		}
		t.Setenv(k, v)
	}

	// nil config → ResolveReadiness draws purely from the CHARLY_READINESS_* env we just set,
	// exactly as the out-of-process plugin's readinessResolve(nil) does in its spawned process.
	got, err := ResolveReadiness(nil)
	if err != nil {
		t.Fatalf("re-resolve from emitted env: %v", err)
	}
	if got != want {
		t.Fatalf("readiness did not round-trip through CHARLY_READINESS_* env:\n want %+v\n got  %+v", want, got)
	}
}
