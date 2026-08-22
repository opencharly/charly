package exec

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/opencharly/spec/spec"
)

// venue_wait.go — the deploy-venue READINESS gates: pure process-driving pollers that wait for a
// freshly-deployed VM (SSH-reachable + cloud-init settled) or container (exec-able + supervisord
// children settled) to reach steady state before a check-live probe runs. Relocated from
// sdk/deploykit (#55 K4) — they compose only spec/exec executors + spec readiness primitives +
// os/exec (the process fabric), with NO registry/loader/host-state coupling, so they belong in
// spec/exec beside the executors they gate (the boundary law: "drives podman/ssh itself" is not a
// permanence reason). candy/plugin-check's bed session calls them directly (#55 W3 B2-full — the
// bed-SESSION mechanism dissolved entirely into bed_session.go; PersistBedDeployOverrides is the
// one piece that genuinely stayed in sdk/deploykit, a plain library function any placement can
// call — ResolveBedCheckLevel, its sibling, turned out to have zero remaining callers once every
// consumer inlined to the spec.* helpers directly, and was deleted as dead code).

// WaitForVmSshReady gates on the VM being SSH-reachable AND cloud-init having settled, using
// the SAME deterministic SSHExecutor preflight the VM check-live path and the external vm
// deploy walk run — NOT a fixed sleep. WaitForSSH polls until sshd answers; WaitForCloudInit
// retries until an ssh connection survives a `cloud-init status` poll (the deterministic
// cloud-init-settled signal — so deploy-add never races a still-running first-boot pacman).
// domainID is the per-deploy DOMAIN IDENTITY (the bed/member deploy name), not the shared
// kind:vm entity — the alias the create path published. Best-effort: silent on timeout (the
// downstream deploy-add surfaces the real error).
func WaitForVmSshReady(domainID string) {
	gate := &SSHExecutor{Host: spec.VmSshAlias(domainID), ConnectTimeout: 5}
	ctx := context.Background()
	if err := gate.WaitForSSH(ctx); err != nil {
		return
	}
	_ = gate.WaitForCloudInit(ctx)
}

// WaitForContainerReady gates on the container being exec-able AND its supervisord-managed
// children having left their transitional states, so a one-shot check-live port/service probe
// never races a child that has not yet bound. `charly start` returns when systemd reports the
// service active, but supervisord's autostart children are still STARTING for a moment after.
// This polls `supervisorctl status` until no child is STARTING/BACKOFF (a child binds its port
// the instant it reaches RUNNING) instead of sleeping a fixed, host-tuned interval. Images
// without supervisord settle immediately. Best-effort: silent on timeout (the next check-live
// step surfaces the real failure). Reads the project's readiness bounds via
// spec.ReadinessProvider() — the SAME plugin-portable channel every other executor wait uses
// (the host threads its resolved bounds via ResolvedReadiness.PluginEnv; a project-unaware
// caller falls back to the built-in defaults).
func WaitForContainerReady(bed string) {
	containerName := "charly-" + bed
	// supervisorStatus reports __NOSUP__ when the image has no supervisorctl, so
	// "no supervisord" is distinguishable from "socket not up yet".
	const supervisorStatus = `command -v supervisorctl >/dev/null 2>&1 || { echo __NOSUP__; exit 0; }; supervisorctl status 2>&1`
	// MONOTONIC readiness via the unified pollUntil primitive: the progress marker is the
	// count of SETTLED children — it climbs as children reach RUNNING, so a slow startup
	// under heavy parallel load is waited for (the no-progress watchdog resets on each new
	// settled child); a child crash-looping back to BACKOFF drops the count below its
	// high-water, so the watchdog correctly does NOT treat the flap as progress and the bed
	// stalls out instead of hiding the fault. Best-effort: silent on stall/cap (the next
	// check-live step surfaces the real failure).
	cfg := spec.ReadinessProvider().Wait("container-ready "+bed, spec.PollLocal)
	_ = spec.PollUntil(context.Background(), cfg, func(actx context.Context) (bool, float64, error) {
		if exec.CommandContext(actx, "podman", "exec", containerName, "true").Run() != nil {
			return false, 0, nil // container not exec-able yet
		}
		out, _ := exec.CommandContext(actx, "podman", "exec", containerName, "sh", "-c", supervisorStatus).CombinedOutput()
		if bytes.Contains(out, []byte("__NOSUP__")) {
			return true, 0, nil // no supervisord — nothing to settle
		}
		settled := float64(bytes.Count(out, []byte("RUNNING")) + bytes.Count(out, []byte("STOPPED")) +
			bytes.Count(out, []byte("EXITED")) + bytes.Count(out, []byte("FATAL")))
		if bytes.Contains(out, []byte("STARTING")) || bytes.Contains(out, []byte("BACKOFF")) {
			return false, settled, nil // children still coming up
		}
		if settled > 0 {
			return true, settled, nil // supervisord answered + nothing transitional
		}
		return false, 0, nil // supervisord control socket not up yet
	})
}
