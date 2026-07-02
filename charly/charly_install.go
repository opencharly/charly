package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// CharlyInstallStrategy is the resolved strategy string after defaults.
// Values: "auto" | "scp" | "skip". Empty input resolves to "auto".
type CharlyInstallStrategy string

const (
	CharlyInstallAuto CharlyInstallStrategy = "auto"
	CharlyInstallScp  CharlyInstallStrategy = "scp"
	CharlyInstallSkip CharlyInstallStrategy = "skip"
)

// ResolveCharlyInstallStrategy reads spec.CloudInit.CharlyInstall and applies
// the default. Used by the vm deploy's PrepareVenue to decide which post-boot action
// (if any) to perform for delivering the charly binary into the guest.
func ResolveCharlyInstallStrategy(spec *VmSpec) CharlyInstallStrategy {
	if spec == nil || spec.CloudInit == nil || spec.CloudInit.CharlyInstall == nil {
		return CharlyInstallAuto
	}
	s := spec.CloudInit.CharlyInstall.Strategy
	switch s {
	case "":
		return CharlyInstallAuto
	case "auto", "scp", "skip":
		return CharlyInstallStrategy(s)
	default:
		// Validator should have caught this; defensive fallback.
		return CharlyInstallAuto
	}
}

// EnsureCharlyInVenue is the GENERIC "copy charly into a running system" mechanism: it
// guarantees an invokable `charly` on ANY deployment venue — container (podman cp),
// VM / SSH host (scp), or the local host (install) — and returns the command the
// caller should use to invoke it. It is venue-agnostic because it works only
// through the DeployExecutor abstraction (RunCapture to probe, PutFile to
// deliver — cp / scp / podman cp per executor), so one code path serves every
// substrate (R3). Used by the VM-deploy strategy wrapper (EnsureCharlyInGuest) to
// deliver a guest `charly` for in-guest deploy + nested-pod checks, so a VM image
// need NOT bake the `charly` candy for that transient need.
//
// Resolution (quiet — the caller decides what, if anything, to print):
//
//  1. The venue's SYSTEM charly (PATH `charly`, normally a package-managed binary kept
//     current by the package manager) is authoritative whenever it is at least
//     as new as the host's (CalVer — the true build identity, never a content
//     checksum). It is used as-is: NEVER shadowed, downgraded, or overwritten.
//  2. Otherwise — ONLY when the venue charly is ABSENT or strictly OLDER (the
//     automatic CalVer check is never skipped) — the host's OWN binary
//     (os.Executable(), guaranteed current and from-box-capable) is delivered
//     to a /tmp/charly-<calver> path. That path is OUTSIDE $PATH and is invoked by
//     EXPLICIT path, NEVER via a PATH lookup — so it can NOT shadow a
//     package-manager charly (e.g. the opencharly-git /usr/bin/charly), not even one a
//     package manager installs LATER: nothing puts the copy into a higher-PATH-
//     priority location than the package. The /tmp name embeds the host CalVer so
//     repeated calls reuse the same copy (idempotent — a still-good prior copy is
//     verified and reused, never re-transferred).
//
// Returns "charly" (use the venue's PATH charly) or "/tmp/charly-<calver>" (the delivered
// host copy). SweepStaleTemps reclaims leftover /tmp copies.
func EnsureCharlyInVenue(ctx context.Context, exec DeployExecutor, opts EmitOpts) (string, error) {
	hostVer := CharlyVersion()
	stdout, _, _, _ := exec.RunCapture(ctx,
		`command -v charly >/dev/null 2>&1 && charly version 2>/dev/null || true`)
	venueVer := strings.TrimSpace(stdout)

	// Venue charly present AND equal-or-newer → use the system charly as-is.
	if venueVer != "" && !hostCharlyIsNewer(hostVer, venueVer) {
		return "charly", nil
	}

	tmp := "/tmp/charly-" + hostVer

	// Idempotent: a prior copy at the stable /tmp path that still runs is reused
	// — never re-transferred (the host binary is unchanged within one CalVer).
	if _, _, exit, err := exec.RunCapture(ctx,
		deployShellQuote(tmp)+" version >/dev/null 2>&1"); err == nil && exit == 0 {
		return tmp, nil
	}

	// Venue charly absent or older → deliver the host binary at the /tmp path
	// (outside $PATH; the system charly, if any, is left untouched).
	if err := putHostCharlyInVenue(ctx, exec, tmp, false, opts); err != nil {
		return "", err
	}
	return tmp, nil
}

