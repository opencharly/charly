package main

// Internal helpers for invoking child `charly` processes from within charly
// itself. Used by:
//
//   - UpdateCmd (commands.go) — dispatches to per-target update logic
//     by shelling out to charly box build / charly stop / charly config / charly start
//   - The unified-target Update/Rebuild methods (unified_targets_*.go)
//   - check_kind_cmd.go — orchestrates per-kind R10 sequences
//   - cycle.go — charly vm cycle / etc.
//
// These helpers are internal subprocess plumbing for the update path.
// Keeping them in their own file makes the ownership explicit (they're
// not part of any one verb's implementation) and lets the
// unified-target dispatch keep working through it.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// runCharlySubcommand shells out to `charly <args…>` in the current working
// directory, inheriting stdin/stdout/stderr. Uses the same charly binary
// the caller invoked (via os.Args[0]) so update loops pick up the
// local build-under-test automatically.
//
// A package var (not a plain func) so tests can stub the child-process
// boundary — e.g. deploy_nested_pod_test.go records the image-build /
// vm-cp-box calls deployNestedPodsInGuest makes without spawning charly.
var runCharlySubcommand = func(args ...string) error {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runCharlySubcommandCapture is like runCharlySubcommand but captures stderr
// into a buffer instead of mirroring it to os.Stderr. The caller
// decides whether the captured text is a real error (print it) or a
// benign signal (suppress). This keeps the update output clean when
// the child's "error" is actually just "already running" or similar.
//
// A package var (like runCharlySubcommand) so tests can stub the
// child-process boundary — e.g. the vm deploy lifecycle tests record the
// `charly vm start` call without spawning charly.
var runCharlySubcommandCapture = func(args ...string) (string, error) {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// captureCharlyStdout captures the child's stdout (instead of stderr).
// Sibling of runCharlySubcommandCapture; used when the caller needs to
// parse `charly vm list` / `charly status` table output.
func captureCharlyStdout(args ...string) (string, error) {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return buf.String(), err
}

// isBenignAlreadyRunning detects "already running" error text from
// the underlying VM backend. During an update, `charly vm create` may boot
// the VM as part of its libvirt-config-injection sequence (or, for
// QEMU-direct, may auto-start at the end of create); a subsequent
// `charly vm start` then fails. That's the end state we want — treat it
// as success.
//
// Two backend dialects to match:
//   - libvirt: "domain is already running" / "operation is not valid"
//   - qemu-direct: "Cannot lock pid file: Resource temporarily unavailable"
//     (the second qemu-system-x86_64 invocation can't acquire the same
//     pid-file lock the first one holds → effectively "already running")
func isBenignAlreadyRunning(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "already running") ||
		strings.Contains(s, "operation is not valid") ||
		strings.Contains(s, "cannot lock pid file")
}
