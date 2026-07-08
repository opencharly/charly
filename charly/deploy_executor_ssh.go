package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// SSHExecutor implements DeployExecutor against an SSH-reachable guest.
// Used by the external vm deploy to run the same InstallPlan IR that
// the local deploy target runs — but wrapped as `ssh <user>@<host> sudo
// bash -s` instead of direct local bash, and scp for file transfers.
//
// The builder-container path (VenueContainerBuilder steps for
// pixi/npm/cargo/aur) runs on the **host** (where podman is available),
// and the resulting artifacts are scp'd into the guest via PutFile.
// This keeps podman out of the guest's dependency surface.
//
// Credential-free by design: SSHExecutor contains zero key paths, zero
// host-key overrides, zero ssh-agent socket detection. ssh(1) reads
// ~/.ssh/config and ssh-agent for everything. VMs publish a managed
// Host stanza into ~/.config/charly/ssh_config (Included from ~/.ssh/config)
// that names the IdentityFile + UserKnownHostsFile + StrictHostKeyChecking
// per VM; charly vm create writes the stanza, charly vm destroy removes it.
type SSHExecutor struct {
	// User is the SSH login user. Optional — when empty, ssh(1) reads
	// the User directive from ~/.ssh/config or falls back to $USER.
	User string

	// Host is the SSH target — a hostname, an "[user@]host[:port]"
	// destination, or an ssh-config alias (e.g., the "charly-<vmname>"
	// stanzas managed by charly vm create). Required.
	Host string

	// Port is the SSH port. Optional — when 0, ssh uses the Port
	// directive from ~/.ssh/config or default 22.
	Port int

	// Args are extra ssh-cli arguments appended verbatim before the
	// destination. Pass-through from the deployment's `ssh_args:` field
	// (Ansible's ansible_ssh_extra_args). We do NOT parse, validate,
	// or interpret. Use sparingly — ssh-config is the right home for
	// persistent options.
	Args []string

	// ConnectTimeout caps the `-o ConnectTimeout=<N>` used in every
	// ssh invocation. Defaults to 10 seconds when zero.
	ConnectTimeout int
}

// Venue returns a stable "ssh://<user>@<host>:<port>" identifier so
// install_ledger.go can scope per-target ledgers without colliding
// with the local-shell ledger or other SSH targets. Components that
// are empty stringify naturally ("ssh://server" when User+Port unset).
func (e *SSHExecutor) Venue() string {
	switch {
	case e.User != "" && e.Port > 0:
		return fmt.Sprintf("ssh://%s@%s:%d", e.User, e.Host, e.Port)
	case e.User != "":
		return fmt.Sprintf("ssh://%s@%s", e.User, e.Host)
	case e.Port > 0:
		return fmt.Sprintf("ssh://%s:%d", e.Host, e.Port)
	default:
		return fmt.Sprintf("ssh://%s", e.Host)
	}
}

