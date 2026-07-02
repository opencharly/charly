package main

import (
	"context"
	"testing"
)

// TestRelocatedAddrVerb_DispatchesViaKit proves the `addr` check verb — relocated to
// candy/plugin-addr (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter passing the live *Runner as a
// kit.CheckContext) and runs the relocated reachability logic. Deterministic via the
// ModeBox nc path (fakeExecutor): nc exit 0 = reachable, exit 1 = not.
func TestRelocatedAddrVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("addr")
	if !ok {
		t.Fatal("addr verb not registered — compiled-in kit candy (candy/plugin-addr) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("addr provider is not a CheckVerbProvider: %T", prov)
	}

	// ModeBox, nc exit 0 (reachable) + reachable:true → pass.
	feUp := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "nc -z", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: feUp, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"addr": "127.0.0.1:22", "reachable": true}})
	if res.Status != TestPass {
		t.Fatalf("nc-up + reachable:true: want pass, got %v: %s", res.Status, res.Message)
	}

	// ModeBox, nc exit 1 (unreachable) + reachable:false → pass.
	feDown := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "nc -z", exit: 1}}}
	res2 := cv.RunVerb(context.Background(), &Runner{Exec: feDown, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"addr": "127.0.0.1:1", "reachable": false}})
	if res2.Status != TestPass {
		t.Fatalf("nc-down + reachable:false: want pass, got %v: %s", res2.Status, res2.Message)
	}
}
