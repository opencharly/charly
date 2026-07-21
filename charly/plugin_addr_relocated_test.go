package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedAddrVerb_DispatchesViaKit proves the `addr` check verb — relocated to
// candy/plugin-addr (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter passing the live *Runner as a
// kit.CheckContext) and runs the relocated reachability logic. Deterministic via the
// ModeBox nc path (fakeExecutor): nc exit 0 = reachable, exit 1 = not.
func TestRelocatedAddrVerb_DispatchesViaKit(t *testing.T) {
	assertRelocatedVerbDispatch(t, "addr", []relocatedVerbCase{
		// ModeBox, nc exit 0 (reachable) + reachable:true → pass.
		{"nc-up + reachable:true", "nc -z", 0, RunModeBox,
			map[string]any{"addr": "127.0.0.1:22", "reachable": true}, spec.StatusPass},
		// ModeBox, nc exit 1 (unreachable) + reachable:false → pass.
		{"nc-down + reachable:false", "nc -z", 1, RunModeBox,
			map[string]any{"addr": "127.0.0.1:1", "reachable": false}, spec.StatusPass},
	})
}