// run is the shared body of RunSystem/RunUser: it wraps the script as
// `ssh vm ['sudo'] bash -s` with the script fed on stdin. asRoot prepends `sudo`
// (root on the guest) and picks the CHARLY_ROOT dry-run heredoc label; !asRoot runs
// as the guest's unprivileged SSH-login user (CHARLY_USER label).
func (e *SSHExecutor) run(ctx context.Context, script string, asRoot bool, opts EmitOpts) error {
	if opts.DryRun {
		echo, label := "[dry-run] ssh vm bash -s <<CHARLY_USER", "CHARLY_USER"
		if asRoot {
			echo, label = "[dry-run] ssh vm sudo bash -s <<CHARLY_ROOT", "CHARLY_ROOT"
		}
		fmt.Fprintln(os.Stderr, echo)
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, label)
		return nil
	}
	args := e.sshBaseArgs()
	if asRoot {
		args = append(args, "sudo", "bash", "-s")
	} else {
		args = append(args, "bash", "-s")
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunSystem executes a bash script as root on the guest.
// Wraps as `ssh vm 'sudo bash -s'` with the script fed on stdin.
func (e *SSHExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	return e.run(ctx, script, true, opts)
}

// RunUser executes a bash script as the guest's unprivileged user
// (i.e. spec.SSH.User, the account SSHExecutor connects as).
func (e *SSHExecutor) RunUser(ctx context.Context, script string, opts EmitOpts) error {
	return e.run(ctx, script, false, opts)
}

// RunBuilder delegates to BuilderRun which runs the podman container
// *on the host*. Caller (the external vm deploy) is responsible for scp-ing
// the resulting artifacts into the guest via PutFile afterwards —
// this executor doesn't shuttle artifact trees itself.
func (e *SSHExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	return BuilderRun(ctx, opts)
}

// PutFile copies a local file into the guest via scp, then `install`s it into place
// with the correct mode/owner. This two-step dance is needed because scp runs as the
// guest's unprivileged user (you can't scp directly to /usr/local/bin).
//
// ownerRoot decides the PRIVILEGE the install runs at — the SAME contract as
// ShellExecutor.PutFile (R3): ownerRoot=true → `sudo install -o root -g root` (a
// system-scoped file, root-owned); ownerRoot=false → a plain user-scoped `install`
// run AS THE GUEST USER (RunUser, NO sudo) so the file AND any parent dirs `install
// -D` creates are USER-owned. Running the non-root branch under sudo (the old bug —
// it always RunSystem'd) created root-owned `~/.config/opencharly/{env.d,…}` in the
// guest, which then blocked the user-scoped ledger write (`mkdir … Permission
// denied`). The prior in-proc VM target wrote env.d via RunUser for exactly this
// reason; the externalized walk reaches the same RunUser path through here.
func (e *SSHExecutor) PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] scp %s vm:%s  (mode=%o, ownerRoot=%v)\n",
			localPath, remotePath, mode, ownerRoot)
		return nil
	}

	// Stage to a tmp path in the guest's home. The guest user always
	// has write access to its own /tmp/charly-staging/ directory.
	tmpName := "charly-staging-" + filepath.Base(remotePath) + "-" + strconv.FormatInt(randSeed(), 36)
	tmpRemote := "/tmp/" + tmpName

	// scp <local> <user>@<host>:<tmpRemote>
	scpArgs := e.scpBaseArgs()
	scpArgs = append(scpArgs, localPath, fmt.Sprintf("%s@%s:%s", e.User, e.Host, tmpRemote))
	scpCmd := exec.CommandContext(ctx, "scp", scpArgs...)
	scpCmd.Stderr = os.Stderr
	if err := scpCmd.Run(); err != nil {
		return fmt.Errorf("scp %s -> %s: %w", localPath, tmpRemote, err)
	}

	// ssh guest: move staged file into place with correct mode+owner, at the privilege the
	// ownerRoot contract dictates (sshInstallScript reports which).
	installScript, asRoot := sshInstallScript(tmpRemote, remotePath, fmtOctal(mode), ownerRoot)
	if asRoot {
		return e.RunSystem(ctx, installScript, opts)
	}
	return e.RunUser(ctx, installScript, opts)
}

// sshInstallScript builds the guest-side `install` script that moves a staged tmp file into
// place, and reports whether it MUST run as ROOT. It is the SSH analogue of
// ShellExecutor.PutFile's ownerRoot branch (R3): ownerRoot=true → `sudo install -o root -g
// root` (a system-scoped, root-owned file, run via RunSystem); ownerRoot=false → a plain
// `install` run AS THE GUEST USER (RunUser, NO sudo) so the file AND the parent dirs
// `install -D` creates are USER-owned. Split out of PutFile so the privilege mapping is
// unit-testable — it is the exact regression point (the user-scoped branch was previously run
// under sudo, creating root-owned ~/.config/opencharly in the guest and blocking the
// user-scoped ledger write).
func sshInstallScript(tmpRemote, remotePath, modeOctal string, ownerRoot bool) (script string, asRoot bool) {
	q := deployShellQuote
	if ownerRoot {
		return fmt.Sprintf("set -e\nsudo install -D -m %s -o root -g root %s %s\nrm -f %s\n",
			modeOctal, q(tmpRemote), q(remotePath), q(tmpRemote)), true
	}
	return fmt.Sprintf("set -e\ninstall -D -m %s %s %s\nrm -f %s\n",
		modeOctal, q(tmpRemote), q(remotePath), q(tmpRemote)), false
}

