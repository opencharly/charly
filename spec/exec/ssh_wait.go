package exec

// ssh_wait.go — host-surface ssh readiness waits for a managed VM alias (moved from charly core's
// SSHExecutor.WaitFor* so the externalized vm deploy plugin, co-located on the host, drives them
// itself). These os/exec `ssh` against the managed alias — they run BEFORE the guest executor the
// reverse channel serves exists (WaitForSSH must poll a not-yet-up sshd), so they cannot use the
// reverse channel. The exact flag/script tuning below is load-bearing (slow-boot guests under
// parallel load); do not drift it.
//
// The poll LOOP + its readiness bounds live in the vmshared readiness subsystem (heavy: x/crypto),
// which kit must NOT import (kit is stdlib + spec only). So the caller INJECTS the poll via a PollFunc
// closure that wraps the host's real pollUntil + resolved bounds — kit owns the ssh/scp mechanics, the
// caller owns the readiness policy. One poll primitive, no duplication (R3).

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PollCond is a per-tick readiness probe (ready?, progress, err) — the same shape the host poll
// primitive expects.
type PollCond func(ctx context.Context) (ready bool, progress float64, err error)

// PollFunc drives a PollCond to readiness under the caller's readiness-configured bounds. Core and the
// vm plugin inject one wrapping vmshared's pollUntil + the resolved remote bounds, so kit never imports
// the readiness/poll subsystem.
type PollFunc func(ctx context.Context, cond PollCond) error

// SSHArgs is the host-surface ssh/scp coordinates for a managed VM alias.
type SSHArgs struct {
	User           string
	Host           string
	Port           int
	Args           []string
	ConnectTimeout int
}

func (a SSHArgs) connectTimeout() int {
	if a.ConnectTimeout <= 0 {
		return 10
	}
	return a.ConnectTimeout
}

// BaseArgs builds the common ssh invocation prefix: only the per-call ergonomics (LogLevel,
// ConnectTimeout) + optional Port + pass-through Args + the "user@host"-or-"host" destination
// (ssh(1) reads ~/.ssh/config + ssh-agent for keys/host-key checking/identity).
func (a SSHArgs) BaseArgs() []string {
	args := []string{"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=" + strconv.Itoa(a.connectTimeout())}
	if a.Port > 0 {
		args = append(args, "-p", strconv.Itoa(a.Port))
	}
	args = append(args, a.Args...)
	if a.User != "" {
		args = append(args, fmt.Sprintf("%s@%s", a.User, a.Host))
	} else {
		args = append(args, a.Host)
	}
	return args
}

// ScpBaseArgs builds the scp invocation prefix (scp's `-P` is uppercase vs ssh's lowercase `-p`).
func (a SSHArgs) ScpBaseArgs() []string {
	args := []string{"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=" + strconv.Itoa(a.connectTimeout())}
	if a.Port > 0 {
		args = append(args, "-P", strconv.Itoa(a.Port))
	}
	args = append(args, a.Args...)
	return args
}

// WaitForSSH polls `ssh <alias> true` until sshd answers (BINARY/EDGE readiness: refused→up), under
// the injected poll's bounds. ConnectTimeout=2 bounds each connect; ServerAlive* bound a
// connected-then-blackholed session; the injected poll's per-attempt context is the never-hang bound.
func WaitForSSH(ctx context.Context, ssh SSHArgs, poll PollFunc) error {
	if err := poll(ctx, func(actx context.Context) (bool, float64, error) {
		args := ssh.BaseArgs()
		args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
			"-o", "ServerAliveInterval=2", "-o", "ServerAliveCountMax=2", "true")
		cmd := exec.CommandContext(actx, "ssh", args...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		return cmd.Run() == nil, 0, nil
	}); err != nil {
		return fmt.Errorf("waiting for sshd on %s:%d: %w", ssh.Host, ssh.Port, err)
	}
	return nil
}

