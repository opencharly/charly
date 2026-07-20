package main

// Internal helpers for invoking child `charly` processes from within charly
// itself. Used by:
//
//   - podUpdateCmd (commands.go, the host-side reconstruction of the former UpdateCmd — now
//     command:update in candy/plugin-pod) — dispatches to per-target update logic by shelling
//     out to charly box build / charly stop / charly config / charly start
//   - The unified-target Update/Rebuild methods (unified_targets_*.go)
//   - check_kind_cmd.go — orchestrates per-kind R10 sequences
//   - cycle.go — charly vm cycle / etc.
//
// These helpers are internal subprocess plumbing for the update path.
// Keeping them in their own file makes the ownership explicit (they're
// not part of any one verb's implementation) and lets the
// unified-target dispatch keep working through it.

import (
	"os"
	"os/exec"
)

// runCharlySubcommand shells out to `charly <args…>` in the current working
// directory, inheriting stdin/stdout/stderr. Uses the same charly binary
// the caller invoked (via os.Args[0]) so update loops pick up the
// local build-under-test automatically.
//
// A package var (not a plain func) so tests can stub the child-process
// boundary — e.g. deploy_nested_pod_test.go records the image-build /
// vm-cp-box calls plugin-deploy-vm's PostApply makes without spawning charly.
var runCharlySubcommand = func(args ...string) error {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
