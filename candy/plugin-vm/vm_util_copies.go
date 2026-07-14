package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// vm_util_copies.go — small pure host helpers the moved VM CLI handlers reference by their former core
// short names. These carry NO core coupling (they read the resolved spec / the host env / pure paths),
// so they are copied verbatim from core rather than routed through a seam (the vm_phaseA_shims trivia
// line — below the bar for cross-module export). resolveVmSshPort is the one exception: its persisted-
// port READ is config state, so it reads reply.VmState via the config-resolve seam.

// UnifiedFileName is the project config filename ("charly.yml") — kit owns the one copy.
const UnifiedFileName = kit.UnifiedFileName

// resolveVmSshUser picks the guest SSH user from the resolved spec (verbatim from core).
func resolveVmSshUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	if spec.Source.BaseUser != "" {
		return spec.Source.BaseUser
	}
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return ""
}

// hostArchRuntime maps GOARCH to the libvirt/qemu arch string (verbatim from core).
func hostArchRuntime() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

// substTilde expands a leading ~ against home (verbatim from core).
func substTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// expandHostPath resolves a leading ~ against the host home (verbatim from core).
func expandHostPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~ in %q: %w", p, err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// memlockUnlimited reports whether a hard RLIMIT_MEMLOCK is effectively unlimited (verbatim from core).
func memlockUnlimited(hard uint64) bool { return hard >= 1<<62 }

// ResolveNewestLocalCalVer is a best-effort local-image newest-CalVer resolver for `charly vm cp-box`.
// The core resolver (local_image.go) queries the local podman store; the plugin has no direct store
// access, so this returns empty and the caller uses the ref as-authored (correct for an explicit tag).
// cp-box is not exercised by the vm beds; a follow-up adds a generic local-image-resolve seam for the
// bare-ref newest-CalVer convenience. Signature matches core's (engine, ref) -> (resolved, err).
func ResolveNewestLocalCalVer(_, _ string) (string, error) { return "", nil }

// resolveVmSshPort resolves the guest SSH host port. For ssh.port_auto it reuses the persisted port
// (from the config-resolve seam's VmState) for idempotency, else allocates a free one; a fixed port or
// the 2222 default otherwise. The persisted READ is the ONE core-coupled bit (routed through the seam).
func resolveVmSshPort(spec *VmSpec, vmName string) (int, error) {
	if spec.SSH != nil && spec.SSH.PortAuto {
		if reply, err := hostConfigResolve(vmName); err == nil && reply.VmState != nil && reply.VmState.SshPort > 0 {
			return reply.VmState.SshPort, nil
		}
		alloc, err := kit.AllocateAutoPorts([]int{22}, nil)
		if err != nil {
			return 0, fmt.Errorf("vm %q: ssh.port_auto allocation failed: %w", vmName, err)
		}
		return alloc[0].Host, nil
	}
	if spec.SSH != nil && spec.SSH.Port > 0 {
		return spec.SSH.Port, nil
	}
	return 2222, nil
}

// resolveVmPortForwards resolves each network.port_forwards entry to a concrete
// "<host>:<guest>" string, mirroring resolveVmSshPort: an `auto:<guest>` entry
// auto-allocates a free host port — reusing the persisted allocation from vm_state
// (guest→host map) for idempotency across the vm-create → deploy-add sequence — while
// a fixed "<host>:<guest>" passes through unchanged. The shared `occupied` set (seeded
// with the ssh host port by the caller) prevents an auto forward from colliding with
// the ssh port or another forward WITHIN one create. Returns the resolved strings (fed
// to rt.ExtraPortForwards, the sole render input) plus the guest→host allocation map to
// persist. The persisted READ is the ONE core-coupled bit (routed through the seam).
func resolveVmPortForwards(spec *VmSpec, vmName string, occupied map[int]bool) (resolved []string, allocated map[string]int, err error) {
	if spec.Network == nil || len(spec.Network.PortForwards) == 0 {
		return nil, nil, nil
	}
	// Prior persisted allocations (an `auto` entry reuses the same host port keyed by
	// its guest port, so a create→deploy-add re-resolve is stable). The seam READ is
	// the ONLY core-coupled bit; the pure allocation logic is resolvePortForwards.
	var prior map[string]int
	if reply, rerr := hostConfigResolve(vmName); rerr == nil && reply.VmState != nil {
		prior = reply.VmState.PortForwards
	}
	return resolvePortForwards(spec.Network.PortForwards, prior, occupied, vmName)
}

// resolvePortForwards is the pure (seam-free, unit-tested) allocation core: given the
// authored port_forwards entries, the prior persisted guest→host allocations, and the
// shared occupied set, it resolves each entry to a concrete "<host>:<guest>" string
// (auto → reused-prior-else-allocated; fixed → passthrough) and returns the resolved
// list + the guest→host map of the auto allocations to persist.
func resolvePortForwards(forwards []string, prior map[string]int, occupied map[int]bool, vmName string) (resolved []string, allocated map[string]int, err error) {
	if occupied == nil {
		occupied = map[int]bool{}
	}
	for _, pf := range forwards {
		host, guest, ok := strings.Cut(pf, ":")
		if !ok || host == "" || guest == "" {
			// Malformed — CUE rejects these at load; defensive skip mirrors the renderers.
			continue
		}
		if host != "auto" {
			resolved = append(resolved, pf) // fixed passthrough (e.g. "2222:22")
			continue
		}
		if h, hit := prior[guest]; hit && h > 0 && !occupied[h] {
			occupied[h] = true
			if allocated == nil {
				allocated = map[string]int{}
			}
			allocated[guest] = h
			resolved = append(resolved, fmt.Sprintf("%d:%s", h, guest))
			continue
		}
		gi, cerr := strconv.Atoi(guest)
		if cerr != nil || gi <= 0 {
			return nil, nil, fmt.Errorf("vm %q: invalid guest port %q in port_forwards %q", vmName, guest, pf)
		}
		alloc, aerr := kit.AllocateAutoPorts([]int{gi}, occupied)
		if aerr != nil {
			return nil, nil, fmt.Errorf("vm %q: port_forwards auto-allocation failed for guest %d: %w", vmName, gi, aerr)
		}
		if allocated == nil {
			allocated = map[string]int{}
		}
		allocated[guest] = alloc[0].Host
		resolved = append(resolved, fmt.Sprintf("%d:%s", alloc[0].Host, guest))
	}
	return resolved, allocated, nil
}
