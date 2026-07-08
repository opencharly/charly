package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/opencharly/sdk/kit"
)

// DeployExecutor abstracts shell execution + file placement for deploy
// targets. the local deploy target uses ShellExecutor (spawns bash directly);
// the external vm deploy uses SSHExecutor (wraps scripts as `ssh vm sudo bash -s`,
// uses scp for file transfers). Nested topologies (container-in-vm,
// vm-in-container, host-in-vm-in-container, etc.) use NestedExecutor,
// which composes a parent DeployExecutor with a "shell jump" (podman
// exec / ssh / virsh) prepended to every primitive.
//
// The interface is narrow but carries one identity method — Venue() —
// that answers the question "where does bash actually run when I call
// RunSystem?". Ledger files live on that venue's filesystem, so the
// venue string is how install_ledger.go picks the right install
// database without a global constant.
type DeployExecutor interface {
	// Venue returns a stable identifier for where this executor's
	// commands physically run. Examples:
	//
	//   "local"                            — ShellExecutor.
	//   "ssh://arch@127.0.0.1:2224"        — SSHExecutor.
	//   "nested:podman exec stack/local"   — NestedExecutor over local.
	//   "nested:ssh vm/local"              — NestedExecutor over SSH.
	//
	// The string is used as a map key for per-venue ledgers, so it
	// must be stable across invocations for the same logical target.
	// Not a URL — don't parse it; just compare.
	Venue() string

	// RunSystem executes a bash script with root privileges. On the
	// host, this is `sudo bash -s <<<script`; on the VM target, it's
	// `ssh <user>@<host> sudo bash -s <<<script`. The script body runs
	// with set -e semantics at the caller's discretion.
	RunSystem(ctx context.Context, script string, opts EmitOpts) error

	// RunUser executes a bash script as the invoking user (no sudo).
	// On the host, it's `bash -s <<<script`; on VM, `ssh <user>@<host>
	// bash -s <<<script` where <user> is the unprivileged guest user.
	RunUser(ctx context.Context, script string, opts EmitOpts) error

	// RunBuilder invokes the multi-stage builder image (podman run
	// <builder>) to compile pixi/npm/cargo/aur artifacts. On the host
	// this calls the existing BuilderRun helper. On VM deploys, the
	// builder runs *on the host* and artifacts are scp'd into the
	// guest via PutFile — podman inside the guest is not required.
	RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error)

	// PutFile places a file at a remote path. ownerRoot == true means
	// the file is chown'd to root:root and chmod'd according to mode.
	// On the host, this is a plain os.WriteFile (plus sudo chown when
	// ownerRoot). On VM, this is scp into a tmp location followed by
	// `sudo install -m <mode> -o root -g root` on the guest.
	PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error

	// GetFile retrieves the contents of a file on the venue. asRoot==true
	// runs the read via sudo to handle paths the deploying user cannot
	// access (e.g. /etc/rancher/k3s/k3s.yaml on a k3s server). On the
	// host, this is os.ReadFile (or `sudo cat` when asRoot). On VM, this
	// is `ssh <host> sudo cat <path>` with stdout captured. On nested
	// executors, delegates through the jump via the parent's own RunSystem
	// semantics. Used by layer_artifacts.go to publish files back to the
	// operator after deploy completion.
	GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error)

	// RunCapture executes a single shell command (or short bash script) on
	// the venue and returns stdout/stderr/exit/err separately. Used by the
	// declarative test runner (testrun.go) to probe target state without
	// the streamed-output ergonomics of RunSystem/RunUser. No root
	// escalation — callers add `sudo` explicitly when needed; mirrors the
	// previous test-time Executor.Exec semantics. After the executor-
	// hierarchy cutover (2026-04), this is the single capture-output
	// method used by every probe across `charly check live`, `charly check box`, and
	// `charly check` scoring.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)

	// Kind returns a coarse classification of the venue used by the test
	// runner for reporting and skip decisions. Values:
	//   "host"      — ShellExecutor (operator's machine)
	//   "container" — NestedExecutor with JumpPodmanExec / JumpDockerExec
	//   "image"     — NestedExecutor with JumpPodmanRun / JumpDockerRun
	//                 (disposable container per invocation)
	//   "vm"        — SSHExecutor or NestedExecutor with JumpSSH/JumpVirshConsole
	// Replaces the test-time Executor.Kind() method deleted in the
	// 2026-04 executor-hierarchy cutover.
	Kind() string

	// ResolveHome returns the absolute path of $HOME for the named user
	// on the venue. Empty user means "the executor's default user" (the
	// invoking operator for ShellExecutor; the SSH login user for
	// SSHExecutor). Implementations consult `getent passwd` so they
	// don't depend on $HOME being set in the calling environment — that
	// matters for SSH executors where the operator's $HOME has nothing
	// to do with the remote user's home, and for ShellExecutor when the
	// caller wants a different user's home (e.g. running as root but
	// resolving an unprivileged user's home).
	//
	// Bundled as part of the 2026-05 shell:-schema cutover. Replaces the
	// `the local deploy target.HostHome = os.Getenv("HOME")` static-field
	// initialization that mis-targeted SSH deploys: the operator's
	// $HOME is not the remote user's home, so every shell-rc edit
	// (env.d sourcing block included) was landing in the wrong place
	// for `host: user@machine` deploys.
	ResolveHome(ctx context.Context, user string) (string, error)
}

