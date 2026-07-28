package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// vm_backend_resolve.go — the VM backend detection capability (F6 vm-lifecycle move,
// coneB-vmlifecycle): ported from charly/vm_backend_lifecycle.go's resolveVmBackend/
// vmConfiguredBackend, which the "config-resolve" HostBuild seam (charly/host_build_config_resolve.go)
// used to compute host-side and ship over the wire as ConfigResolveReply.Backend. Both pieces are
// genuinely plugin-portable: resolveVmBackendPlugin is a pure host-env probe (systemctl/virsh/
// socket-stat via vmshared, zero core-registry coupling) using THIS package's own
// startLibvirtUserSession (already duplicated here, vm_phaseA_shims.go — R3, a separate module);
// vmConfiguredBackendPlugin's one dependency (the entity's `backend:` pin) is the generic, kind-blind
// "deploy-entity-resolve" HostBuild seam every other F6 consumer (kube/adb/bundle/deploy-vm) already
// calls directly — spec.DeployEntityResolveRequest's own CUE doc names vm_backend_lifecycle's
// vmConfiguredBackend as a consumer of this exact seam. hostConfigResolve (vm_host_seams.go) now
// computes cfg.Backend itself after decoding the wire reply, instead of trusting a wire field — the
// "config-resolve" reply no longer carries Backend (CUE field removed, sdk#<pending>).

// resolveVmBackendPlugin detects the available VM backend. Priority: libvirt → qemu.
func resolveVmBackendPlugin(configured string) (string, error) {
	if configured == "libvirt" || configured == "auto" {
		// Spawn the libvirt session daemon BEFORE probing for its socket — see
		// startLibvirtUserSession's own doc comment (vm_phaseA_shims.go) for why a cold
		// os.Stat() alone false-negatives a fully working libvirt.
		startLibvirtUserSession()
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
// entity: the entity's `backend:` pin (resolved via the generic "deploy-entity-resolve" HostBuild
// seam) when set, else the global vm.backend setting (rtBackend). THE single source so EVERY vm
// verb resolves the SAME backend for a given entity.
func vmConfiguredBackendPlugin(ctx context.Context, ex *sdk.Executor, vmName, rtBackend string) string {
	if vmName == "" {
		return rtBackend
	}
	reqJSON, err := json.Marshal(spec.DeployEntityResolveRequest{Kind: "vm", Name: vmName})
	if err != nil {
		return rtBackend
	}
	resJSON, err := ex.HostBuild(ctx, "deploy-entity-resolve", reqJSON)
	if err != nil {
		return rtBackend
	}
	var reply spec.DeployEntityResolveReply
	if json.Unmarshal(resJSON, &reply) != nil || len(reply.EntityJSON) == 0 {
		return rtBackend
	}
	var vm spec.ResolvedVm
	if json.Unmarshal(reply.EntityJSON, &vm) != nil || vm.Backend == "" {
		return rtBackend
	}
	return vm.Backend
}
