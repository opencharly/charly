package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
)

// vm_backend_resolve.go — the VM backend detection capability (F6 vm-lifecycle move,
// coneB-vmlifecycle): ported from charly/vm_backend_lifecycle.go's resolveVmBackend/
// vmConfiguredBackend, which the "config-resolve" HostBuild seam (charly/host_build_config_resolve.go)
// used to compute host-side and ship over the wire as ConfigResolveReply.Backend. Both pieces are
// genuinely plugin-portable: resolveVmBackendPlugin is a pure host-env probe (systemctl/virsh/
// socket-stat via vmshared, zero core-registry coupling) using vmshared.StartLibvirtUserSession (an
// R3 hoist — this package used to carry its own duplicate copy, vm_phaseA_shims.go, byte-identical to
// core's now-deleted vm_backend_lifecycle.go; both collapsed into the ONE sdk/vmshared copy);
// vmConfiguredBackendPlugin's one dependency (the entity's `backend:` pin) self-loads the project
// PLUGIN-SIDE now (K-wave W3a A3-phase-2: loaderkit.ResolveVmEntityViaExecutor, unblocked by W1's
// LoadUnifiedViaExecutor) — the former generic, kind-blind "deploy-entity-resolve" HostBuild seam
// every F6 consumer (kube/adb/bundle/deploy-vm) used to call directly is DELETED. hostConfigResolve
// (vm_host_seams.go) now computes cfg.Backend itself after decoding the wire reply, instead of
// trusting a wire field — the "config-resolve" reply no longer carries Backend (CUE field removed,
// sdk#<pending>).

// resolveVmBackendPlugin detects the available VM backend. Priority: libvirt → qemu.
func resolveVmBackendPlugin(configured string) (string, error) {
	if configured == "libvirt" || configured == "auto" {
		// Spawn the libvirt session daemon BEFORE probing for its socket — see
		// vmshared.StartLibvirtUserSession's own doc comment for why a cold
		// os.Stat() alone false-negatives a fully working libvirt.
		vmshared.StartLibvirtUserSession()
		picked, probed := vmshared.LibvirtSessionSocketWithProbes()
		if _, err := os.Stat(picked); err == nil {
			return "libvirt", nil
		}
		if configured == "libvirt" {
			var trail strings.Builder
			for _, p := range probed {
				if _, err := os.Stat(p); err == nil {
					fmt.Fprintf(&trail, "\n  %s — found", p)
				} else {
					fmt.Fprintf(&trail, "\n  %s — not found", p)
				}
			}
			return "", fmt.Errorf(
				"libvirt backend requires libvirt session daemon (probed:%s\n"+
					"configure libvirt session daemon or run: charly settings set vm.backend qemu)",
				trail.String(),
			)
		}
	}
	if configured == "qemu" || configured == "auto" {
		qemuBin := vmshared.QemuSystemBinary()
		if _, err := exec.LookPath(qemuBin); err == nil {
			return "qemu", nil
		}
		if configured == "qemu" {
			return "", fmt.Errorf("qemu backend requires %s", qemuBin)
		}
	}
	return "", fmt.Errorf("no VM backend available (install libvirt or qemu-system)")
}

// vmConfiguredBackendPlugin returns the backend string to feed resolveVmBackendPlugin for a vm
// entity: the entity's `backend:` pin (self-loaded PLUGIN-SIDE — K-wave W3a A3-phase-2,
// loaderkit.ResolveVmEntityViaExecutor, unblocked by W1's LoadUnifiedViaExecutor; the former
// "deploy-entity-resolve" HostBuild seam this round-tripped through is DELETED) when set, else the
// global vm.backend setting (rtBackend). THE single source so EVERY vm verb resolves the SAME
// backend for a given entity. dir="" resolves against this process's own cwd — plugin-vm is
// COMPILED-IN, so that IS the host's cwd (no deploy-plugins-connect indirection needed, unlike an
// out-of-process caller).
func vmConfiguredBackendPlugin(ctx context.Context, ex *sdk.Executor, vmName, rtBackend string) string {
	if vmName == "" {
		return rtBackend
	}
	vm, err := loaderkit.ResolveVmEntityViaExecutor(ctx, ex, "", vmName)
	if err != nil || vm == nil || vm.Backend == "" {
		return rtBackend
	}
	return vm.Backend
}
