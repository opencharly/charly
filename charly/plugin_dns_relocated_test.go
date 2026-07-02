package main

import (
	"context"
	"testing"
)

// TestRelocatedDNSVerb_DispatchesViaKit proves the `dns` check verb — relocated to
// candy/plugin-dns (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter passing the live *Runner as a
// kit.CheckContext) and runs the relocated resolution logic. Deterministic via the
// ModeBox getent path (fakeExecutor): exit 0 = resolvable, exit 2 = not.
func TestRelocatedDNSVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("dns")
	if !ok {
		t.Fatal("dns verb not registered — compiled-in kit candy (candy/plugin-dns) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("dns provider is not a CheckVerbProvider: %T", prov)
	}

	// ModeBox, getent exit 0 (resolvable) + resolvable:true → pass.
	feOk := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "getent hosts", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: feOk, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"dns": "localhost", "resolvable": true}})
	if res.Status != TestPass {
		t.Fatalf("getent-ok + resolvable:true: want pass, got %v: %s", res.Status, res.Message)
	}

	// ModeBox, getent exit 2 (not resolvable) + resolvable:false → pass.
	feNo := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "getent hosts", exit: 2}}}
	res2 := cv.RunVerb(context.Background(), &Runner{Exec: feNo, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"dns": "no.such.host.invalid", "resolvable": false}})
	if res2.Status != TestPass {
		t.Fatalf("getent-fail + resolvable:false: want pass, got %v: %s", res2.Status, res2.Message)
	}
}
