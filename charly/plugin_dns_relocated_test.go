package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedDNSVerb_DispatchesViaKit proves the `dns` check verb — relocated to
// candy/plugin-dns (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter passing the live *Runner as a
// kit.CheckContext) and runs the relocated resolution logic. Deterministic via the
// ModeBox getent path (fakeExecutor): exit 0 = resolvable, exit 2 = not.
func TestRelocatedDNSVerb_DispatchesViaKit(t *testing.T) {
	assertRelocatedVerbDispatch(t, "dns", []relocatedVerbCase{
		// ModeBox, getent exit 0 (resolvable) + resolvable:true → pass.
		{"getent-ok + resolvable:true", "getent hosts", 0, RunModeBox,
			map[string]any{"dns": "localhost", "resolvable": true}, spec.StatusPass},
		// ModeBox, getent exit 2 (not resolvable) + resolvable:false → pass.
		{"getent-fail + resolvable:false", "getent hosts", 2, RunModeBox,
			map[string]any{"dns": "no.such.host.invalid", "resolvable": false}, spec.StatusPass},
	})
}
