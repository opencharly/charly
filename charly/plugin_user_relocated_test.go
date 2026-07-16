package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedUserVerb_DispatchesViaKit proves the MULTI-ROLE `user` verb — relocated to
// candy/plugin-user (a compiled-in kit candy) — resolves as BOTH a CheckVerbProvider (the
// kitVerbActAdapter) AND a ProvisionActor. CHECK: getent passwd via the executor + compare.
// ACT: render `id || useradd`.
func TestRelocatedUserVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("user")
	if !ok {
		t.Fatal("user verb not registered — compiled-in kit candy (candy/plugin-user) failed")
	}

	// CHECK role: getent passwd line; uid:0 matches → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("user provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "getent passwd", stdout: "root:x:0:0:root:/root:/bin/bash\n", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"user": "root", "uid": 0}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: render an idempotent useradd with the given uid/home/shell.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("user provider does not implement ProvisionActor (multi-role adapter missing): %T", prov)
	}
	script, ok := pa.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"user": "svc", "uid": 1500, "home": "/home/svc", "shell": "/bin/sh"}}, nil)
	if !ok || !strings.Contains(script, "useradd") || !strings.Contains(script, "svc") {
		t.Fatalf("act: want a useradd script, got ok=%v %q", ok, script)
	}
}