// GetFile retrieves the contents of a file from the guest via
// `ssh <host> [sudo] cat <path>` with stdout captured. asRoot==true
// wraps the read in sudo so restricted files (e.g. kubeconfig under
// /etc/rancher) are accessible from the unprivileged SSH user.
func (e *SSHExecutor) GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error) {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] ssh vm %scat %s\n",
			func() string {
				if asRoot {
					return "sudo "
				}
				return ""
			}(), remotePath)
		return nil, nil
	}
	args := e.sshBaseArgs()
	if asRoot {
		args = append(args, "sudo", "cat", remotePath)
	} else {
		args = append(args, "cat", remotePath)
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh cat %s: %w (stderr: %s)", remotePath, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RunCapture executes a script on the guest and returns captured
// stdout/stderr/exit. Mirrors the deleted VmTestExecutor.Exec semantics:
// no automatic root escalation (callers that need root prefix sudo).
func (e *SSHExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	bindProcessGroupKill(cmd)
	return runCaptureCmd(cmd)
}

// Kind reports "ssh" — SSHExecutor targets any host reachable over SSH
// (VMs via the managed charly-<name> aliases, remote machines via
// "[user@]host[:port]" or ssh-config aliases).
func (e *SSHExecutor) Kind() string { return "ssh" }

// ResolveHome returns $HOME for `user` on the SSH-reachable target.
// Empty user resolves to whoever ssh logged in as (via `echo $HOME`),
// non-empty user goes through `getent passwd <user>` so callers can
// resolve any user's home on the guest.
//
// This replaces the bug where `the local deploy target.HostHome` was
// initialized from `os.Getenv("HOME")` — the operator's home, not the
// guest user's. Any subsequent shell-rc edit (env.d sourcing block,
// new shell:-schema managed-block, etc.) now lands in the right place.
func (e *SSHExecutor) ResolveHome(ctx context.Context, user string) (string, error) {
	var script string
	if user == "" {
		// $HOME on the SSH-login user's session.
		script = "printf %s \"$HOME\""
	} else {
		// getent passwd <user> | cut -d: -f6
		// Fallback to ~user expansion if getent isn't available.
		script = `entry=$(getent passwd ` + shellSingleQuoteSSH(user) + ` 2>/dev/null) && printf %s "$(printf %s "$entry" | cut -d: -f6)" || check "printf %s ~` + shellSingleQuoteSSH(user) + `"`
	}
	// Feed the script over stdin to `bash -s` (the same transport
	// RunCapture/RunUser use). Passing it as a `bash -c <script>` remote
	// argv is broken: ssh space-joins all remote-command args into one
	// string and the guest shell re-splits on whitespace, so
	// `bash -c printf %s "$HOME"` runs bare `printf` ($0=printf, no format)
	// and exits 2 — which hard-aborted every VM deploy's guest-home
	// preflight. stdin preserves the script verbatim.
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("SSHExecutor.ResolveHome(%q): %w", user, err)
	}
	home := strings.TrimSpace(stdout.String())
	if home == "" {
		return "", fmt.Errorf("SSHExecutor.ResolveHome(%q): empty result", user)
	}
	return home, nil
}

// shellSingleQuoteSSH quotes `s` for safe inclusion in a bash -c script passed via ssh. FU-14
// folded it onto kit.ShellQuote — the shared POSIX single-quoter (behavioural equivalence proven by
// TestShellSingleQuoters_CanonicalPOSIX) — so the transform lives ONCE (R3).
var shellSingleQuoteSSH = kit.ShellQuote

// WaitForSSH polls the guest's sshd until it accepts connections
// (bounded by maxWaitSeconds). Returns nil on first successful
// connect, error on timeout. Used by the vm deploy's PrepareVenue right after
// `charly vm create`.
//
// The loop uses a wall-clock deadline (not an iteration count) and
// a 1-second sleep between attempts. The previous design (300
// iterations with no sleep) fast-failed in ~15 seconds when each
// SSH attempt errored quickly — connection-refused returns in
// ~50 ms, host-key-mismatch in ~20 ms — burning the entire
// "300-second budget" in a tiny window. With a 1 s sleep between
// attempts, slow-to-boot guests get the full polling window
// they were always supposed to receive. Fixed during the
// 2026-05-06 R10 follow-up.
func (e *SSHExecutor) WaitForSSH(ctx context.Context) error {
	// BINARY/EDGE readiness (sshd refused→up) → cap-only via the unified
	// pollUntil primitive (poll.go) at the GENEROUS config cap, replacing the
	// old fixed 120s magic deadline that was too short for a slow-boot guest
	// under heavy parallel load. ConnectTimeout=2 bounds each connect attempt;
	// ServerAliveInterval/CountMax bound a connected-then-blackholed session; the
	// per-attempt context (poll.go PerAttempt) is the final never-hang bound.
	return kit.WaitForSSH(ctx, e.kitSSHArgs(), e.remotePoll("ssh-ready"))
}