// ShellExecutor implements DeployExecutor against the invoking user's shell
// + filesystem. Faithful behavior-preserving wrapper around the
// existing runSudoShell / runUserShell / BuilderRun helpers.
type ShellExecutor struct{}

// VenueLocal is the stable Venue() identifier for the local host.
// Exported so install_ledger.go and tests can reference it without
// hard-coding the literal.
const VenueLocal = "local"

// Venue returns the fixed "local" identifier — commands always run on
// the invoking user's host.
func (ShellExecutor) Venue() string { return VenueLocal }

// run is the shared body of RunSystem/RunUser: asRoot picks the sudo shell
// (runSudoShell) over the unprivileged one (runUserShell). The ctx is unused —
// ShellExecutor runs against the invoking user's own shell.
func (ShellExecutor) run(_ context.Context, script string, asRoot bool, opts EmitOpts) error {
	if asRoot {
		return runSudoShell(script, opts)
	}
	return runUserShell(script, opts)
}

// RunSystem delegates to the package-level runSudoShell.
func (s ShellExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	return s.run(ctx, script, true, opts)
}

// RunUser delegates to the package-level runUserShell.
func (s ShellExecutor) RunUser(ctx context.Context, script string, opts EmitOpts) error {
	return s.run(ctx, script, false, opts)
}

// RunBuilder delegates to the package-level BuilderRun.
func (ShellExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	return BuilderRun(ctx, opts)
}

// PutFile on the local executor is a direct filesystem write. When
// ownerRoot is set, the installer uses `sudo install -m <mode> -o root
// -g root` so the target path can be /usr/local/bin or similar.
func (ShellExecutor) PutFile(_ context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if ownerRoot {
		// Use sudo install for atomic, correct-permissions placement.
		// `install` creates target directory if missing (-D).
		script := "install -D -m " + permOctal(mode) + " -o root -g root " + deployShellQuote(localPath) + " " + deployShellQuote(remotePath)
		return runSudoShell(script, opts)
	}
	script := "install -D -m " + permOctal(mode) + " " + deployShellQuote(localPath) + " " + deployShellQuote(remotePath)
	return runUserShell(script, opts)
}

// GetFile on the local executor is a direct filesystem read. When
// asRoot is set, the read is delegated to `sudo cat` so files with
// restricted permissions (e.g. /etc/shadow, rancher kubeconfig) can
// still be retrieved. Stdout is captured verbatim.
func (ShellExecutor) GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error) {
	if opts.DryRun {
		return nil, nil
	}
	if !asRoot {
		return os.ReadFile(remotePath)
	}
	cmd := exec.CommandContext(ctx, "sudo", "cat", remotePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapReadErr(err, remotePath, stderr.String())
	}
	return stdout.Bytes(), nil
}

