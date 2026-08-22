package lock

// bed_vm_domain.go — the per-libvirt-domain host contention locks for a check bed,
// RELOCATED to the spec/lock fabric slice (#55 CHECK-ENGINE cone Option A — the bed-session
// lock family candy/plugin-check's bed session (bed_session.go, #55 W3 B2-full) reaches importing
// zero kit). Pure over an already-LOADED (loader-stamped) spec.FleetNode: it reads node.Descent
// directly rather than falling back to a registry-backed resolver — a check bed's node always
// comes from LoadUnified, so it is always stamped, and the registry fallback never fires for
// this caller. A caller holding a possibly-synthetic node must NOT use this pair; a synthetic
// (un-stamped) node has no registry-aware resolver to fall back to anymore either — core's former
// on-the-fly nodeTraits died with its last caller (#55 W3 B2-full); every surviving consult site
// reads node.Descent directly, stamped by the loader. sdk/kit re-exports each symbol
// (sdk/kit/check_bed_lock.go) so every existing kit.BedVmDomains / kit.AcquireVmDomainLock call
// site (charly core + plugins) is untouched. New consumers reference spec/lock directly.

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/opencharly/spec/spec"
)

// BedVmDomains returns the sorted, deduped libvirt domain names (charly-<from>) a bed's VM(s)
// occupy — the bed's own vm target plus any group-member vm targets. This is the unit of
// exclusive host contention two DISTINCT beds can collide on (the per-domain lock in
// AcquireVmDomainLock serializes them).
func BedVmDomains(name string, node spec.FleetNode) []string {
	seen := map[string]bool{}
	var out []string
	add := func(domainID string) {
		if domainID == "" {
			return
		}
		dom := "charly-" + domainID
		if seen[dom] {
			return
		}
		seen[dom] = true
		out = append(out, dom)
	}
	if node.Descent != nil && node.Descent.Venue == "ssh" { // vm (ssh venue) root
		add(spec.VmDomainIdentity(name))
	}
	for memberKey, m := range node.Members {
		if m != nil && m.Descent != nil && m.Descent.Venue == "ssh" {
			add(spec.VmDomainIdentity(memberKey))
		}
	}
	sort.Strings(out)
	return out
}

// AcquireVmDomainLock takes a BLOCKING, host-global advisory lock serializing every check bed
// that occupies the given libvirt domain. Host-global (under ~/.cache/charly/.locks/) because
// the qemu:///session domain namespace is host-wide, shared across project dirs.
func AcquireVmDomainLock(domain string) (func() error, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".cache", "charly", ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return AcquireFileLock(filepath.Join(dir, "vm-domain-"+domain+".lock"), true)
}
