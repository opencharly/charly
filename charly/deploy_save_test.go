package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// deploy_save_test.go — the WIRING half of the deploy-state persistence seam. The former
// SaveDeployState/RemoveVmDeployEntry-based tests here asserted the persistence SEMANTICS
// (Image/Target/Disposable field handling, no-clobber, abort-on-invalid, selective/idempotent
// removal) — those moved to candy/plugin-bundle/deploy_state_writer_test.go (the tests-move-with-
// subjects doctrine: the subject is deploykit.SaveDeployState/RemoveVmDeployEntry, plugin-side).
// What stays in core is the WIRING: dispatchDeployTarget("add") reaches the compiled-in
// command:bundle plugin's OpDeployDispatch → handleDeployApply → persistDeployState, which writes
// the deploy entry through the plugin's own deploykit.SaveDeployState. The seam helpers
// (deploy_dispatch_seam_test_helpers_test.go) + testLoadBundleConfig (charly's real LoadUnified)
// remain core test infrastructure.

// TestDeployDispatchReachesOpDeployDispatch proves the WIRING half: dispatchDeployTarget("add")
// reaches the command:bundle plugin's OpDeployDispatch and its deploy-state write path — the
// entry lands in deploy.yml through the plugin's own deploykit.SaveDeployState. The persistence
// SEMANTICS (which fields land, no-clobber, abort-on-invalid, selective/idempotent removal) are
// covered by candy/plugin-bundle's deploy_state_writer_test.go; this test only proves the
// dispatch reaches the plugin.
func TestDeployDispatchReachesOpDeployDispatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := testSeamProvider(t)
	testDispatchLifecycleAdd(t, p, "wiring-probe", spec.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "wiring-probe",
		Target:        "pod",
	})

	dc, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after dispatch: %v", err)
	}
	if _, ok := dc.Bundle["wiring-probe"]; !ok {
		t.Fatal("dispatchDeployTarget(add) did not reach OpDeployDispatch — no deploy entry written")
	}
}
