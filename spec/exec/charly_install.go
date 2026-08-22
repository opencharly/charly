package exec

// charly_install.go — deliver an invokable `charly` into a deployment venue (moved from charly core;
// all its callers were the vm PrepareVenue + nested-pod delegation, now in candy/plugin-deploy-vm).
// Two entry points share ONE decision (probe version → CalVer compare → deliver-if-absent-or-older):
//   - EnsureCharlyInDeployVenue: over spec.DeployExecutor — used by host-side live process
//     transports before starting the fixed gRPC stdio endpoint.
//   - EnsureCharlyInGuest: host-surface ssh/scp against a managed VM alias — used by the vm deploy
//     plugin's PrepareVenue, BEFORE the reverse channel serves a guest executor.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/spec/spec"
)

// CharlyInstallStrategy is the resolved charly_install.strategy: "auto" | "scp" | "skip".
type CharlyInstallStrategy string

const (
	CharlyInstallAuto CharlyInstallStrategy = "auto"
	CharlyInstallScp  CharlyInstallStrategy = "scp"
	CharlyInstallSkip CharlyInstallStrategy = "skip"
)

// ResolveCharlyInstallStrategy applies the default to a raw charly_install.strategy string.
func ResolveCharlyInstallStrategy(raw string) CharlyInstallStrategy {
	switch raw {
	case "auto", "scp", "skip":
		return CharlyInstallStrategy(raw)
	default:
		return CharlyInstallAuto // "" and any validator-rejected value default to auto
	}
}

// HostCharlyIsNewer reports whether the host charly (hostVer) is STRICTLY newer than a venue's charly
// (venueVerOut = raw `charly version` stdout). The single CalVer arbiter (R3): a venue version that is
// unparseable/absent → host wins (treated as older); both parse → strict CalVer compare (host newer →
// true, venue equal-or-newer → false, never downgraded); host unparseable → false (never clobber a
// venue charly on an unprovable claim).
func HostCharlyIsNewer(hostVer, venueVerOut string) bool {
	host, hostOK := spec.ParseCalVer(strings.TrimSpace(hostVer))
	if !hostOK {
		return false
	}
	venue, venueOK := spec.ParseCalVer(strings.TrimSpace(venueVerOut))
	if !venueOK {
		return true
	}
	return venue.Less(host)
}

// ensureCharly is the shared probe→compare→deliver decision behind both transports. probe() returns
// the venue's `charly version` output (empty if absent); present(path) reports whether a delivered
// copy at path already runs; replicaPath() identifies the active controller bytes; and
// deliver(remotePath) copies charlyBin in. Returns "charly" (venue PATH charly current — never
// shadowed) or a content-addressed /tmp path (idempotent: only a byte-identical copy is reused).
func ensureCharly(hostVer string, probe func() string, replicaPath func() (string, error), present func(path string) bool, deliver func(remotePath string) error) (string, error) {
	if venueVer := strings.TrimSpace(probe()); venueVer != "" && !HostCharlyIsNewer(hostVer, venueVer) {
		return "charly", nil
	}
	tmp, err := replicaPath()
	if err != nil {
		return "", err
	}
	if present(tmp) {
		return tmp, nil
	}
	if err := deliver(tmp); err != nil {
		return "", err
	}
	return tmp, nil
}

func replicatedCharlyPath(charlyBin, hostVer string) (string, error) {
	content, err := os.ReadFile(charlyBin)
	if err != nil {
		return "", fmt.Errorf("reading host charly %s for replication identity: %w", charlyBin, err)
	}
	digest := sha256.Sum256(content)
	return fmt.Sprintf("/tmp/charly-%s-%x", hostVer, digest[:8]), nil
}

