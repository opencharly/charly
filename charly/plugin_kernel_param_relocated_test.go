package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedKernelParamVerb_DispatchesViaKit proves the MULTI-ROLE `kernel-param` verb
// — relocated to candy/plugin-kernel-param (a compiled-in kit candy) — resolves through
// the providerRegistry as BOTH a CheckVerbProvider (the kitVerbActAdapter) AND a
// ProvisionActor, exercising the new kit.ProvisionActor extension. CHECK: reads
// /proc/sys via the executor + matches. ACT: renders `sysctl -w`.
func TestRelocatedKernelParamVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("kernel-param")
	if !ok {
		t.Fatal("kernel-param verb not registered — compiled-in kit candy (candy/plugin-kernel-param) failed")
	}

	// CHECK role.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("kernel-param provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "/proc/sys", stdout: "Linux\n", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"kernel-param": "kernel.ostype", "value": []any{"Linux"}}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role — the new kit.ProvisionActor, exposed via the multi-role kitVerbActAdapter.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("kernel-param provider does not implement ProvisionActor (multi-role adapter missing): %T", prov)
	}
	script, ok := pa.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"kernel-param": "vm.swappiness", "value": []any{10}}}, nil)
	if !ok || !strings.Contains(script, "sysctl -w") || !strings.Contains(script, "swappiness") {
		t.Fatalf("act: want a sysctl -w script, got ok=%v %q", ok, script)
	}
}
