package exec

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/spec/spec"
)

// runSudoShell wraps a bash snippet in `sudo -n bash <<EOF`, feeding the body on stdin so it
// isn't exposed in the argv. Always `sudo -n` (non-interactive): a missing NOPASSWD policy
// fails FAST with "a password is required" instead of hanging.
func runSudoShell(script string, opts spec.EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo -n bash <<CHARLY_ROOT")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "CHARLY_ROOT")
		return nil
	}
	cmd := exec.Command("sudo", "-n", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runUserShell runs a script as the invoking user (no sudo). Used by the ShellExecutor's
// RunUser leg (deploy_executor.go).
func runUserShell(script string, opts spec.EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] bash <<CHARLY_USER")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "CHARLY_USER")
		return nil
	}
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