// EnsureCharlyInDeployVenue delivers the controller charly into a live
// spec.DeployExecutor. It gives every process-capable deployment the same Ansible-like bootstrap:
// keep an equal/newer packaged Charly, otherwise place the active controller binary at a
// versioned /tmp path and invoke that explicit path. It never changes PATH and never downgrades a
// target package. The underlying probe/compare/deliver decision remains ensureCharly (one copy).
func EnsureCharlyInDeployVenue(ctx context.Context, ve spec.DeployExecutor, charlyBin, hostVer string) (string, error) {
	if ve == nil {
		return "", fmt.Errorf("ensure charly in deploy venue: nil executor")
	}
	return ensureCharly(hostVer,
		func() string {
			out, _, _, _ := ve.RunCapture(ctx, `command -v charly >/dev/null 2>&1 && charly version 2>/dev/null || true`)
			return out
		},
		func() (string, error) { return replicatedCharlyPath(charlyBin, hostVer) },
		func(path string) bool {
			_, _, exit, err := ve.RunCapture(ctx, spec.ShellQuote(path)+" version >/dev/null 2>&1")
			return err == nil && exit == 0
		},
		func(remotePath string) error {
			if err := ve.PutFile(ctx, charlyBin, remotePath, 0o755, false, spec.EmitOpts{}); err != nil {
				return fmt.Errorf("copying host charly into deploy venue %s: %w", remotePath, err)
			}
			return nil
		})
}

// EnsureCharlyInGuest is the vm-deploy PrepareVenue-time coordinator: it layers the cloud-init
// charly_install.strategy over a HOST-SURFACE delivery (os/exec ssh/scp against the managed alias),
// because at PrepareVenue time the reverse channel has NOT yet served a guest executor. auto/scp:
// deliver the host charly when the guest's is absent/older; skip: verify presence only.
func EnsureCharlyInGuest(ctx context.Context, ssh SSHArgs, charlyBin, hostVer, strategyRaw string) (string, error) {
	switch ResolveCharlyInstallStrategy(strategyRaw) {
	case CharlyInstallAuto, CharlyInstallScp:
		cmd, err := ensureCharly(hostVer,
			func() string {
				return sshCapture(ctx, ssh, `command -v charly >/dev/null 2>&1 && charly version 2>/dev/null || true`)
			},
			func() (string, error) { return replicatedCharlyPath(charlyBin, hostVer) },
			func(path string) bool { return sshOK(ctx, ssh, spec.ShellQuote(path)+" version >/dev/null 2>&1") },
			func(remotePath string) error { return scpInto(ctx, ssh, charlyBin, remotePath) })
		if err != nil {
			return "", err
		}
		if cmd == "charly" {
			return "guest charly is current (>= host); using the system charly (no scp)", nil
		}
		return fmt.Sprintf("guest charly absent/outdated; host charly provided at %s for deploy use", cmd), nil
	case CharlyInstallSkip:
		if !sshOK(ctx, ssh, `command -v charly >/dev/null 2>&1`) {
			return "", fmt.Errorf("charly binary not present in guest (charly_install.strategy: skip)")
		}
		return "verified charly present in guest (strategy=skip)", nil
	default:
		return "", fmt.Errorf("unknown charly_install.strategy: %q", strategyRaw)
	}
}

// sshCapture runs script over `ssh <alias> bash -s`, returning stdout (empty on failure).
func sshCapture(ctx context.Context, ssh SSHArgs, script string) string {
	var buf bytes.Buffer
	args := append(ssh.BaseArgs(), "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = &buf
	_ = cmd.Run()
	return buf.String()
}

// sshOK runs script over `ssh <alias> bash -s`, reporting exit==0.
func sshOK(ctx context.Context, ssh SSHArgs, script string) bool {
	args := append(ssh.BaseArgs(), "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	return cmd.Run() == nil
}

// scpInto scp's localPath to <alias>:remotePath (scp reads ~/.ssh/config for user/port/key via the alias).
func scpInto(ctx context.Context, ssh SSHArgs, localPath, remotePath string) error {
	dest := ssh.Host
	if ssh.User != "" {
		dest = ssh.User + "@" + ssh.Host
	}
	args := append(ssh.ScpBaseArgs(), localPath, dest+":"+remotePath)
	cmd := exec.CommandContext(ctx, "scp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp charly into guest %s: %w: %s", remotePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
