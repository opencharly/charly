package proc

import (
	"os"
	"os/exec"
)

// RunCharlySubcommand shells out to the CURRENT binary with args, inheriting
// stdin/stdout/stderr. Relocated from charly core (run_subcommand.go) as a
// stdlib-only host-exec leaf: the host-side update/deploy orchestration
// (podUpdateCmd, the unified-target Update/Rebuild methods, per-kind R10
// sequences, `charly vm cycle`, fleet member bring-up/teardown) spawns child
// `charly <args…>` invocations through it, and resolving the build-under-test
// means an update loop (or a member fork) picks up the local build automatically.
//
// The child binary resolves via os.Executable() — an ABSOLUTE, chdir-immune
// path to the running process's binary — falling back to os.Args[0] only on
// os.Executable()'s error (a genuinely rare OS-level failure, e.g. the exe was
// deleted mid-run on some platforms). os.Args[0] ALONE is wrong here: it is
// whatever string invoked the process — a bare `charly` (PATH-resolved,
// historically the only thing operators ran, so the bug stayed masked) or a
// RELATIVE path like `./bin/charly`. A relative os.Args[0] resolves against the
// process's CURRENT working directory at fork time, not the directory it had
// when os.Args[0] was captured — so a caller that os.Chdir()s (e.g. `charly -C
// box/fedora …`) before this var forks a child (e.g. fleet-member bring-up's
// `charly config <member>`) sends the child hunting for
// box/fedora/bin/charly, which doesn't exist (ENOENT). os.Executable() has no
// such relative-path hazard — it is resolved once, absolutely, at the OS level.
//
// A package var (not a plain func) so tests can stub the child-process boundary
// (e.g. record the image-build / vm-cp-box calls a deploy makes without actually
// spawning the binary).
var RunCharlySubcommand = func(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
