package check

import (
	"strings"
	"testing"
)

// TestEnsureHarnessSandboxRunning_AbsentSurfacesRemediation proves the iterate-sandbox
// preflight guard converts a not-running sandbox into an actionable provisioning
// diagnostic — the fix for the scaffolding-selftest exit-125 (the bare "container does
// not exist" the downstream `podman exec` emits when the operator has not provisioned
// the per-host sandbox deploy). Without the guard the error would be the raw exec-125.
func TestEnsureHarnessSandboxRunning_AbsentSurfacesRemediation(t *testing.T) {
	orig := podContainerRunning
	t.Cleanup(func() { podContainerRunning = orig })

	podContainerRunning = func(string) bool { return false }
	err := ensureHarnessSandboxRunning("check-sandbox", "scaffolding-selftest")
	if err == nil {
		t.Fatal("absent sandbox must fail with a provisioning remediation, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"harness sandbox \"check-sandbox\" is not running",
		"charly bundle add check-sandbox <ref> --disposable",
		"charly start check-sandbox",
		"scaffolding-selftest",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("remediation missing %q; got:\n%s", want, msg)
		}
	}
}

// TestEnsureHarnessSandboxRunning_RunningProceeds confirms a running sandbox is not
// blocked — the guard is a fail-fast on the absent case only, never a false positive on
// a healthy operator-provisioned sandbox.
func TestEnsureHarnessSandboxRunning_RunningProceeds(t *testing.T) {
	orig := podContainerRunning
	t.Cleanup(func() { podContainerRunning = orig })

	podContainerRunning = func(container string) bool {
		if container != "charly-check-sandbox" {
			t.Errorf("guard probed the wrong container: %q", container)
		}
		return true
	}
	if err := ensureHarnessSandboxRunning("check-sandbox", "scaffolding-selftest"); err != nil {
		t.Fatalf("running sandbox must proceed, got: %v", err)
	}
}