// RunCapture executes a shell command on the local host and returns
// captured stdout/stderr/exit. Mirrors the deleted ContainerExecutor /
// ImageExecutor / VmTestExecutor behaviour from the pre-cutover test-
// time interface — callers (testrun.go verbs) get the same return
// shape via the unified DeployExecutor interface.
func (ShellExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	bindProcessGroupKill(cmd)
	return runCaptureCmd(cmd)
}

// Kind reports "host" — ShellExecutor's commands run on the
// operator's machine.
func (ShellExecutor) Kind() string { return "host" }

// ResolveHome returns $HOME for `user` on the local host. Empty user
// resolves to the invoking operator's $HOME (matches today's
// `os.Getenv("HOME")` behaviour). Non-empty user goes through
// `getent passwd <user>` so callers can resolve any user's home.
func (ShellExecutor) ResolveHome(ctx context.Context, user string) (string, error) {
	if user == "" {
		if h := os.Getenv("HOME"); h != "" {
			return h, nil
		}
		// Last-ditch: ask getent for our own uid.
		user = os.Getenv("USER")
		if user == "" {
			return "", fmt.Errorf("ResolveHome: $HOME and $USER both empty")
		}
	}
	cmd := exec.CommandContext(ctx, "getent", "passwd", user)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("getent passwd %s: %w", user, err)
	}
	return parseGetentHome(stdout.String(), user)
}

// parseGetentHome extracts the home directory (field 6) from a getent
// passwd line. Shared between ShellExecutor and SSHExecutor.
func parseGetentHome(line, user string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("getent passwd %s: no entry found", user)
	}
	fields := strings.Split(line, ":")
	if len(fields) < 6 {
		return "", fmt.Errorf("getent passwd %s: malformed entry: %q", user, line)
	}
	home := fields[5]
	if home == "" {
		return "", fmt.Errorf("getent passwd %s: empty home field", user)
	}
	return home, nil
}

// runCaptureCmd is the shared output-capture helper. Identical behaviour
// to the pre-cutover testrun.go's runCapture (which lived on the now-
// deleted Executor interface): exit codes are NOT errors, only spawn
// failures are. Lives here so SSHExecutor / NestedExecutor implementations
// can share it without circular imports.
// signalKillErrMarker is the stable substring every runCaptureCmd caller stamps into a
// signal-kill error (see below). The check runner's probeWasKilled matches it to tell a
// probe that was KILLED before completing (infra interruption → re-attempt) from a probe
// that RAN and returned a failure (authoritative → never retried). One shared literal (R3).
const signalKillErrMarker = "terminated by signal"

// runCaptureWaitDelay bounds how long Cmd.Wait() blocks draining the stdout/stderr
// pipes AFTER the process has exited or its context deadline has fired. It is a hard
// upper bound on the pathological lingering-pipe case ONLY (see runCaptureCmd) — a
// normally-completing command never waits this long (WaitDelay is a MAXIMUM, not a
// minimum: fast commands return the instant their pipes hit EOF). Named + documented,
// not a tuned magic value: it is the OS-level guarantee that a check probe or deploy
// command ALWAYS returns instead of wedging the whole pass. A var (not const) only so
// tests can shrink it to exercise the double-fork-escape path without a 10s wall wait.
var runCaptureWaitDelay = 10 * time.Second