// EnsureCharlyInGuest is the VM-deploy strategy coordinator the vm lifecycle hook's PrepareVenue calls
// after the guest is booted, sshd is up, and cloud-init has finished. It layers
// the cloud-init `charly_install.strategy` policy on top of the generic
// EnsureCharlyInVenue mechanism:
//
//	auto / scp — EnsureCharlyInVenue: use the guest's system charly when current
//	             (>= host by CalVer); ONLY when absent or older, deliver the host
//	             charly to a /tmp path (no shadow). Routine updates are the package
//	             manager's job.
//	skip       — verify `command -v charly` exists; error if missing
//
// Returns an informational message on success suitable for printing at info
// level (the deploy context wants the detail; a caller that wants it quiet can
// call the generic EnsureCharlyInVenue directly).
func EnsureCharlyInGuest(
	ctx context.Context,
	spec *VmSpec,
	exec DeployExecutor,
	opts EmitOpts,
) (string, error) {
	strategy := ResolveCharlyInstallStrategy(spec)

	switch strategy {
	case CharlyInstallAuto, CharlyInstallScp:
		cmd, err := EnsureCharlyInVenue(ctx, exec, opts)
		if err != nil {
			return "", err
		}
		if cmd == "charly" {
			return "guest charly is current (>= host); using the system charly (no scp)", nil
		}
		return fmt.Sprintf("guest charly absent/outdated; host charly provided at %s for deploy use", cmd), nil

	case CharlyInstallSkip:
		return verifyCharlyPresent(ctx, exec, opts, "skip")

	default:
		return "", fmt.Errorf("unknown charly_install.strategy: %q", strategy)
	}
}

// putHostCharlyInVenue delivers THIS process's OWN charly binary (os.Executable()) into
// the venue at remotePath via the DeployExecutor's PutFile (cp for the local
// host, scp for an SSH/VM venue, `podman cp` for a container) — the single
// host→venue charly-delivery primitive (R3), used by EnsureCharlyInVenue (the generic
// copy-in) AND by deployNestedPodsInGuest (the host→nested from-box
// delegation). Callers deliver to a /tmp path with ownerRoot=false — a non-$PATH
// copy that leaves a venue's own packaged charly untouched and un-shadowed; the
// caller invokes the delivered binary by explicit path.
//
// The host binary is guaranteed current and from-box-capable: it is the binary
// running this very deploy. That is the whole point — a venue's own PATH charly may
// be a stale candy install (a @github-fetched charly candy ships no bin/charly, so its
// cmd: curls a pre-from-box release that reports the wall clock as its version)
// and must never be trusted as the delegation binary.
func putHostCharlyInVenue(ctx context.Context, exec DeployExecutor, remotePath string, ownerRoot bool, opts EmitOpts) error {
	localPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating local charly via os.Executable(): %w", err)
	}
	// On Linux os.Executable returns the realpath; on macOS it can point at an
	// app bundle — stat it to ensure it's a regular file.
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local charly path %s is not a regular file", localPath)
	}
	if err := exec.PutFile(ctx, localPath, remotePath, 0o755, ownerRoot, opts); err != nil {
		return fmt.Errorf("copying host charly into venue %s: %w", remotePath, err)
	}
	return nil
}

// verifyCharlyPresent runs `command -v charly` in the guest and reports
// whether the binary is already in place. Tail of the "skip" strategy
// (user-managed charly install).
func verifyCharlyPresent(ctx context.Context, exec DeployExecutor, opts EmitOpts, strategy string) (string, error) {
	// `charly version` (subcommand), not `charly --version`. Kong returns exit 80
	// for unknown flags, which this check otherwise surfaces as a false
	// "charly not present" error. The PKGBUILD installs to /usr/bin/charly, not
	// /usr/local/bin/charly — rely on PATH rather than hard-coding either.
	script := `
set -e
if ! command -v charly >/dev/null 2>&1; then
    echo "charly binary not present in guest (charly_install.strategy: ` + strategy + `)"
    exit 1
fi
charly version
`
	if err := exec.RunSystem(ctx, script, opts); err != nil {
		return "", fmt.Errorf("charly presence check (strategy=%s): %w", strategy, err)
	}
	return fmt.Sprintf("verified charly present in guest (strategy=%s)", strategy), nil
}
