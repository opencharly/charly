package main

// reverse_ops.go — execute ReverseOp slices recorded by the local deploy target
// at install time, turning them into concrete teardown commands.
//
// Each InstallStep's Reverse() method records a list of ReverseOps
// when the step runs (see deploy_host_helpers.go). `charly bundle del`
// reads those ops from the candy ledger and hands them here for
// execution. The ops are opaque to the ledger — only the teardown
// logic in this file understands each Kind.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// ReverseExecutor is the interface ReverseOp handlers expect. Allows
// us to pass either BundleDelCmd (for real teardown) or a test mock.
//
// reverseRunner returns the shell-runner used to execute reversal
// commands. When non-nil, handlers dispatch through it (so VM teardown
// runs commands over SSH instead of locally); when nil, handlers fall
// back to local `exec.Command` — the long-standing host-teardown path.
type ReverseExecutor interface {
	reverseDryRun() bool
	reverseKeepRepoChanges() bool
	reverseKeepServices() bool
	reverseRunner() ReverseRunner
}

// ReverseRunner executes reversal shell scripts at the requested
// privilege level. Implemented by both the local-exec host runner and
// the SSH-based VM runner (sshReverseRunner, bundle_add_cmd_vm.go).
type ReverseRunner interface {
	// RunSystem runs a bash script as root (wraps with sudo on host
	// runners; uses `ssh sudo bash -s` on VM runners).
	RunSystem(script string) error
	// RunUser runs a bash script as the deploy user (no sudo).
	RunUser(script string) error
}

// BundleDelCmd satisfies ReverseExecutor via thin wrappers — keeps
// the flag-accessor protocol decoupled from the concrete command type.
func (c *BundleDelCmd) reverseDryRun() bool          { return c.DryRun }
func (c *BundleDelCmd) reverseKeepRepoChanges() bool { return c.KeepRepoChanges }
func (c *BundleDelCmd) reverseKeepServices() bool    { return c.KeepServices }
func (c *BundleDelCmd) reverseRunner() ReverseRunner { return c.Runner }

// runReverseOps executes ops in REVERSE order (last-installed, first-
// removed). Idempotent where possible: a missing file is treated as
// "already removed" rather than an error.
func runReverseOps(ops []ReverseOp, exec ReverseExecutor) {
	for _, op := range slices.Backward(ops) {
		if err := runReverseOp(op, exec); err != nil {
			// Keep going: a partial teardown is better than giving up
			// mid-way with half the candy removed. Log to stderr and
			// continue.
			fmt.Fprintf(os.Stderr, "reverse: %s failed: %v\n", op.Kind, err)
		}
	}
}

// runReverseOp dispatches on Kind and runs the appropriate command.
func runReverseOp(op ReverseOp, re ReverseExecutor) error {
	switch op.Kind {
	case ReverseOpPackageRemove:
		return reversePackageRemove(op, re)
	case ReverseOpPixiEnvRemove:
		return reversePixiEnvRemove(op, re)
	case ReverseOpCargoUninstall:
		return reverseCargoUninstall(op, re)
	case ReverseOpNpmUninstallG:
		return reverseNpmUninstallG(op, re)
	case ReverseOpRmFileSystem:
		return reverseRmFileSystem(op, re)
	case ReverseOpRmFileUser:
		return reverseRmFileUser(op, re)
	case ReverseOpRmDirRecursive:
		return reverseRmDir(op, re)
	case ReverseOpServiceDisable:
		return reverseServiceDisable(op, re)
	case ReverseOpServiceRemove:
		return reverseServiceRemove(op, re)
	case ReverseOpRemoveDropin:
		return reverseRemoveDropin(op, re)
	case ReverseOpRestoreEnabled:
		return reverseRestoreEnabled(op, re)
	case ReverseOpRemoveManaged:
		return reverseRemoveManaged(op, re)
	case ReverseOpRemoveEnvdFile:
		return reverseRemoveEnvdFile(op, re)
	case ReverseOpRemoveRepoFile:
		return reverseRemoveRepoFile(op, re)
	case ReverseOpCoprDisable:
		return reverseCoprDisable(op, re)
	case ReverseOpPluginScript:
		return reversePluginScript(op, re)
	}
	return fmt.Errorf("runReverseOp: unknown kind %q", op.Kind)
}

