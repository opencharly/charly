package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedUnixGroupVerb_DispatchesViaKit proves the MULTI-ROLE `unix_group` verb —
// relocated to candy/plugin-unix-group (a compiled-in kit candy) — resolves as BOTH a
// CheckVerbProvider AND a ProvisionActor. CHECK: getent group via the executor + compare.
// ACT: render `getent group || groupadd`.
func TestRelocatedUnixGroupVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("unix_group")
	if !ok {
		t.Fatal("unix_group verb not registered — compiled-in kit candy (candy/plugin-unix-group) failed")
	}

	// CHECK role: getent group line; gid:0 matches → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("unix_group provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "getent group", stdout: "root:x:0:\n", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"unix_group": "root", "gid": 0}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: render an idempotent groupadd with the given gid.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("unix_group provider does not implement ProvisionActor (multi-role adapter missing): %T", prov)
	}
	script, ok := pa.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"unix_group": "svc", "gid": 1500}}, nil)
	if !ok || !strings.Contains(script, "groupadd") || !strings.Contains(script, "svc") {
		t.Fatalf("act: want a groupadd script, got ok=%v %q", ok, script)
	}
}
