package preempt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/alecthomas/kong"
)

// command.go — the command:preempt leg. `charly preempt status`/`restore [claimant]` shells back
// through the hidden in-core `charly __preempt-status`/`__preempt-restore` verbs (the SAME
// internal-command pattern the command has always used); those hidden verbs run the arbiter via
// the in-core PROXY (charly/preempt.go), which dispatches to this plugin's verb:arbiter. So the
// operator CLI is byte-identical to before C9 — only the arbiter's HOME moved. No core symbol
// crosses the boundary; no ad-hoc podman/virsh.
//
// The shellback runs `charly __preempt-*` as a CHILD (exec.Command, stdio inherited) rather than
// syscall.Exec, so it works identically whether dispatched IN-PROC (compiled-in command Invoke)
// or OUT-OF-PROCESS (the cmd/serve fork/exec'd binary, CliMain). status/restore are
// non-interactive, so a child process is fine.

// runPreemptCLI parses the pass-through tokens against PreemptCmd and dispatches the selected
// leaf. Returns the process exit code (propagating the shelled-back charly's exit code).
func runPreemptCLI(args []string) int {
	var grp PreemptCmd
	parser, err := kong.New(&grp,
		kong.Name("preempt"),
		kong.Description("charly preempt — inspect and recover exclusive-resource preemption leases"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-preempt: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-preempt: parse `charly preempt %v`: %v\n", args, err)
		return 1
	}
	if err := kctx.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "charly preempt: %v\n", err)
		return 1
	}
	return 0
}

// charlyBin returns the host charly binary the dispatch seam stamped into CHARLY_BIN, falling
// back to `charly` on PATH.
func charlyBin() string {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b
	}
	return "charly"
}

// runCharly runs `charly <args…>` as a child with the operator's stdio inherited, so the hidden
// arbiter verb's table/messages reach the terminal natively. Returns the child's error (an
// *exec.ExitError carries its exit code).
func runCharly(args ...string) error {
	cmd := exec.Command(charlyBin(), args...) //nolint:gosec // charlyBin is CHARLY_BIN; args are fixed hidden-command tokens
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// PreemptCmd is the operator-facing surface over the in-core resource arbiter: inspect active
// exclusive-resource leases and recover holders left stopped by a crashed claim. The leaf
// structs are byte-identical to the former core command so `charly preempt …` parses as before.
type PreemptCmd struct {
	Status  PreemptStatusCmd  `cmd:"" help:"Show active resource-arbitration leases (which preemptible holders are stopped for which claimant) and flag stranded ones"`
	Restore PreemptRestoreCmd `cmd:"" help:"Restore preempted holders — no argument reconciles every stranded lease; a claimant argument force-releases that lease and restarts its holders"`
}

// PreemptStatusCmd prints the active leases via the `charly __preempt-status` shellback.
type PreemptStatusCmd struct{}

func (c *PreemptStatusCmd) Run() error { return runCharly("__preempt-status") }

// PreemptRestoreCmd recovers preempted holders via the `charly __preempt-restore` shellback.
type PreemptRestoreCmd struct {
	Claimant string `arg:"" optional:"" help:"Claimant whose lease to force-restore; omit to reconcile ALL stranded leases"`
}

func (c *PreemptRestoreCmd) Run() error {
	if c.Claimant == "" {
		return runCharly("__preempt-restore")
	}
	return runCharly("__preempt-restore", c.Claimant)
}