// reversePluginScript runs the verbatim shell script an external (out-of-process)
// deploy/step/builder plugin recorded as its teardown. The script body lives in
// Extra[ReverseOpPluginScriptKey]; Scope picks the privilege — ScopeSystem runs
// it as root (sudo on host / `ssh sudo` on a VM runner), anything else as the
// deploy user. It routes through the SAME runScriptReverse / runUserShellReverse
// the config-rendered package-uninstall command uses (R3), so dry-run + the
// ReverseRunner dispatch (local vs SSH) are honored identically. An empty script
// is a no-op (nothing config-sanctioned to run).
func reversePluginScript(op ReverseOp, re ReverseExecutor) error {
	script := strings.TrimSpace(op.Extra[spec.ReverseOpPluginScriptKey])
	if script == "" {
		return nil
	}
	if op.Scope == ScopeUser {
		return runUserShellReverse(script, re)
	}
	return runScriptReverse(script, re)
}

// ---------------------------------------------------------------------------
// Per-kind implementations.
// ---------------------------------------------------------------------------

func reversePackageRemove(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	// The removal command is rendered from the format's uninstall_template at
	// record time (fillReverseUninstallCmds) and persisted in the op — no
	// hardcoded per-format switch here. An empty command means the format
	// declares no host uninstall_template, OR the op predates this field; either
	// way there is nothing config-sanctioned to run.
	cmd := strings.TrimSpace(op.UninstallCmd)
	if cmd == "" {
		return fmt.Errorf("reversePackageRemove: no uninstall command for format %q (format declares no uninstall_template?)", op.Format)
	}
	return runScriptReverse(cmd, re)
}

// fillReverseUninstallCmds renders the host-venue uninstall command for every
// ReverseOpPackageRemove op in the slice from the format's uninstall_template
// (the embedded build vocabulary, charly/charly.yml), in place. Called at install/record time by the local deploy target and
// the external vm deploy (R3 — one shared filler) when the DistroConfig is in hand, so
// the persisted ledger op carries the exact removal command the teardown will
// run. Ops whose format declares no uninstall_template, or whose format isn't in
// the config, are left with an empty UninstallCmd (teardown then errors loudly
// rather than silently running a wrong command).
func fillReverseUninstallCmds(ops []ReverseOp, distroCfg *DistroConfig) {
	if distroCfg == nil {
		return
	}
	for i := range ops {
		if ops[i].Kind != ReverseOpPackageRemove || ops[i].UninstallCmd != "" {
			continue
		}
		fd := distroCfg.FindFormat(ops[i].Format)
		if fd == nil || strings.TrimSpace(fd.UninstallTemplate) == "" {
			continue
		}
		ctx := &InstallContext{Packages: append([]string(nil), ops[i].Targets...)}
		rendered, err := RenderTemplate(ops[i].Format+"-uninstall", fd.UninstallTemplate, ctx)
		if err != nil {
			continue
		}
		ops[i].UninstallCmd = strings.TrimSpace(rendered)
	}
}

func reversePixiEnvRemove(op ReverseOp, re ReverseExecutor) error {
	for _, envName := range op.Targets {
		path := filepath.Join(os.Getenv("HOME"), ".pixi", "envs", envName)
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -rf %s\n", path)
			continue
		}
		if runner := re.reverseRunner(); runner != nil {
			// Remote: $HOME resolves on the guest.
			if err := runner.RunUser(fmt.Sprintf("rm -rf %q", "$HOME/.pixi/envs/"+envName)); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func reverseCargoUninstall(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	argv := append([]string{"cargo", "uninstall"}, op.Targets...)
	if re.reverseDryRun() {
		fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
		return nil
	}
	return runUserShellReverse(strings.Join(argv, " "), re)
}

func reverseNpmUninstallG(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	argv := append([]string{"npm", "uninstall", "-g"}, op.Targets...)
	if re.reverseDryRun() {
		fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
		return nil
	}
	return runUserShellReverse(strings.Join(argv, " "), re)
}

func reverseRmFileSystem(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] sudo rm -f %s\n", path)
			continue
		}
		if err := runSudoArgvReverse([]string{"rm", "-f", path}, re); err != nil {
			return err
		}
	}
	return nil
}

