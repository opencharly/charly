package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedInterfaceVerb_DispatchesViaKit proves the `interface` check verb —
// relocated to candy/plugin-interface (a compiled-in kit candy) — dispatches through
// the providerRegistry as a CheckVerbProvider (the kitVerbAdapter passing the live
// *Runner as a kit.CheckContext) and runs the relocated `ip`-probe logic against the
// executor. Deterministic via fakeExecutor (present → pass, empty → fail).
func TestRelocatedInterfaceVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("interface")
	if !ok {
		t.Fatal("interface verb not registered — compiled-in kit candy (candy/plugin-interface) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("interface provider is not a CheckVerbProvider: %T", prov)
	}

	// present: non-empty `ip -o addr show` output, exit 0 → pass.
	fePresent := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "ip -o addr show", stdout: "1: lo    inet 127.0.0.1/8", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fePresent, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"interface": "lo"}})
	if res.Status != spec.StatusPass {
		t.Fatalf("present: want pass, got %v: %s", res.Status, res.Message)
	}

	// absent: empty output → fail.
	feAbsent := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "ip -o addr show", stdout: "", exit: 0}}}
	res2 := cv.RunVerb(context.Background(), hostVerbResolverFor(feAbsent, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"interface": "nonexistent"}})
	if res2.Status != spec.StatusFail {
		t.Fatalf("absent: want fail, got %v: %s", res2.Status, res2.Message)
	}
}
