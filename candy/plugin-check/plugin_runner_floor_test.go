package check

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// TestNewPluginCheckRunner_WiresReadinessFloor: relocated from charly's
// poll_probe_neverhang_test.go TestRunner_ProbeNeverHang_HonorsAuthorTimeout's trailing
// assertion (#55 decoupling cone, Batch D) — the SUBJECT under test is the CONSTRUCTION
// (newPluginCheckRunner sets ProbeTimeout from the readiness table when the caller leaves it
// zero), which moved plugin-side with the rest of the in-proc check-engine construction (the
// former charly/checkrun.go newCheckRunner). A runner built the production way gets the
// readiness per-attempt floor as its per-probe never-hang, not kit's own bare zero-value
// fallback (that kit-internal defensive const is never hit by this constructor, since it
// always sets ProbeTimeout from the readiness table first).
func TestNewPluginCheckRunner_WiresReadinessFloor(t *testing.T) {
	r := newPluginCheckRunner(nil, nil, spec.CheckEnv{}, kit.RunnerConfig{})
	if got := r.ProbeNeverHang(&spec.Op{}); got != vmshared.ReadinessPerAttemptFallback {
		t.Errorf("newPluginCheckRunner default: got %s, want readiness floor %s", got, vmshared.ReadinessPerAttemptFallback)
	}
}