func reverseRmFileUser(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
			continue
		}
		if runner := re.reverseRunner(); runner != nil {
			if err := runner.RunUser(fmt.Sprintf("rm -f %s", shellQuoteSimple(path))); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func reverseRmDir(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -rf %s\n", path)
			continue
		}
		if runner := re.reverseRunner(); runner != nil {
			if err := runner.RunUser(fmt.Sprintf("rm -rf %s", shellQuoteSimple(path))); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func reverseServiceDisable(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, unit := range op.Targets {
		argv := []string{"systemctl", "disable", "--now", unit}
		if op.Scope == ScopeUser {
			argv = []string{"systemctl", "--user", "disable", "--now", unit}
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
				continue
			}
			_ = runUserShellReverse(strings.Join(argv, " "), re)
			continue
		}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

func reverseServiceRemove(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, path := range op.Targets {
		if op.Scope == ScopeUser {
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
				continue
			}
			if runner := re.reverseRunner(); runner != nil {
				_ = runner.RunUser(fmt.Sprintf("rm -f %s", shellQuoteSimple(path)))
				continue
			}
			_ = os.Remove(path)
			continue
		}
		_ = runSudoArgvReverse([]string{"rm", "-f", path}, re)
	}
	return nil
}

func reverseRemoveDropin(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, path := range op.Targets {
		if op.Scope == ScopeUser {
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
				continue
			}
			if runner := re.reverseRunner(); runner != nil {
				_ = runner.RunUser(fmt.Sprintf("rm -f %s && rmdir --ignore-fail-on-non-empty %s",
					shellQuoteSimple(path), shellQuoteSimple(filepath.Dir(path))))
				continue
			}
			_ = os.Remove(path)
			// Also try to remove the now-empty .d parent directory.
			_ = os.Remove(filepath.Dir(path))
			continue
		}
		_ = runSudoArgvReverse([]string{"rm", "-f", path}, re)
		_ = runSudoArgvReverse([]string{"rmdir", "--ignore-fail-on-non-empty", filepath.Dir(path)}, re)
	}
	return nil
}

func reverseRestoreEnabled(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	// If the unit was enabled before charly touched it, re-enable after
	// disable. The disable in reverseServiceDisable will have stopped
	// it; we re-enable (and restart) here so the user's prior state is
	// preserved.
	for _, unit := range op.Targets {
		argv := []string{"systemctl", "enable", "--now", unit}
		if op.Scope == ScopeUser {
			argv = []string{"systemctl", "--user", "enable", "--now", unit}
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
				continue
			}
			_ = runUserShellReverse(strings.Join(argv, " "), re)
			continue
		}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

// reverseRemoveManaged strips the candy's per-candy managed BLOCK (a `shell_snippet:`
// appended to an existing rc file the user may also own) from each target on teardown,
// keyed on Extra["marker"] (the per-candy fence tag) so unrelated content and other
// candies' blocks are left intact. Local venue: the in-proc read-strip-write
// (RemoveManagedBlockAt). Remote venue (SSH/VM): a rendered in-place strip script over
// the shell-only reverse runner, since it cannot read-modify-write a file in Go.
func reverseRemoveManaged(op ReverseOp, re ReverseExecutor) error {
	marker := op.Extra["marker"]
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] strip managed block %q from %s\n", marker, path)
			continue
		}
		if runner := re.reverseRunner(); runner != nil {
			if err := runner.RunUser(renderManagedBlockStrip(path, marker)); err != nil {
				return err
			}
			continue
		}
		if err := RemoveManagedBlockAt(path, marker); err != nil {
			return err
		}
	}
	return nil
}

