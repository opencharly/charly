package main

import (
	"context"
	"strings"
	"testing"
)

// TestRelocatedServiceVerb_DispatchesViaKit proves the THREE-role `service` verb —
// relocated to candy/plugin-service (a compiled-in kit candy) — resolves through the
// providerRegistry as a CheckVerbProvider AND a ProvisionActor AND a TypedStepProvider
// (the kitVerbActStepAdapter). CHECK: supervisorctl/systemctl probe via the executor.
// ACT: the enable shell. STEP: materialize into a ServicePackagedStep.
func TestRelocatedServiceVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("service")
	if !ok {
		t.Fatal("service verb not registered — compiled-in kit candy (candy/plugin-service) failed")
	}

	// CHECK role: supervisorctl status reports RUNNING (exit 0); running:true → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("service provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "supervisorctl status", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: fe, Mode: RunModeLive},
		&Op{PluginInput: map[string]any{"service": "nginx", "running": true}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: render the enable shell.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("service provider does not implement ProvisionActor: %T", prov)
	}
	script, ok := pa.RenderProvisionScript(&Op{PluginInput: map[string]any{"service": "nginx"}}, nil)
	if !ok || !strings.Contains(script, "systemctl enable") || !strings.Contains(script, "supervisorctl") {
		t.Fatalf("act: want an enable shell, got ok=%v %q", ok, script)
	}

	// STEP role: lower into a ServicePackagedStep with the right unit/enable/candy.
	sp, ok := prov.(TypedStepProvider)
	if !ok {
		t.Fatalf("service provider does not implement TypedStepProvider (multi-role step adapter missing): %T", prov)
	}
	if sp.LowersTo() != StepKindServicePackaged {
		t.Fatalf("LowersTo = %v, want StepKindServicePackaged", sp.LowersTo())
	}
	step := sp.ConstructStep(&Op{PluginInput: map[string]any{"service": "nginx"}}, &Candy{Name: "mylayer"}, &ResolvedBox{})
	sps, ok := step.(*ServicePackagedStep)
	if !ok {
		t.Fatalf("ConstructStep returned %T, want *ServicePackagedStep", step)
	}
	if sps.Unit != "nginx" || !sps.Enable || sps.CandyName != "mylayer" {
		t.Fatalf("ServicePackagedStep = %+v, want Unit=nginx Enable=true CandyName=mylayer", sps)
	}
}