// WaitForCloudInit polls `sudo cloud-init status` (as root — charly guests have passwordless sudo)
// until cloud-init reaches an EXPLICIT terminal status (done/error/disabled) over a SURVIVING ssh
// connection — that is the deterministic signal that first-boot host-key regen finished (sshd stable)
// AND the seed-package phase (which holds the distro package lock) completed. Ready ONLY on a terminal
// token: a blank/transient/"running" read keeps polling, so the deploy's first `pacman -Sy` can't race
// cloud-final.service. Only meaningful for cloud-image sources; skip for bootc with no cidata ISO.
func WaitForCloudInit(ctx context.Context, ssh SSHArgs, poll PollFunc) error {
	script := `if command -v cloud-init >/dev/null 2>&1; then sudo cloud-init status 2>/dev/null || true; else echo "status: done"; fi`
	if err := poll(ctx, func(actx context.Context) (bool, float64, error) {
		var buf bytes.Buffer
		args := ssh.BaseArgs()
		args = append(args, "-o", "ServerAliveInterval=2", "-o", "ServerAliveCountMax=2", "bash", "-s")
		cmd := exec.CommandContext(actx, "ssh", args...)
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if cmd.Run() != nil {
			return false, 0, nil // ssh dropped (key regen in progress) — keep polling
		}
		out := buf.String()
		if strings.Contains(out, "status: running") {
			return false, 0, nil // cloud-init still working
		}
		if strings.Contains(out, "status: done") || strings.Contains(out, "status: error") ||
			strings.Contains(out, "status: disabled") {
			return true, 0, nil // settled (sshd stable, package phase complete)
		}
		return false, 0, nil // blank / not-started / transient — keep polling
	}); err != nil {
		return fmt.Errorf("cloud-init wait on %s:%d: %w", ssh.Host, ssh.Port, err)
	}
	return nil
}

// WaitForPackageLock makes the guest package manager READY for the deploy's own pacman/apt/dnf:
// (1) block until no boot-time package PROCESS runs (the direct R4 sync primitive on the contended
// lock, process-based so a stale lock can't hang it forever), then (2) clear a STALE lock FILE that a
// reaped boot-time install left with no holding process (re-checking no-process inside the command
// keeps the removal safe).
func WaitForPackageLock(ctx context.Context, ssh SSHArgs, poll PollFunc) error {
	noProc := `! pgrep -x pacman >/dev/null 2>&1 && ! pgrep -x pacman-key >/dev/null 2>&1 && ` +
		`! pgrep -x apt >/dev/null 2>&1 && ! pgrep -x apt-get >/dev/null 2>&1 && ` +
		`! pgrep -x dpkg >/dev/null 2>&1 && ! pgrep -x dnf >/dev/null 2>&1`
	if err := poll(ctx, func(actx context.Context) (bool, float64, error) {
		var buf bytes.Buffer
		args := ssh.BaseArgs()
		args = append(args, "bash", "-s")
		cmd := exec.CommandContext(actx, "ssh", args...)
		cmd.Stdin = strings.NewReader(`if ` + noProc + `; then echo FREE; else echo BUSY; fi`)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if cmd.Run() != nil {
			return false, 0, nil // ssh hiccup — keep polling
		}
		return strings.Contains(buf.String(), "FREE"), 0, nil
	}); err != nil {
		return fmt.Errorf("package-lock wait on %s:%d: %w", ssh.Host, ssh.Port, err)
	}
	clear := `if ` + noProc + `; then sudo -n rm -f /var/lib/pacman/db.lck ` +
		`/var/lib/dpkg/lock-frontend /var/lib/dpkg/lock 2>/dev/null; fi; true`
	cargs := ssh.BaseArgs()
	cargs = append(cargs, "bash", "-s")
	ccmd := exec.CommandContext(ctx, "ssh", cargs...)
	ccmd.Stdin = strings.NewReader(clear)
	_ = ccmd.Run() // best-effort stale-lock clear
	return nil
}
