package main

import (
	"context"
	"strings"
	"testing"
)

// TestRelocatedMountVerb_DispatchesViaKit proves the MULTI-ROLE `mount` verb — relocated
// to candy/plugin-mount (a compiled-in kit candy) — resolves as BOTH a CheckVerbProvider
// (the kitVerbActAdapter) AND a ProvisionActor. CHECK: findmnt via the executor + match.
// ACT: render `findmnt || mount`.
func TestRelocatedMountVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("mount")
	if !ok {
		t.Fatal("mount verb not registered — compiled-in kit candy (candy/plugin-mount) failed")
	}

	// CHECK role: findmnt prints SOURCE FSTYPE OPTIONS; filesystem:proc matches → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("mount provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "findmnt", stdout: "proc proc rw,nosuid\n", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: fe, Mode: RunModeLive},
		&Op{PluginInput: map[string]any{"mount": "/proc", "filesystem": "proc"}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: a mount_source yields an idempotent findmnt||mount script.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("mount provider does not implement ProvisionActor (multi-role adapter missing): %T", prov)
	}
	script, ok := pa.RenderProvisionScript(
		&Op{PluginInput: map[string]any{"mount": "/data", "mount_source": "/dev/sdb1", "filesystem": "ext4"}}, nil)
	if !ok || !strings.Contains(script, "mount") || !strings.Contains(script, "/dev/sdb1") {
		t.Fatalf("act: want a findmnt||mount script, got ok=%v %q", ok, script)
	}
}
