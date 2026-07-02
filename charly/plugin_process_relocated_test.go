package main

import (
	"context"
	"testing"
)

// TestRelocatedProcessVerb_DispatchesViaKit proves the `process` check verb — relocated
// to candy/plugin-process (a compiled-in kit candy) — dispatches through the SAME
// providerRegistry path as an typed builtin verb: it resolves as a CheckVerbProvider
// (the kitVerbAdapter), which passes the live *Runner as a kit.CheckContext and runs the
// relocated pgrep logic against the executor. Deterministic via fakeExecutor (no live
// process/pgrep), exercising both the found (pass) and absent (fail) paths — proving the
// dispatch + adapter + relocated logic end to end.
func TestRelocatedProcessVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("process")
	if !ok {
		t.Fatal("process verb not registered — compiled-in kit candy (candy/plugin-process) failed to register")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("process provider is not a CheckVerbProvider: %T", prov)
	}

	// pgrep finds the process (exit 0) + running:true → pass.
	feFound := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "pgrep", exit: 0}}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: feFound, Mode: RunModeLive},
		&Op{PluginInput: map[string]any{"process": "sleep", "running": true}})
	if res.Status != TestPass {
		t.Fatalf("found + running:true: want pass, got %v: %s", res.Status, res.Message)
	}

	// pgrep does not find it (exit 1) + running:true → fail.
	feAbsent := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "pgrep", exit: 1}}}
	res2 := cv.RunVerb(context.Background(), &Runner{Exec: feAbsent, Mode: RunModeLive},
		&Op{PluginInput: map[string]any{"process": "absent", "running": true}})
	if res2.Status != TestFail {
		t.Fatalf("absent + running:true: want fail, got %v: %s", res2.Status, res2.Message)
	}
}
