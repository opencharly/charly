package main

import "testing"

// TestHostReverseExec_AccessorPassthrough covers the ReverseExecutor adapter the
// host-venue teardown (externalDeployTarget.Del → teardownHostDeploy) hands to
// runReverseOps. Moved here from the deleted unified_targets_local_test.go when the in-proc
// the local deploy target externalized (hostReverseExec + teardownHostDeploy survive in
// deploy_host_helpers.go; their end-to-end teardown is exercised live by the check-local
// bed's `charly bundle del`).
func TestHostReverseExec_AccessorPassthrough(t *testing.T) {
	e := &hostReverseExec{
		DryRun:          true,
		KeepRepoChanges: true,
		KeepServices:    false,
		Runner:          nil,
	}
	if !e.reverseDryRun() {
		t.Errorf("reverseDryRun = false, want true")
	}
	if !e.reverseKeepRepoChanges() {
		t.Errorf("reverseKeepRepoChanges = false, want true")
	}
	if e.reverseKeepServices() {
		t.Errorf("reverseKeepServices = true, want false")
	}
	if e.reverseRunner() != nil {
		t.Errorf("reverseRunner non-nil, want nil")
	}
}