// bindProcessGroupKill hardens a CTX-BOUND command (exec.CommandContext ONLY) against
// the canonical CommandContext lingering-grandchild hang: a check `command:` probe execs
// `podman exec`, which forks conmon + the in-container process. At the per-probe deadline
// the DEFAULT cancel SIGKILLs only the DIRECT child (bash); the podman-exec descendants
// survive holding the stdout/stderr pipe open, and Cmd.Wait() then blocks FOREVER on the
// copy goroutine that never sees EOF (goroutine dump: os.(*File).Read on the pipe → the
// 40-min check-live wedge). Two-part fix: (1) run the child in its OWN process group and
// cancel by killing the WHOLE group, so the podman-exec descendants die with it (no orphan
// leak); (2) WaitDelay bounds the post-deadline pipe drain, so Wait ALWAYS returns even if
// a descendant double-forked out of the group.
//
// MUST be called ONLY on a cmd built with exec.CommandContext — Go rejects a non-nil
// cmd.Cancel on a plain exec.Command (`command with a non-nil Cancel was not created with
// CommandContext`), so the shared runCaptureCmd (which some callers reach with a ctx-less
// exec.Command) does NOT set these; each ctx-bound RunCapture calls this itself (R3).
func bindProcessGroupKill(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid = the whole process group (requires Setpgid above).
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = runCaptureWaitDelay
	}
}

func runCaptureCmd(cmd *exec.Cmd) (string, string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if asExitErrorDeploy(err, &ee) {
			// A NEGATIVE exit code means the process was TERMINATED BY A SIGNAL
			// (a ctx-deadline SIGKILL, the OOM-killer, an operator kill) — it never
			// ran to a real exit, so it is an EXECUTION ERROR, not a probe "exit code".
			// The old path returned (-1, nil), collapsing a KILL into a silent "exit -1"
			// that a check verb reported as an ordinary failure — masking a probe that
			// was starved/killed as a spurious check failure (the 2026-07 check-box
			// "exit -1" mystery). Surface the signal + ProcessState so it is diagnosable
			// AND so callers treat a killed probe as an error, never a completed result.
			if ee.ExitCode() < 0 {
				return stdout.String(), stderr.String(), ee.ExitCode(),
					fmt.Errorf("process %s (%s): %w", signalKillErrMarker, ee.String(), err)
			}
			return stdout.String(), stderr.String(), ee.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// asExitErrorDeploy unwraps to *exec.ExitError. Local copy of the helper
// in testrun.go to avoid an import cycle once the test-time Executor is
// removed.
func asExitErrorDeploy(err error, ee **exec.ExitError) bool {
	return errors.As(err, ee)
}

// wrapReadErr is a small wrap helper so every executor's GetFile returns
// a consistent error shape.
func wrapReadErr(err error, path, stderr string) error {
	if stderr != "" {
		return &readFileError{path: path, stderr: stderr, cause: err}
	}
	return &readFileError{path: path, cause: err}
}

type readFileError struct {
	path   string
	stderr string
	cause  error
}

func (e *readFileError) Error() string {
	msg := "read " + e.path + ": " + e.cause.Error()
	if e.stderr != "" {
		msg += " (stderr: " + e.stderr + ")"
	}
	return msg
}

// permOctal renders a uint32 mode as a 4-digit octal string suitable
// for the `install -m` flag.
func permOctal(mode uint32) string {
	return fmtOctal(mode)
}

func fmtOctal(mode uint32) string {
	if mode == 0 {
		return "0644"
	}
	// Render as 0NNN.
	hi := (mode >> 9) & 0o7
	mi := (mode >> 6) & 0o7
	lo := (mode >> 3) & 0o7
	vlo := mode & 0o7
	return string([]byte{
		'0',
		byte('0' + hi),
		byte('0' + mi),
		byte('0' + lo),
		byte('0' + vlo),
	})
}

// deployShellQuote wraps a string in single-quotes for safe embedding in a
// bash script. Handles embedded single quotes via the standard
// 'foo'\”bar' trick.
// (FU-13: folded onto kit.ShellQuote — the behaviourally identical POSIX single-quoter, proven by
// TestShellSingleQuoters_CanonicalPOSIX, that core already shares with the plugins/check path; the
// shell-single-quote transform now lives ONCE — R3.)
var deployShellQuote = kit.ShellQuote
