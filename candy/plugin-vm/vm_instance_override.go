package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// VmInstanceOverride is the operator-side per-libvirt-domain override
// file. Lives at ~/.local/share/charly/vm/<domain-name>/instance.yml — a
// sibling to the SSH key, NVRAM, and console socket already kept
// there. The presence of this file lets an operator override the
// project-level VM classification for their specific instance
// without modifying the project's charly.yml or vm.yml.
//
// The override carries `disposable:` / `lifecycle:` (the two fields
// that gate `charly update <vm-entity>`) AND a `libvirt:` block — a
// per-host libvirt device overlay, using the SAME schema as a
// `kind: vm` entity's `libvirt:` block, merged into the VmSpec at
// `charly vm create`. This is where HOST-SPECIFIC device config lives:
// a PCI `<hostdev>` (the GPU's bus/slot address is host-specific) and
// a virtiofs `<filesystem>` share rooted at an absolute host path
// (e.g. /home/<operator>). Keeping these in the home overlay — never
// the committed `vm.yml` — lets the project's VM entities stay PORTABLE
// (no PCI address, no operator-home path baked into version control)
// while this host attaches its real GPU + shares for a live run. Future
// fields (per-instance ports, env, add_candy) can be added without
// breaking the on-disk format because yaml.v3 unknown-keys defaults
// to forgiving.
//
// Pointer to bool for Disposable lets the loader distinguish "field
// absent → use upstream classification" from "field set to false →
// override-explicit no". A bare bool would conflate the two.
//
// Use case: project charly.yml's arch-vm has disposable: true. An
// operator who wants to use the arch-vm bed for a long-running
// experiment can write
//
//	~/.local/share/charly/vm/charly-arch/instance.yml:
//	  disposable: false
//	  lifecycle: long-running
//
// and the AUTONOMOUS rebuild path (the check-runner / R10 discipline)
// then treats the domain as non-disposable and skips it — protecting
// the experiment from an unattended destroy, with no need to edit the
// project's charly.yml or stash a different lifecycle tag in version
// control. (An explicit `charly update arch` still obeys, printing a
// transparency note: the disposable flag gates autonomy, not the verb.)
type VmInstanceOverride struct {
	// Disposable, when non-nil, overrides the upstream classification
	// for the libvirt domain at this path. Pointer-typed to
	// distinguish "absent" from "explicit false".
	Disposable *bool `yaml:"disposable,omitempty" json:"disposable,omitempty"`
	// Lifecycle, when non-empty, overrides the upstream lifecycle tag.
	Lifecycle string `yaml:"lifecycle,omitempty" json:"lifecycle,omitempty"`
	// Libvirt, when non-nil, is a per-host device overlay merged into the
	// VmSpec at create time (ApplyToVmSpec). Uses the SAME schema as a
	// `kind: vm` entity's `libvirt:` block, but only the host-specific
	// device categories are merged: `devices.hostdevs` (PCI passthrough —
	// the address is host-specific) and `devices.filesystems` (virtiofs
	// shares rooted at an absolute host path). Both APPEND to whatever the
	// portable repo `vm.yml` already declares, so the committed entity
	// carries no host-specific identity.
	Libvirt *LibvirtDomain `yaml:"libvirt,omitempty" json:"libvirt,omitempty"`
}

// VmInstanceOverridePath returns the canonical path for a domain's
// instance override file. The domainName is the libvirt/qemu
// domain identifier (typically "charly-<vmEntityName>").
func VmInstanceOverridePath(domainName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "charly", "vm", domainName, "instance.yml"), nil
}

// LoadVmInstanceOverride reads the per-domain override file and
// returns a parsed VmInstanceOverride. Returns (nil, nil) when the
// file doesn't exist (the common case — most operators don't
// override). Returns (nil, error) when the file exists but is
// unreadable or contains invalid YAML — silent fall-through there
// would mask real config problems.
func LoadVmInstanceOverride(domainName string) (*VmInstanceOverride, error) {
	path, err := VmInstanceOverridePath(domainName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var charly VmInstanceOverride
	if err := yaml.Unmarshal(data, &charly); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &charly, nil
}

// ApplyToVmClassification merges an override on top of an upstream
// (disposable, lifecycle) pair. When the override is nil or empty,
// returns the upstream pair unchanged. When set, the override wins.
//
// Centralised here so every caller (the `charly update` VM-entity path,
// future commands that want per-instance classification) gets the
// same semantics.
func (o *VmInstanceOverride) ApplyToVmClassification(disposable bool, lifecycle string) (bool, string) {
	if o == nil {
		return disposable, lifecycle
	}
	if o.Disposable != nil {
		disposable = *o.Disposable
	}
	if o.Lifecycle != "" {
		lifecycle = o.Lifecycle
	}
	return disposable, lifecycle
}

// ApplyToVmSpec merges the override's per-host `libvirt:` device overlay onto
// spec IN PLACE, called by runVmSpecCreate before the domain XML is rendered.
// Only the HOST-SPECIFIC device categories are merged — `devices.hostdevs`
// (PCI passthrough, a host-specific bus/slot address) and `devices.filesystems`
// (virtiofs shares rooted at an absolute host path) — and they APPEND to
// whatever the portable repo vm.yml already declares. This is what lets the
// committed `kind: vm` entity stay free of any PCI address or operator-home
// path: the project ships the portable shape, the operator's home overlay
// supplies this host's GPU + shares.
//
// Nil override or nil overlay → no-op (the common case). spec.Libvirt /
// spec.Libvirt.Devices are created on demand so an entity that declares no
// libvirt block at all still receives the overlay. Other libvirt fields in
// the overlay are intentionally ignored — host-specific config is exactly the
// passthrough device + the host-path share, nothing else.
func (o *VmInstanceOverride) ApplyToVmSpec(spec *VmSpec) {
	if o == nil || o.Libvirt == nil || o.Libvirt.Devices == nil || spec == nil {
		return
	}
	od := o.Libvirt.Devices
	if len(od.Hostdevs) == 0 && len(od.Filesystems) == 0 {
		return
	}
	if spec.Libvirt == nil {
		spec.Libvirt = &LibvirtDomain{}
	}
	if spec.Libvirt.Devices == nil {
		spec.Libvirt.Devices = &LibvirtDevices{}
	}
	d := spec.Libvirt.Devices
	d.Hostdevs = append(d.Hostdevs, od.Hostdevs...)
	d.Filesystems = append(d.Filesystems, od.Filesystems...)
}
