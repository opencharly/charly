package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedProcessVerb_DispatchesViaKit proves the `process` check verb — relocated
// to candy/plugin-process (a compiled-in kit candy) — dispatches through the SAME
// providerRegistry path as an typed builtin verb: it resolves as a CheckVerbProvider
// (the kitVerbAdapter), which passes the live *Runner as a kit.CheckContext and runs the
// relocated pgrep logic against the executor. Deterministic via fakeExecutor (no live
// process/pgrep), exercising both the found (pass) and absent (fail) paths — proving the
// dispatch + adapter + relocated logic end to end.
func TestRelocatedProcessVerb_DispatchesViaKit(t *testing.T) {
	assertRelocatedVerbDispatch(t, "process", []relocatedVerbCase{
		// pgrep finds the process (exit 0) + running:true → pass.
		{"found + running:true", "pgrep", 0, RunModeLive,
			map[string]any{"process": "sleep", "running": true}, spec.StatusPass},
		// pgrep does not find it (exit 1) + running:true → fail.
		{"absent + running:true", "pgrep", 1, RunModeLive,
			map[string]any{"process": "absent", "running": true}, spec.StatusFail},
	})
}
