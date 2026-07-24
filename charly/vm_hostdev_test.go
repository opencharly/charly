package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestVmHostdevCountIsRuntimeOnly guards the validation contract: VM_HOSTDEV_COUNT
// resolves only against a live VM deployment, so a scope:"build" check must be
// barred from referencing it (validate_check.go enforces this via IsRuntimeOnlyVar).
// The VM_HOSTDEV_COUNT COMPUTATION itself (vmHostdevCount) moved plugin-side
// (candy/plugin-check/live_gather.go's pluginVmHostdevCount, K1-unblock wave arm 1) — its
// nil-safety contract is pinned by TestPluginVmHostdevCount there.
func TestVmHostdevCountIsRuntimeOnly(t *testing.T) {
	if !kit.IsRuntimeOnlyVar("VM_HOSTDEV_COUNT") {
		t.Error("VM_HOSTDEV_COUNT must be runtime-only so build-scope checks cannot reference it")
	}
}
