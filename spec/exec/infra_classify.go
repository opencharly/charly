package exec

import (
	"fmt"
	"strings"
)

// ── Container-setup infra-failure classification (the R44 store-contention fix) ──
//
// `charly check box` (R44 Option A) runs its probes inside ONE persistent container
// (checkBoxContainerChain: `podman run -d … sleep infinity` once, then `podman exec`
// per step). The container SETUP (rootfs mount + temp-/etc/passwd generation for a
// non-root image USER) can race the builders' layer commits in the shared c/storage;
// podman fails the setup and unmounts, exiting nonzero (observed: 127 with "creating
// temporary passwd file for container … / Cleaning up container: unmounting container",
// plus the 125 store-lock class) — BEFORE the check command ever ran. Option A shrinks
// this to ONE setup per check-box (was one per step); a residual failure is classified
// here and surfaced as the infra exit class (never checks-failed, never retried).
//
// That is podman's OWN infra failure, NOT the checked command's verdict. Without this
// classifier the probe path laundered any positive podman exit into a "check result"
// (RunCaptureCmd returned it as (code, nil)), so a transient container-setup race became
// a reproducible RED check step. This classifier lets the container executor return a
// MARKED infra error instead — which the eventually.go bounded retry re-attempts (a probe
// that never RAN is re-run, exactly like the signal-kill class) and which the check-box
// exit mapping routes to the INFRA exit class (1), never checks-failed (2).
//
// The classification is BY EVIDENCE, never by correlation: exit-125 is podman's own
// structured error exit (the PRIMARY signal); the stderr signatures below are the
// SECONDARY discriminator for the non-125 manifestations (e.g. the 127 passwd-setup
// case). A genuine in-container "command not found" (the far-side shell started — bash
// or, on a busybox base, sh — and the command is absent)
// prints "…: command not found" and matches NO signature → it stays an ordinary check
// result. The table is the single source of truth, exercised by infra_classify_test.go.

// PodmanInfraExitCode is podman's own "an error occurred in podman itself" exit code —
// container create/store/config failures the CLI reports before (or instead of) running
// the container command. It is the PRIMARY structured infra signal. (podman/libpod
// registerExitCode: 125 = "the error is with podman itself".)
const PodmanInfraExitCode = 125

// ContainerInfraErrMarker is the stable substring the container executor stamps into a
// container-setup infra error, mirroring SignalKillErrMarker. probeWasContainerInfra
// (eventually.go), the check-box exit mapping, and the reporter all key on it — one
// literal, no parallel copies (R3).
const ContainerInfraErrMarker = "container-setup infra failure"

// infraSignature is one stderr substring plus its PROVENANCE — which podman/OCI/c-storage
// error emits it — so a future reader can tell a real infra string from a guess.
type infraSignature struct {
	Substr     string
	Provenance string
}

// containerInfraSignatures: the SECONDARY discriminator. Each entry is a substring of the
// FAILING `podman run`/`podman exec` STDERR that only podman/crun/c-storage emit at
// container setup — never a user command's own output. Provenance-commented per entry.
var containerInfraSignatures = []infraSignature{
	{"creating temporary passwd file", "libpod generatePasswdAndGroup: the probe container's temp /etc/passwd could not be written at setup (mount/store raced a concurrent build) — the R44 motivating error, seen with exit 127"},
	{"unmounting container", "libpod cleanupStorage: 'Cleaning up container: unmounting container …' — podman tearing down a container whose START failed (the R44 error's second line)"},
	{"error creating container", "libpod container create failed (store/config error before the command ran)"},
	{"error mounting", "c/storage overlay mount failure while assembling the container rootfs (a lower layer raced a concurrent build's commit/prune)"},
	{"layer not known", "c/storage: a layer referenced during mount was mid-commit / just-pruned by a concurrent build (directly reproduced under build+prune churn)"},
	{"image not known", "c/storage: the resolved image ID was removed by a concurrent prune between resolution and mount"},
	{"database is locked", "c/storage boltdb/sqlite store write-lock contention — the pre-transient_store 125 concurrency-ceiling class"},
	{"OCI runtime", "crun/runc could not set up or exec the container (the runtime failed before/at command exec, e.g. 'OCI runtime attempted to invoke a command that was not found')"},
}

// ClassifyContainerInfraFailure reports whether a NONZERO container-run/exec result is
// podman's OWN infra failure (the container never ran the check command), returning the
// matched signature (with provenance) for annotation. exit-125 is the primary signal;
// the stderr table is the secondary discriminator. A zero exit is never infra.
func ClassifyContainerInfraFailure(exitCode int, stderr string) (signature string, ok bool) {
	if exitCode == 0 {
		return "", false
	}
	if exitCode == PodmanInfraExitCode {
		return fmt.Sprintf("exit-%d (podman's own error exit)", PodmanInfraExitCode), true
	}
	for _, s := range containerInfraSignatures {
		if strings.Contains(stderr, s.Substr) {
			return fmt.Sprintf("%q (%s)", s.Substr, s.Provenance), true
		}
	}
	return "", false
}

// IsContainerInfraResult reports whether a CheckResult/StepResult message carries the
// infra marker (the executor stamped a container-setup infra error into it, propagated
// verbatim by the verbs' `execution error: %v` formatting). Keyed on the ONE marker (R3).
func IsContainerInfraResult(message string) bool {
	return strings.Contains(message, ContainerInfraErrMarker)
}

// containerInfraError builds the MARKED error the container executor returns for a
// classified setup failure. The marker rides through each verb's existing
// `kit.Failf("… %v", err)` into the CheckResult.Message — no per-verb change (the exact
// mechanism SignalKillErrMarker already uses).
func containerInfraError(signature string, exitCode int, stderrPreview string) error {
	return fmt.Errorf("%s [%s]: podman exit=%d (stderr: %s)",
		ContainerInfraErrMarker, signature, exitCode, stderrPreview)
}

// ContainerInfraError is the exported constructor for a classified CONTAINER-CREATE infra
// failure — the R44 Option-A box-mode creates ONE persistent container host-side, and its
// create failure is classified there (not through the per-step RunCapture seam). The caller
// returns this as the check's error so it exits the INFRA class (a plain error → exit 1),
// never checks-failed, and the marker makes it recognizable in logs.
func ContainerInfraError(signature string, exitCode int, stderrPreview string) error {
	return containerInfraError(signature, exitCode, stderrPreview)
}