// renderManagedBlockStrip is the remote analogue of RemoveManagedBlockAt: a POSIX-sh
// snippet that drops the marker's begin/end fence pair (and its body) from the file IN
// PLACE on the venue. The fences come from markersForTag so they match exactly what the
// forward walk wrote, and awk index() matches them LITERALLY (no regex). The final
// `cat > "$f"` rewrites in place to preserve the rc file's perms/owner.
func renderManagedBlockStrip(path, marker string) string {
	begin, end := markersForTag(marker)
	return fmt.Sprintf(`f=%s
[ -f "$f" ] || exit 0
t="$f.charly-strip.$$"
awk -v b=%s -v e=%s 'index($0,b){s=1;next} s&&index($0,e){s=0;next} !s{print}' "$f" > "$t" && cat "$t" > "$f" && rm -f "$t"`,
		shellQuoteSimple(path), shellQuoteSimple(begin), shellQuoteSimple(end))
}

func reverseRemoveEnvdFile(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
			continue
		}
		if runner := re.reverseRunner(); runner != nil {
			_ = runner.RunUser(fmt.Sprintf("rm -f %s", shellQuoteSimple(path)))
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

func reverseRemoveRepoFile(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepRepoChanges() {
		return nil
	}
	for _, path := range op.Targets {
		if err := runSudoArgvReverse([]string{"rm", "-f", path}, re); err != nil {
			return err
		}
	}
	return nil
}

func reverseCoprDisable(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepRepoChanges() {
		return nil
	}
	for _, copr := range op.Targets {
		argv := []string{"dnf", "-y", "copr", "disable", copr}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

// runSudoArgvReverse runs a sudo argv on the reverse (teardown) side. Accepts
// a possibly DEBIAN_FRONTEND-prefixed argv (we strip the prefix and
// set it as env instead).
//
// Dispatch: when re.reverseRunner() is non-nil (set by the VM target's
// Del, potentially others in the future), delegates the command to the
// runner so it executes in the right context (remote VM over SSH,
// etc.). Otherwise falls back to local `sudo <argv>`.
func runSudoArgvReverse(argv []string, re ReverseExecutor) error {
	if re.reverseDryRun() {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo "+strings.Join(argv, " "))
		return nil
	}
	envPrefix := []string{}
	for len(argv) > 0 && strings.Contains(argv[0], "=") {
		envPrefix = append(envPrefix, argv[0])
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return nil
	}
	if runner := re.reverseRunner(); runner != nil {
		// Build a bash -lc compatible script. Env prefixes are rendered
		// as `VAR=value VAR2=value2 cmd …` so the runner's bash sees them
		// as command-scoped env.
		var parts []string
		parts = append(parts, envPrefix...)
		for _, a := range argv {
			parts = append(parts, shellQuoteSimple(a))
		}
		return runner.RunSystem(strings.Join(parts, " "))
	}
	fullArgv := append([]string{}, argv...)
	if len(envPrefix) > 0 {
		fullArgv = append([]string{"env"}, envPrefix...)
		fullArgv = append(fullArgv, argv...)
	}
	cmd := exec.Command("sudo", fullArgv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// shellQuoteSimple single-quotes a token for inclusion in a shell
// command line. `sudo pacman -Rs foo` is trivial; but package names
// can contain dashes/plus/dots which benefit from quoting anyway.
func shellQuoteSimple(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runScriptReverse runs a bash snippet as root. Dispatches through the
// ReverseRunner when present (VM teardown over SSH wraps with sudo); otherwise
// executes locally via `sudo bash -lc <script>`. Used for the config-rendered
// package-uninstall command, which is a full shell command line (it may carry an
// env prefix like DEBIAN_FRONTEND=…) that bash parses directly.
func runScriptReverse(script string, re ReverseExecutor) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if re.reverseDryRun() {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo bash -lc "+shellQuoteSimple(script))
		return nil
	}
	if runner := re.reverseRunner(); runner != nil {
		return runner.RunSystem(script)
	}
	cmd := exec.Command("sudo", "bash", "-lc", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runUserShellReverse runs a bash snippet as the deploy user (no sudo).
// Dispatches through the ReverseRunner when present (VM teardown over
// SSH); otherwise executes locally as the current process user.
func runUserShellReverse(script string, re ReverseExecutor) error {
	if re.reverseDryRun() {
		fmt.Fprintln(os.Stderr, "[dry-run] "+script)
		return nil
	}
	if runner := re.reverseRunner(); runner != nil {
		return runner.RunUser(script)
	}
	cmd := exec.Command("bash", "-lc", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