// kitSSHArgs projects this SSHExecutor onto the kit.SSHArgs host-surface coordinates (the WaitFor*
// helpers moved to sdk/kit so the externalized vm deploy plugin drives them itself; core delegates).
func (e *SSHExecutor) kitSSHArgs() kit.SSHArgs {
	return kit.SSHArgs{User: e.User, Host: e.Host, Port: e.Port, Args: e.Args, ConnectTimeout: e.ConnectTimeout}
}

// remotePoll builds the kit.PollFunc kit's WaitFor* drive: it wraps the host's readiness-configured
// pollUntil at the remote cap (kit is stdlib-only and cannot own the readiness/poll subsystem, so the
// poll policy is injected). kit supplies the ssh probe as the PollCond; core owns the bounds.
func (e *SSHExecutor) remotePoll(label string) kit.PollFunc {
	return func(ctx context.Context, cond kit.PollCond) error {
		cfg := loadedReadiness().WaitCapped(fmt.Sprintf("%s %s:%d", label, e.Host, e.Port), PollRemote, 0)
		return pollUntil(ctx, cfg, PollCondition(cond))
	}
}

// WaitForCloudInit polls `sudo cloud-init status` on the guest until cloud-init
// settles (status done/error/disabled). Only meaningful for cloud-image VMs;
// callers should skip this for bootc sources with no cidata ISO attached.
func (e *SSHExecutor) WaitForCloudInit(ctx context.Context) error {
	return kit.WaitForCloudInit(ctx, e.kitSSHArgs(), e.remotePoll("cloud-init"))
}

// WaitForPackageLock makes the guest package manager READY for a deploy plan's own
// `pacman -Sy` / `apt` / `dnf`: (1) it blocks until no boot-time package PROCESS is
// running (cloud-init's first-boot seed-package install holds the lock through
// cloud-final.service — the direct R4 sync primitive on the contended resource), then
// (2) it clears a STALE lock FILE left with no holding process. A boot-time pacman is
// reaped by systemd when cloud-final.service's cgroup is torn down, leaving an empty
// `/var/lib/pacman/db.lck` that no process owns; pacman then refuses ("unable to lock
// database") even though nothing is running. Removing a lock NO process holds is the
// standard, safe stale-lock recovery — gated on a re-check of no-process inside the
// command so it never races a live transaction. Process-based (not lock-file-based)
// for the WAIT so a stale lock can't hang the gate forever.
func (e *SSHExecutor) WaitForPackageLock(ctx context.Context) error {
	return kit.WaitForPackageLock(ctx, e.kitSSHArgs(), e.remotePoll("pkg-lock"))
}

// sshBaseArgs builds the common ssh invocation prefix. ssh(1) reads
// ~/.ssh/config + ssh-agent for keys, host-key checking, identity
// files, etc. We supply only the per-call ergonomics (LogLevel,
// ConnectTimeout) plus optional Port (when caller pre-parsed it from
// the destination string) plus the deployment's pass-through Args.
// The destination is "user@host" when User is set, otherwise just Host
// — letting ssh-config's User directive apply.
func (e *SSHExecutor) sshBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	args := []string{
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
	}
	if e.Port > 0 {
		args = append(args, "-p", strconv.Itoa(e.Port))
	}
	args = append(args, e.Args...)
	if e.User != "" {
		args = append(args, fmt.Sprintf("%s@%s", e.User, e.Host))
	} else {
		args = append(args, e.Host)
	}
	return args
}

// scpBaseArgs builds the scp-invocation prefix. scp reads ~/.ssh/config
// the same way ssh does. Note: scp's `-P` flag has uppercase semantics
// (vs ssh's lowercase `-p`); SSHArgs is pass-through, so callers
// targeting scp-specific options should put them in scp's expected form.
func (e *SSHExecutor) scpBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	args := []string{
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
	}
	if e.Port > 0 {
		args = append(args, "-P", strconv.Itoa(e.Port))
	}
	args = append(args, e.Args...)
	return args
}

// randSeed returns a fast-enough unique number for staging filename
// suffixes. Not cryptographically strong — it only needs to avoid
// collisions between concurrent PutFile calls for the same remotePath.
func randSeed() int64 {
	return int64(os.Getpid())<<16 | int64(os.Getppid()&0xffff)
}
