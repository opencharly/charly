package main

import (
	"context"
	"strings"
	"testing"
)

// TestRelocatedFileVerb_DispatchesViaKit proves the MULTI-ROLE `file` verb — relocated to
// candy/plugin-file (a compiled-in kit candy) — resolves as BOTH a CheckVerbProvider AND
// a ProvisionActor. CHECK: stat probe via the executor + attribute asserts. ACT: render
// the mkdir/touch + chmod file-creation.
func TestRelocatedFileVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("file")
	if !ok {
		t.Fatal("file verb not registered — compiled-in kit candy (candy/plugin-file) failed")
	}

	// CHECK role: stat probe reports a regular file 0644 root:root; exists+mode+owner match → pass.
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("file provider is not a CheckVerbProvider: %T", prov)
	}
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: fe, Mode: RunModeLive},
		&Op{PluginInput: map[string]any{"file": "/etc/hostname", "exists": true, "mode": "644", "owner": "root", "filetype": "file"}})
	if res.Status != TestPass {
		t.Fatalf("check: want pass, got %v: %s", res.Status, res.Message)
	}

	// ACT role: a content-bearing file act renders an mkdir + cat-heredoc + chmod.
	pa, ok := prov.(ProvisionActor)
	if !ok {
		t.Fatalf("file provider does not implement ProvisionActor (multi-role adapter missing): %T", prov)
	}
	script, ok := pa.RenderProvisionScript(
		&Op{PluginInput: map[string]any{"file": "/etc/motd", "mode": "644"}, Content: "hello"}, nil)
	if !ok || !strings.Contains(script, "/etc/motd") || !strings.Contains(script, "chmod") {
		t.Fatalf("act: want a file-creation script, got ok=%v %q", ok, script)
	}
}
