package vm

// YAML ↔ libvirt XML bridge.
//
// Converts opencharly's YAML-facing LibvirtDomain (authored in vm.yml
// as the `libvirt:` stanza) into libvirt.org/go/libvirtxml's Domain —
// the type system used to marshal the actual libvirt domain XML.
//
// Mapping rules (approved plan):
//   Rule 1: YAML keys = libvirt XML element/attribute names, snake_cased.
//   Rule 2: repeated elements → YAML sequences; singletons → maps.
//   Rule 3: mixed-content (attrs + text) uses `value` for the text.
//   Rule 4: booleans normalized to "yes"/"no" at marshal time.
//   Rule 5: 7 named divergences (accel3d scalar, channels/channel alias,
//           listen scalar, autoport string, network.mode shortcut,
//           memballoon.model scalar, rng.backend scalar). Handled
//           explicitly in the mappers below.
//   Rule 6: xml_passthrough merges verbatim libvirt XML fragments
//           into the rendered domain.
//
// Entry point: RenderDomainXML(spec, rt) — replaces the legacy
// RenderDomain string-assembly renderer. Callers: vm_create_spec.go.
//
// Rare libvirt features (HyperV enlightenments, KVM paravirt flags,
// detailed SysInfo per-section entries, NUMA cells, launch-security
// nuance) are intentionally not first-class in the structured YAML.
// Users reach them via xml_passthrough, which keeps the bridge
// focused and avoids chasing libvirtxml schema drift for marginal
// features.

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"text/template"

	libvirtxml "libvirt.org/go/libvirtxml"
)

// RenderDomainXML produces the complete libvirt domain XML for a VM
// from a VmSpec plus runtime parameters. Builds a libvirtxml.Domain
// tree and marshals it. Pure function — no filesystem, network, or
// libvirt calls.
func RenderDomainXML(spec *VmSpec, rt VmRuntimeParams) (string, error) {
	dom, err := BuildLibvirtDomainXML(spec, rt)
	if err != nil {
		return "", fmt.Errorf("building domain XML: %w", err)
	}
	out, err := xml.MarshalIndent(dom, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling domain XML: %w", err)
	}
	xmlStr := string(out) + "\n"
	// Egress is validated HOST-SIDE — the out-of-process plugin must not carry the egress
	// subsystem. runVmSpecCreate's two-phase ValidateOnly create renders + RETURNS this XML, the
	// host runs the real ValidateXMLEgress, then authorizes create (charly/vm_create_spec.go).
	return xmlStr, nil
}

// BuildLibvirtDomainXML builds a libvirtxml.Domain tree. Exposed so
// callers (e.g. `charly check libvirt passwd`) can mutate the tree before
// marshaling.
func BuildLibvirtDomainXML(spec *VmSpec, rt VmRuntimeParams) (*libvirtxml.Domain, error) {
	d := &libvirtxml.Domain{
		Type: "kvm",
		Name: rt.Name,
		Memory: &libvirtxml.DomainMemory{
			Value: uint(rt.RamMB),
			Unit:  "MiB",
		},
		CurrentMemory: &libvirtxml.DomainCurrentMemory{
			Value: uint(rt.RamMB),
			Unit:  "MiB",
		},
		VCPU: &libvirtxml.DomainVCPU{
			Value:     uint(rt.Cpus),
			Placement: "static",
		},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
	}

	var lv *LibvirtDomain
	if spec.Libvirt != nil {
		lv = spec.Libvirt
	}

	if lv != nil && lv.IOThreads > 0 {
		d.IOThreads = uint(lv.IOThreads)
	}
	if lv != nil && lv.CPUTune != nil {
		d.CPUTune = mapCPUTune(lv.CPUTune)
	}
	if lv != nil && lv.MemTune != nil {
		d.MemoryTune = mapMemTune(lv.MemTune)
	}
	if lv != nil && lv.NUMATune != nil {
		d.NUMATune = mapNUMATune(lv.NUMATune)
	}
	if lv != nil && lv.MemoryBacking != nil {
		d.MemoryBacking = mapMemoryBacking(lv.MemoryBacking)
	}

	d.OS = buildDomainOS(spec, rt)
	d.Features = buildDomainFeatures(lv)
	d.CPU = buildDomainCPU(spec, rt)
	d.Clock = buildDomainClock(lv)
	d.Devices = buildDomainDevices(spec, rt)
	ensureVirtiofsSharedMemory(d)
	ensureVirtiofsIdmap(d)

	if lv != nil && lv.SecLabel != nil {
		d.SecLabel = []libvirtxml.DomainSecLabel{mapSecLabel(lv.SecLabel)}
	}
	if lv != nil && lv.LaunchSecurity != nil && lv.LaunchSecurity.Type != "" {
		d.LaunchSecurity = mapLaunchSecurity(lv.LaunchSecurity)
	}
	if si := buildDomainSysInfo(spec, rt); si != nil {
		d.SysInfo = []libvirtxml.DomainSysInfo{*si}
		// Expose our <sysinfo> as the guest's SMBIOS tables. Without
		// <os><smbios mode='sysinfo'/> QEMU defines the OEM strings but never
		// presents them to the guest's DMI, so systemd-creds/systemd-tmpfiles
		// never see the `tmpfiles.extra` SSH-key credential — the entire SMBIOS
		// key-injection channel is silently dead (the deploy then depends solely
		// on cloud-init). This pairs the directive with the credential so SMBIOS
		// injection actually works (and stays authoritative, per KeyToUserTmpfilesD).
		if d.OS != nil {
			d.OS.SMBios = &libvirtxml.DomainSMBios{Mode: "sysinfo"}
		}
	}
	if lv != nil && lv.Resource != nil {
		d.Resource = mapResource(lv.Resource)
	}

	if lv != nil && lv.XMLPassthrough != "" {
		if err := mergeXMLPassthrough(d, lv.XMLPassthrough); err != nil {
			return nil, fmt.Errorf("xml_passthrough: %w", err)
		}
	}

	// Classification metadata (see /charly-internals:disposable). Per schema v3,
	// disposability is a deploy property, not a spec property. libvirt
	// domain XML no longer encodes a disposable flag sourced from the
	// VM spec — callers that want the flag visible in `virsh dumpxml`
	// can mount it via XMLPassthrough. Lifecycle tag is equally a
	// deploy property now; no emission from spec.

	return d, nil
}

// ---------------- OS ----------------

func buildDomainOS(spec *VmSpec, rt VmRuntimeParams) *libvirtxml.DomainOS {
	arch := rt.HostArch
	if arch == "" {
		arch = "x86_64"
	}
	machine := spec.Machine
	if machine == "" {
		machine = defaultMachineForArch(arch)
	}
	// firmware is materialized to its #Vm schema default ("bios") by
	// applyCueDefaults at the resolve point (vm_create_spec.go) — no Go fallback.
	firmware := spec.Firmware

	os := &libvirtxml.DomainOS{
		Type: &libvirtxml.DomainOSType{
			Type:    "hvm",
			Arch:    arch,
			Machine: machine,
		},
		BootDevices: []libvirtxml.DomainBootDevice{{Dev: "hd"}},
	}

	switch firmware {
	case "uefi-insecure", "uefi-secure":
		os.Firmware = "efi"
		fi := &libvirtxml.DomainOSFirmwareInfo{}
		if firmware == "uefi-secure" {
			fi.Features = []libvirtxml.DomainOSFirmwareFeature{
				{Name: "secure-boot", Enabled: "yes"},
				{Name: "enrolled-keys", Enabled: "yes"},
			}
		} else {
			fi.Features = []libvirtxml.DomainOSFirmwareFeature{
				{Name: "secure-boot", Enabled: "no"},
			}
		}
		os.FirmwareInfo = fi

		if rt.OVMFCodePath != "" {
			secure := "no"
			if firmware == "uefi-secure" {
				secure = "yes"
			}
			os.Loader = &libvirtxml.DomainLoader{
				Readonly: "yes",
				Secure:   secure,
				Type:     "pflash",
				Path:     rt.OVMFCodePath,
			}
		}
		if rt.NVRAMPath != "" {
			os.NVRam = &libvirtxml.DomainNVRam{NVRam: rt.NVRAMPath}
		}
	}

	return os
}

// ---------------- Features ----------------

func buildDomainFeatures(lv *LibvirtDomain) *libvirtxml.DomainFeatureList {
	fl := &libvirtxml.DomainFeatureList{}
	f := &LibvirtFeatures{}
	if lv != nil && lv.Features != nil {
		f = lv.Features
	}

	if boolPtrDefaultTrue(f.ACPI) {
		fl.ACPI = &libvirtxml.DomainFeature{}
	}
	if boolPtrDefaultTrue(f.APIC) {
		fl.APIC = &libvirtxml.DomainFeatureAPIC{}
	}
	if boolPtrTrue(f.PAE) {
		fl.PAE = &libvirtxml.DomainFeature{}
	}
	if boolPtrTrue(f.SMM) {
		fl.SMM = &libvirtxml.DomainFeatureSMM{State: "on"}
	}
	if boolPtrTrue(f.HAP) {
		fl.HAP = &libvirtxml.DomainFeatureState{State: "on"}
	}
	if boolPtrTrue(f.VMPort) {
		fl.VMPort = &libvirtxml.DomainFeatureState{State: "on"}
	}
	if boolPtrTrue(f.PMU) {
		fl.PMU = &libvirtxml.DomainFeatureState{State: "on"}
	}
	if kvm := mapKVMFeature(f.KVM); kvm != nil {
		fl.KVM = kvm
	}
	if hv := mapHyperVFeature(f.HyperV); hv != nil {
		fl.HyperV = hv
	}
	if f.IBS != "" {
		fl.IBS = &libvirtxml.DomainFeatureIBS{Value: f.IBS}
	}
	return fl
}

// stateFeature renders a `<elem state='on|off'/>` feature, nil when unset.
func stateFeature(s string) *libvirtxml.DomainFeatureState {
	if s == "" {
		return nil
	}
	return &libvirtxml.DomainFeatureState{State: s}
}

// mapKVMFeature renders <kvm>...</kvm>. The load-bearing field for NVIDIA
// consumer-GPU passthrough is <hidden state='on'/> (the classic Code-43
// workaround); the rest are para-virt perf knobs.
func mapKVMFeature(k *LibvirtKVM) *libvirtxml.DomainFeatureKVM {
	if k == nil {
		return nil
	}
	kvm := &libvirtxml.DomainFeatureKVM{
		Hidden:        stateFeature(k.Hidden),
		HintDedicated: stateFeature(k.HintDedicated),
		PollControl:   stateFeature(k.PollControl),
		PVIPI:         stateFeature(k.PVIPI),
	}
	if k.DirtyRingSize > 0 {
		kvm.DirtyRing = &libvirtxml.DomainFeatureKVMDirtyRing{
			DomainFeatureState: libvirtxml.DomainFeatureState{State: "on"},
			Size:               uint(k.DirtyRingSize),
		}
	}
	if *kvm == (libvirtxml.DomainFeatureKVM{}) {
		return nil
	}
	return kvm
}

// mapHyperVFeature renders <hyperv>. Only vendor_id is mapped here — the
// Code-43-relevant piece (`<vendor_id state='on' value='...'/>` hides the KVM
// signature from the NVIDIA driver). Other HyperV enlightenments (Windows-guest
// perf knobs, irrelevant to a Linux GPU guest) stay available via libvirt.snippets.
func mapHyperVFeature(h *LibvirtHyperV) *libvirtxml.DomainFeatureHyperV {
	if h == nil || h.VendorID == nil {
		return nil
	}
	vid := &libvirtxml.DomainFeatureHyperVVendorId{Value: h.VendorID.Value}
	vid.State = h.VendorID.State
	return &libvirtxml.DomainFeatureHyperV{VendorId: vid}
}

// ---------------- CPU ----------------

func buildDomainCPU(spec *VmSpec, rt VmRuntimeParams) *libvirtxml.DomainCPU {
	cpu := resolveCPUDefaults(spec, rt)
	out := &libvirtxml.DomainCPU{
		Mode:       cpu.Mode,
		Check:      cpu.Check,
		Migratable: cpu.Migratable,
	}
	if cpu.Model != "" {
		out.Model = &libvirtxml.DomainCPUModel{Value: cpu.Model}
	}
	if cpu.Topology != nil {
		out.Topology = &libvirtxml.DomainCPUTopology{
			Sockets: cpu.Topology.Sockets,
			Dies:    cpu.Topology.Dies,
			Cores:   cpu.Topology.Cores,
			Threads: cpu.Topology.Threads,
		}
	}
	for _, f := range cpu.Features {
		out.Features = append(out.Features, libvirtxml.DomainCPUFeature{
			Policy: f.Policy,
			Name:   f.Name,
		})
	}
	if cpu.Cache != nil {
		out.Cache = &libvirtxml.DomainCPUCache{
			Level: uint(cpu.Cache.Level),
			Mode:  cpu.Cache.Mode,
		}
	}
	// NUMA cells: not mapped in the bridge — use xml_passthrough if needed.
	return out
}

// ---------------- Clock ----------------

func buildDomainClock(lv *LibvirtDomain) *libvirtxml.DomainClock {
	c := &LibvirtClock{Offset: "utc"}
	if lv != nil && lv.Clock != nil {
		c = lv.Clock
		if c.Offset == "" {
			c.Offset = "utc"
		}
	}
	out := &libvirtxml.DomainClock{
		Offset:     c.Offset,
		TimeZone:   c.Timezone,
		Adjustment: c.Adjustment,
		Basis:      c.Basis,
	}
	for _, t := range c.Timers {
		timer := libvirtxml.DomainTimer{
			Name:       t.Name,
			Present:    t.Present,
			Track:      t.Track,
			TickPolicy: t.TickPolicy,
			Mode:       t.Mode,
			Frequency:  uint64(t.Frequency),
		}
		out.Timer = append(out.Timer, timer)
	}
	return out
}

// ---------------- MemoryBacking / MemTune / NUMATune / CPUTune ----------------

// ensureVirtiofsSharedMemory makes any virtiofs share startable. virtiofs
// shares the guest's RAM with the virtiofsd process, which requires
// <memoryBacking><source type='memfd'/><access mode='shared'/></memoryBacking>;
// without it libvirt refuses to start the domain with a cryptic error. We
// auto-pair it for any virtiofs filesystem so authors never have to remember
// the coupling. An explicitly-declared backing (e.g. hugepages) is honored —
// only the missing source/access bits are filled in.
func ensureVirtiofsSharedMemory(d *libvirtxml.Domain) {
	if d == nil || d.Devices == nil {
		return
	}
	hasVirtiofs := false
	for _, fs := range d.Devices.Filesystems {
		if fs.Driver != nil && fs.Driver.Type == "virtiofs" {
			hasVirtiofs = true
			break
		}
	}
	if !hasVirtiofs {
		return
	}
	if d.MemoryBacking == nil {
		d.MemoryBacking = &libvirtxml.DomainMemoryBacking{}
	}
	if d.MemoryBacking.MemorySource == nil {
		d.MemoryBacking.MemorySource = &libvirtxml.DomainMemorySource{Type: "memfd"}
	}
	if d.MemoryBacking.MemoryAccess == nil {
		d.MemoryBacking.MemoryAccess = &libvirtxml.DomainMemoryAccess{Mode: "shared"}
	}
}

// defaultGuestUserID is the conventional uid/gid of the first interactive
// user a cloud/bootstrap VM creates (cloud-init, pacstrap, debootstrap all
// number the primary account 1000). The guest-user virtiofs idmap maps THIS
// guest id to the host operator so the share is owned by the guest's
// interactive user.
const defaultGuestUserID = 1000

// ensureVirtiofsIdmap auto-injects a guest-user-owned <idmap> onto every
// passthrough virtiofs share that doesn't already declare one. libvirt's
// DEFAULT rootless idmap maps guest-root → the host operator, so a host-home
// passthrough share is owned by root inside the guest and the interactive
// guest user (uid 1000 by convention) gets EACCES — exactly the "/workspace
// is mounted but cachy can't read it" footgun. Mapping the guest's primary
// user to the host operator instead makes the share usable by the guest user,
// which is what "mount my home into the VM" means in practice. An
// author-declared idmap, a non-passthrough accessmode, or a missing host
// subordinate-ID range all leave libvirt's own default untouched.
func ensureVirtiofsIdmap(d *libvirtxml.Domain) {
	if !domainHasUnmappedPassthroughVirtiofs(d) {
		return
	}
	hostUID := os.Getuid()
	hostGID := os.Getgid()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	subUIDStart, subUIDCount, okU := subIDRange("/etc/subuid", username, hostUID)
	subGIDStart, subGIDCount, okG := subIDRange("/etc/subgid", username, hostGID)
	if !okU || !okG {
		// No subordinate-ID range configured for this user — rootless
		// virtiofs can't remap arbitrary ids anyway; leave libvirt's
		// default idmap in place.
		return
	}
	uidMap := guestOwnerIDMap(defaultGuestUserID, hostUID, subUIDStart, subUIDCount)
	gidMap := guestOwnerIDMap(defaultGuestUserID, hostGID, subGIDStart, subGIDCount)
	applyVirtiofsIdmap(d, uidMap, gidMap)
}

// domainHasUnmappedPassthroughVirtiofs reports whether the domain has at least
// one passthrough virtiofs share that does not yet declare an <idmap>.
func domainHasUnmappedPassthroughVirtiofs(d *libvirtxml.Domain) bool {
	if d == nil || d.Devices == nil {
		return false
	}
	for i := range d.Devices.Filesystems {
		fs := &d.Devices.Filesystems[i]
		if fs.Driver != nil && fs.Driver.Type == "virtiofs" &&
			(fs.AccessMode == "" || fs.AccessMode == "passthrough") && fs.IDMap == nil {
			return true
		}
	}
	return false
}

// applyVirtiofsIdmap sets the given uid/gid maps on every passthrough virtiofs
// share that doesn't already declare one. No-op when either map is nil
// (unbuildable partition) so a bad host config never produces a broken idmap.
func applyVirtiofsIdmap(d *libvirtxml.Domain, uidMap, gidMap []libvirtxml.DomainFilesystemIDMapEntry) {
	if d == nil || d.Devices == nil || uidMap == nil || gidMap == nil {
		return
	}
	for i := range d.Devices.Filesystems {
		fs := &d.Devices.Filesystems[i]
		if fs.Driver == nil || fs.Driver.Type != "virtiofs" {
			continue
		}
		if fs.AccessMode != "" && fs.AccessMode != "passthrough" {
			continue
		}
		if fs.IDMap != nil {
			continue // author-declared idmap wins
		}
		fs.IDMap = &libvirtxml.DomainFilesystemIDMap{UID: uidMap, GID: gidMap}
	}
}

// guestOwnerIDMap builds the three-segment filesystem idmap that maps the
// guest's primary id (guestID) to the host operator (hostID) and every other
// guest id into the operator's subordinate-ID range [subStart, subStart+
// subCount). libvirt idmap semantics: each entry maps guest `start` → host
// `target` for `count` ids. Returns nil when guestID falls outside the
// mappable range (the caller then leaves libvirt's default in place).
//
//	guest 0 .. guestID-1        → subStart .. subStart+guestID-1
//	guest guestID               → hostID                          (the operator)
//	guest guestID+1 .. subCount → subStart+guestID .. subStart+subCount-1
func guestOwnerIDMap(guestID, hostID, subStart, subCount int) []libvirtxml.DomainFilesystemIDMapEntry {
	if guestID < 1 || guestID >= subCount || subStart < 0 {
		return nil
	}
	return []libvirtxml.DomainFilesystemIDMapEntry{
		{Start: 0, Target: uint(subStart), Count: uint(guestID)},
		{Start: uint(guestID), Target: uint(hostID), Count: 1},
		{Start: uint(guestID + 1), Target: uint(subStart + guestID), Count: uint(subCount - guestID)},
	}
}

// subIDRange reads /etc/subuid or /etc/subgid and returns the [start, count)
// subordinate-ID range allocated to the user (matched by name OR uid). ok is
// false when the file is unreadable or the user has no entry.
func subIDRange(path, username string, uid int) (start, count int, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	uidStr := strconv.Itoa(uid)
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) != 3 {
			continue
		}
		if fields[0] != username && fields[0] != uidStr {
			continue
		}
		s, err1 := strconv.Atoi(fields[1])
		c, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || c <= 0 {
			continue
		}
		return s, c, true
	}
	return 0, 0, false
}

func mapMemoryBacking(mb *LibvirtMemoryBacking) *libvirtxml.DomainMemoryBacking {
	out := &libvirtxml.DomainMemoryBacking{}
	if mb.Hugepages != nil {
		hp := &libvirtxml.DomainMemoryHugepages{}
		page := libvirtxml.DomainMemoryHugepage{
			Size:    sizeValue(mb.Hugepages.Size),
			Unit:    sizeUnit(mb.Hugepages.Size),
			Nodeset: mb.Hugepages.NodeSet,
		}
		hp.Hugepages = append(hp.Hugepages, page)
		out.MemoryHugePages = hp
	}
	if boolPtrTrue(mb.NoSharepages) {
		out.MemoryNosharepages = &libvirtxml.DomainMemoryNosharepages{}
	}
	if boolPtrTrue(mb.Locked) {
		out.MemoryLocked = &libvirtxml.DomainMemoryLocked{}
	}
	if mb.Source != "" {
		out.MemorySource = &libvirtxml.DomainMemorySource{Type: mb.Source}
	}
	if mb.Access != "" {
		out.MemoryAccess = &libvirtxml.DomainMemoryAccess{Mode: mb.Access}
	}
	if mb.Allocation != "" {
		out.MemoryAllocation = &libvirtxml.DomainMemoryAllocation{Mode: mb.Allocation}
	}
	if boolPtrTrue(mb.Discard) {
		out.MemoryDiscard = &libvirtxml.DomainMemoryDiscard{}
	}
	return out
}

func mapMemTune(mt *LibvirtMemTune) *libvirtxml.DomainMemoryTune {
	out := &libvirtxml.DomainMemoryTune{}
	if mt.HardLimit != "" {
		out.HardLimit = &libvirtxml.DomainMemoryTuneLimit{Value: sizeKiB(mt.HardLimit), Unit: "KiB"}
	}
	if mt.SoftLimit != "" {
		out.SoftLimit = &libvirtxml.DomainMemoryTuneLimit{Value: sizeKiB(mt.SoftLimit), Unit: "KiB"}
	}
	if mt.SwapHardLimit != "" {
		out.SwapHardLimit = &libvirtxml.DomainMemoryTuneLimit{Value: sizeKiB(mt.SwapHardLimit), Unit: "KiB"}
	}
	if mt.MinGuarantee != "" {
		out.MinGuarantee = &libvirtxml.DomainMemoryTuneLimit{Value: sizeKiB(mt.MinGuarantee), Unit: "KiB"}
	}
	return out
}

func mapNUMATune(nt *LibvirtNUMATune) *libvirtxml.DomainNUMATune {
	out := &libvirtxml.DomainNUMATune{}
	if nt.Memory != nil {
		out.Memory = &libvirtxml.DomainNUMATuneMemory{
			Mode:      nt.Memory.Mode,
			Nodeset:   nt.Memory.Nodeset,
			Placement: nt.Memory.Placement,
		}
	}
	for _, mn := range nt.MemNodes {
		out.MemNodes = append(out.MemNodes, libvirtxml.DomainNUMATuneMemNode{
			CellID:  uint(mn.CellID),
			Mode:    mn.Mode,
			Nodeset: mn.Nodeset,
		})
	}
	return out
}

func mapCPUTune(ct *LibvirtCPUTune) *libvirtxml.DomainCPUTune {
	out := &libvirtxml.DomainCPUTune{}
	if ct.Shares > 0 {
		out.Shares = &libvirtxml.DomainCPUTuneShares{Value: uint(ct.Shares)}
	}
	if ct.Period > 0 {
		out.Period = &libvirtxml.DomainCPUTunePeriod{Value: uint64(ct.Period)}
	}
	if ct.Quota > 0 {
		out.Quota = &libvirtxml.DomainCPUTuneQuota{Value: int64(ct.Quota)}
	}
	if ct.GlobalPeriod > 0 {
		out.GlobalPeriod = &libvirtxml.DomainCPUTunePeriod{Value: uint64(ct.GlobalPeriod)}
	}
	if ct.GlobalQuota > 0 {
		out.GlobalQuota = &libvirtxml.DomainCPUTuneQuota{Value: int64(ct.GlobalQuota)}
	}
	if ct.EmulatorPeriod > 0 {
		out.EmulatorPeriod = &libvirtxml.DomainCPUTunePeriod{Value: uint64(ct.EmulatorPeriod)}
	}
	if ct.EmulatorQuota > 0 {
		out.EmulatorQuota = &libvirtxml.DomainCPUTuneQuota{Value: int64(ct.EmulatorQuota)}
	}
	if ct.IOThreadPeriod > 0 {
		out.IOThreadPeriod = &libvirtxml.DomainCPUTunePeriod{Value: uint64(ct.IOThreadPeriod)}
	}
	if ct.IOThreadQuota > 0 {
		out.IOThreadQuota = &libvirtxml.DomainCPUTuneQuota{Value: int64(ct.IOThreadQuota)}
	}
	for _, p := range ct.VCPUPin {
		out.VCPUPin = append(out.VCPUPin, libvirtxml.DomainCPUTuneVCPUPin{
			VCPU:   uint(p.VCPU),
			CPUSet: p.CPUSet,
		})
	}
	if ct.EmulatorPin != nil {
		out.EmulatorPin = &libvirtxml.DomainCPUTuneEmulatorPin{CPUSet: ct.EmulatorPin.CPUSet}
	}
	for _, p := range ct.IOThreadPin {
		out.IOThreadPin = append(out.IOThreadPin, libvirtxml.DomainCPUTuneIOThreadPin{
			IOThread: uint(p.IOThread),
			CPUSet:   p.CPUSet,
		})
	}
	return out
}

// ---------------- SecLabel / LaunchSecurity / Resource / SysInfo ----------------

func mapSecLabel(s *LibvirtSecLabel) libvirtxml.DomainSecLabel {
	return libvirtxml.DomainSecLabel{
		Type:       s.Type,
		Model:      s.Model,
		Relabel:    s.Relabel,
		Label:      s.Label,
		BaseLabel:  s.BaseLabel,
		ImageLabel: s.ImageLabel,
	}
}

// mapLaunchSecurity covers the most common confidential-VM types.
// Exotic detail (CBitPos, DhCert, Session, kernel-hashes) uses
// xml_passthrough when needed.
func mapLaunchSecurity(ls *LibvirtLaunchSecurity) *libvirtxml.DomainLaunchSecurity {
	switch ls.Type {
	case "sev":
		sev := &libvirtxml.DomainLaunchSecuritySEV{
			Policy:       hexUintPtr(ls.Policy),
			DHCert:       ls.DhCert,
			Session:      ls.Session,
			KernelHashes: ls.KernelHashes,
		}
		return &libvirtxml.DomainLaunchSecurity{SEV: sev}
	case "sev-es":
		// libvirtxml doesn't have a distinct SEV-ES type; emit as SEV.
		sev := &libvirtxml.DomainLaunchSecuritySEV{
			Policy:       hexUintPtr(ls.Policy),
			KernelHashes: ls.KernelHashes,
		}
		return &libvirtxml.DomainLaunchSecurity{SEV: sev}
	case "sev-snp":
		snp := &libvirtxml.DomainLaunchSecuritySEVSNP{
			Policy:       hexU64Ptr(ls.Policy),
			KernelHashes: ls.KernelHashes,
		}
		return &libvirtxml.DomainLaunchSecurity{SEVSNP: snp}
	case "s390-pv":
		return &libvirtxml.DomainLaunchSecurity{S390PV: &libvirtxml.DomainLaunchSecurityS390PV{}}
	case "tdx":
		tdx := &libvirtxml.DomainLaunchSecurityTDX{
			Policy: hexUintPtr(ls.Policy),
		}
		return &libvirtxml.DomainLaunchSecurity{TDX: tdx}
	}
	return nil
}

func mapResource(r *LibvirtResource) *libvirtxml.DomainResource {
	out := &libvirtxml.DomainResource{Partition: r.Partition}
	if fc, ok := r.FibreChannel["appid"]; ok {
		out.FibreChannel = &libvirtxml.DomainResourceFibreChannel{AppID: fc}
	}
	return out
}

// buildDomainSysInfo builds the SysInfo SMBIOS block. Per-section
// (BIOS, System, BaseBoard, Chassis, Processor) entries are supported
// via their distinct libvirtxml types.
func buildDomainSysInfo(spec *VmSpec, rt VmRuntimeParams) *libvirtxml.DomainSysInfo {
	var user *LibvirtSysInfo
	if spec.Libvirt != nil {
		user = spec.Libvirt.SysInfo
	}
	var oem []string
	oem = append(oem, rt.SMBIOSCredentials...)
	if user != nil {
		oem = append(oem, user.OEMStrings...)
	}
	hasContent := len(oem) > 0 ||
		(user != nil && (len(user.BIOS) > 0 || len(user.System) > 0 ||
			len(user.BaseBoard) > 0 || len(user.Chassis) > 0 || len(user.Processor) > 0))
	if !hasContent {
		return nil
	}

	smb := &libvirtxml.DomainSysInfoSMBIOS{}
	if user != nil {
		if len(user.BIOS) > 0 {
			smb.BIOS = &libvirtxml.DomainSysInfoBIOS{Entry: mapSysInfoEntries(user.BIOS)}
		}
		if len(user.System) > 0 {
			smb.System = &libvirtxml.DomainSysInfoSystem{Entry: mapSysInfoEntries(user.System)}
		}
		for _, bb := range user.BaseBoard {
			if entries := mapSysInfoEntries(bb); entries != nil {
				smb.BaseBoard = append(smb.BaseBoard, libvirtxml.DomainSysInfoBaseBoard{Entry: entries})
			}
		}
		if len(user.Chassis) > 0 {
			smb.Chassis = &libvirtxml.DomainSysInfoChassis{Entry: mapSysInfoEntries(user.Chassis)}
		}
		for _, pr := range user.Processor {
			if entries := mapSysInfoEntries(pr); entries != nil {
				smb.Processor = append(smb.Processor, libvirtxml.DomainSysInfoProcessor{Entry: entries})
			}
		}
	}
	if len(oem) > 0 {
		os := &libvirtxml.DomainSysInfoOEMStrings{}
		os.Entry = append(os.Entry, oem...)
		smb.OEMStrings = os
	}
	return &libvirtxml.DomainSysInfo{SMBIOS: smb}
}

func mapSysInfoEntries(m map[string]string) []libvirtxml.DomainSysInfoEntry {
	if len(m) == 0 {
		return nil
	}
	out := make([]libvirtxml.DomainSysInfoEntry, 0, len(m))
	for k, v := range m {
		out = append(out, libvirtxml.DomainSysInfoEntry{Name: k, Value: v})
	}
	return out
}

// ---------------- Devices ----------------

//nolint:gocyclo // libvirt device-list builder (20+ device types via if-nil-append); cohesive inventory; per-device extraction bloats and obscures the taxonomy
func buildDomainDevices(spec *VmSpec, rt VmRuntimeParams) *libvirtxml.DomainDeviceList {
	out := &libvirtxml.DomainDeviceList{}
	var lvd *LibvirtDevices
	if spec.Libvirt != nil {
		lvd = spec.Libvirt.Devices
	}

	if lvd != nil && lvd.Emulator != "" {
		out.Emulator = lvd.Emulator
	}

	// Auto-synthesized root disk.
	if rt.QCOW2Path != "" {
		out.Disks = append(out.Disks, libvirtxml.DomainDisk{
			Device: "disk",
			Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "qcow2"},
			Source: &libvirtxml.DomainDiskSource{
				File: &libvirtxml.DomainDiskSourceFile{File: rt.QCOW2Path},
			},
			Target: &libvirtxml.DomainDiskTarget{Dev: "vda", Bus: "virtio"},
		})
	}
	// Auto-synthesized seed ISO cdrom.
	if rt.SeedISOPath != "" {
		out.Disks = append(out.Disks, libvirtxml.DomainDisk{
			Device:   "cdrom",
			Driver:   &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "raw"},
			Source:   &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: rt.SeedISOPath}},
			Target:   &libvirtxml.DomainDiskTarget{Dev: "sda", Bus: "sata"},
			ReadOnly: &libvirtxml.DomainDiskReadOnly{},
		})
	}
	if lvd != nil {
		for _, d := range lvd.Disks {
			out.Disks = append(out.Disks, mapDisk(d))
		}
	}

	// Auto-synthesized default interface.
	out.Interfaces = append(out.Interfaces, buildDefaultInterface(spec, rt))
	if lvd != nil {
		for _, iface := range lvd.Interfaces {
			out.Interfaces = append(out.Interfaces, mapInterface(iface))
		}
	}

	// Auto-synthesized serial + console for `charly vm console`.
	out.Serials = append(out.Serials, libvirtxml.DomainSerial{
		Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
		Target: &libvirtxml.DomainSerialTarget{Port: new(uint)},
	})
	out.Consoles = append(out.Consoles, libvirtxml.DomainConsole{
		Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
		Target: &libvirtxml.DomainConsoleTarget{Type: "serial", Port: new(uint)},
	})

	if lvd != nil {
		for _, ch := range lvd.Channels {
			out.Channels = append(out.Channels, mapChannel(ch, rt))
		}
		for _, s := range lvd.Serial {
			out.Serials = append(out.Serials, mapSerial(s))
		}
		for _, c := range lvd.Console {
			out.Consoles = append(out.Consoles, mapConsole(c))
		}
		for _, p := range lvd.Parallel {
			out.Parallels = append(out.Parallels, mapParallel(p))
		}
		for _, g := range lvd.Graphics {
			out.Graphics = append(out.Graphics, mapGraphics(g))
		}
		for _, v := range lvd.Video {
			out.Videos = append(out.Videos, mapVideo(v))
		}
		for _, a := range lvd.Audio {
			out.Audios = append(out.Audios, mapAudio(a))
		}
		for _, s := range lvd.Sound {
			out.Sounds = append(out.Sounds, libvirtxml.DomainSound{Model: s.Model})
		}
		for _, i := range lvd.Inputs {
			out.Inputs = append(out.Inputs, libvirtxml.DomainInput{Type: i.Type, Bus: i.Bus})
		}
		for _, u := range lvd.USB {
			out.Controllers = append(out.Controllers, libvirtxml.DomainController{
				Type:  "usb",
				Model: u.Model,
			})
		}
		for _, r := range lvd.RedirDev {
			out.RedirDevs = append(out.RedirDevs, mapRedirDev(r))
		}
		for _, h := range lvd.Hostdevs {
			if hd := mapHostdev(h); hd != nil {
				out.Hostdevs = append(out.Hostdevs, *hd)
			}
		}
		for _, f := range lvd.Filesystems {
			out.Filesystems = append(out.Filesystems, mapFilesystem(f))
		}
		for _, r := range lvd.RNG {
			out.RNGs = append(out.RNGs, mapRNG(r))
		}
		for _, t := range lvd.TPM {
			out.TPMs = append(out.TPMs, mapTPM(t))
		}
		for _, w := range lvd.Watchdog {
			out.Watchdogs = append(out.Watchdogs, libvirtxml.DomainWatchdog{Model: w.Model, Action: w.Action})
		}
		if lvd.MemBalloon != nil {
			out.MemBalloon = mapMemBalloon(lvd.MemBalloon)
		}
		for _, s := range lvd.Shmem {
			out.Shmems = append(out.Shmems, mapShmem(s))
		}
		if lvd.IOMMU != nil {
			out.IOMMU = mapIOMMU(lvd.IOMMU)
		}
		if lvd.Vsock != nil {
			out.VSock = mapVsock(lvd.Vsock)
		}
		for _, p := range lvd.Panic {
			out.Panics = append(out.Panics, libvirtxml.DomainPanic{Model: p.Model})
		}
		for _, sc := range lvd.Smartcard {
			out.Smartcards = append(out.Smartcards, mapSmartcard(sc))
		}
		for _, h := range lvd.Hub {
			out.Hubs = append(out.Hubs, libvirtxml.DomainHub{Type: h.Type})
		}
	}

	return out
}

func buildDefaultInterface(spec *VmSpec, rt VmRuntimeParams) libvirtxml.DomainInterface {
	net := &VmNetwork{Mode: "user", Model: "virtio-net-pci"}
	if spec.Network != nil {
		net = spec.Network
		if net.Mode == "" {
			net.Mode = "user"
		}
		if net.Model == "" {
			net.Model = "virtio-net-pci"
		}
	}

	out := libvirtxml.DomainInterface{
		Model: &libvirtxml.DomainInterfaceModel{Type: net.Model},
	}
	if net.MAC != "" {
		out.MAC = &libvirtxml.DomainInterfaceMAC{Address: net.MAC}
	}

	switch net.Mode {
	case "bridge":
		out.Source = &libvirtxml.DomainInterfaceSource{
			Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: net.Bridge},
		}
	case "nat", "network":
		source := net.Bridge
		if source == "" {
			source = "default"
		}
		out.Source = &libvirtxml.DomainInterfaceSource{
			Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: source},
		}
	default: // user
		out.Source = &libvirtxml.DomainInterfaceSource{User: &libvirtxml.DomainInterfaceSourceUser{}}
		if rt.SshPort > 0 || len(net.PortForwards) > 0 || len(rt.ExtraPortForwards) > 0 {
			out.Backend = &libvirtxml.DomainInterfaceBackend{Type: "passt"}
			var ranges []libvirtxml.DomainInterfaceSourcePortForwardRange
			if rt.SshPort > 0 {
				ranges = append(ranges, libvirtxml.DomainInterfaceSourcePortForwardRange{
					Start: uint(rt.SshPort),
					To:    uint(22),
				})
			}
			for _, pf := range net.PortForwards {
				if r := parsePortForwardRange(pf); r != nil {
					ranges = append(ranges, *r)
				}
			}
			for _, pf := range rt.ExtraPortForwards {
				if r := parsePortForwardRange(pf); r != nil {
					ranges = append(ranges, *r)
				}
			}
			if len(ranges) > 0 {
				out.PortForward = []libvirtxml.DomainInterfaceSourcePortForward{{
					Proto: "tcp",
					// Bind host-side forwards to loopback ONLY (security): a VM
					// port must never be exposed on 0.0.0.0 / the LAN — the same
					// 127.0.0.1 default podman uses for published pod ports. Host
					// tooling (charly vm ssh, charly bundle add vm:, host-net peers) reaches
					// the VM via 127.0.0.1; nothing off-host can.
					Address: vmForwardBindAddr,
					Ranges:  ranges,
				}}
			}
		}
	}
	return out
}

func parsePortForwardRange(pf string) *libvirtxml.DomainInterfaceSourcePortForwardRange {
	host, guest := splitPortForward(pf)
	if host == "" || guest == "" {
		return nil
	}
	var hi, gi int
	if _, err := fmt.Sscanf(host, "%d", &hi); err != nil || hi <= 0 {
		return nil
	}
	if _, err := fmt.Sscanf(guest, "%d", &gi); err != nil || gi <= 0 {
		return nil
	}
	return &libvirtxml.DomainInterfaceSourcePortForwardRange{
		Start: uint(hi),
		To:    uint(gi),
	}
}

// ---------------- Per-device mappers ----------------

func mapDisk(d LibvirtDisk) libvirtxml.DomainDisk {
	dType := d.Type
	if dType == "" {
		dType = "file"
	}
	dev := d.Device
	if dev == "" {
		dev = "disk"
	}
	out := libvirtxml.DomainDisk{Device: dev}
	if len(d.Driver) > 0 {
		drv := &libvirtxml.DomainDiskDriver{}
		drv.Name = d.Driver["name"]
		drv.Type = d.Driver["type"]
		drv.Cache = d.Driver["cache"]
		drv.IO = d.Driver["io"]
		drv.Discard = d.Driver["discard"]
		drv.ErrorPolicy = d.Driver["error_policy"]
		if s := d.Driver["iothread"]; s != "" {
			if p := uintPtrOrNil(s); p != nil {
				drv.IOThread = p
			}
		}
		out.Driver = drv
	}
	if len(d.Source) > 0 {
		src := &libvirtxml.DomainDiskSource{}
		switch dType {
		case "file":
			src.File = &libvirtxml.DomainDiskSourceFile{File: d.Source["file"]}
		case "block":
			src.Block = &libvirtxml.DomainDiskSourceBlock{Dev: d.Source["dev"]}
		case "volume":
			src.Volume = &libvirtxml.DomainDiskSourceVolume{Pool: d.Source["pool"], Volume: d.Source["volume"]}
		case "network":
			src.Network = &libvirtxml.DomainDiskSourceNetwork{
				Protocol: d.Source["protocol"],
				Name:     d.Source["name"],
			}
		default:
			src.File = &libvirtxml.DomainDiskSourceFile{File: d.Source["file"]}
		}
		out.Source = src
	}
	if len(d.Target) > 0 {
		out.Target = &libvirtxml.DomainDiskTarget{
			Dev: d.Target["dev"],
			Bus: d.Target["bus"],
		}
	}
	if boolPtrTrue(d.Readonly) {
		out.ReadOnly = &libvirtxml.DomainDiskReadOnly{}
	}
	out.Serial = d.Serial
	out.WWN = d.WWN
	if d.Boot > 0 {
		out.Boot = &libvirtxml.DomainDeviceBoot{Order: uint(d.Boot)}
	}
	return out
}

func mapInterface(iface LibvirtInterface) libvirtxml.DomainInterface {
	t := iface.Type
	if t == "" {
		t = "user"
	}
	out := libvirtxml.DomainInterface{}
	if iface.Model != "" {
		out.Model = &libvirtxml.DomainInterfaceModel{Type: iface.Model}
	}
	if iface.MAC != "" {
		out.MAC = &libvirtxml.DomainInterfaceMAC{Address: iface.MAC}
	}
	if iface.MTU > 0 {
		out.MTU = &libvirtxml.DomainInterfaceMTU{Size: uint(iface.MTU)}
	}

	switch t {
	case "bridge":
		out.Source = &libvirtxml.DomainInterfaceSource{
			Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: iface.Source["bridge"]},
		}
	case "network":
		out.Source = &libvirtxml.DomainInterfaceSource{
			Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: iface.Source["network"]},
		}
	case "direct":
		out.Source = &libvirtxml.DomainInterfaceSource{
			Direct: &libvirtxml.DomainInterfaceSourceDirect{Dev: iface.Source["dev"], Mode: iface.Source["mode"]},
		}
	case "user":
		out.Source = &libvirtxml.DomainInterfaceSource{User: &libvirtxml.DomainInterfaceSourceUser{}}
	}
	if iface.Boot > 0 {
		out.Boot = &libvirtxml.DomainDeviceBoot{Order: uint(iface.Boot)}
	}
	if len(iface.PortForwards) > 0 {
		ranges := make([]libvirtxml.DomainInterfaceSourcePortForwardRange, 0, len(iface.PortForwards))
		for _, pf := range iface.PortForwards {
			r := libvirtxml.DomainInterfaceSourcePortForwardRange{Start: uint(pf.Start)}
			if pf.To > 0 {
				r.To = uint(pf.To)
			}
			ranges = append(ranges, r)
		}
		proto := "tcp"
		if iface.PortForwards[0].Proto != "" {
			proto = iface.PortForwards[0].Proto
		}
		// Loopback-only bind (security) — see vmForwardBindAddr.
		out.PortForward = []libvirtxml.DomainInterfaceSourcePortForward{{Proto: proto, Address: vmForwardBindAddr, Ranges: ranges}}
	}
	return out
}

// vmForwardBindAddr is the host address VM passt/user-mode port forwards bind to.
// 127.0.0.1 (loopback) ONLY — a VM port is never exposed on 0.0.0.0 / the LAN,
// mirroring podman's default 127.0.0.1 for published pod ports. Host tooling and
// host-net peers reach the VM via loopback; nothing off-host can.
const vmForwardBindAddr = "127.0.0.1"

func mapChannel(ch LibvirtChannel, rt VmRuntimeParams) libvirtxml.DomainChannel {
	out := libvirtxml.DomainChannel{}
	switch {
	case ch.Type == "spicevmc":
		out.Source = &libvirtxml.DomainChardevSource{SpiceVMC: &libvirtxml.DomainChardevSourceSpiceVMC{}}
	case ch.Source != "" || ch.Path != "":
		path := ch.Source
		if path == "" {
			path = ch.Path
		}
		path = expandVmPathTemplate(path, rt)
		out.Source = &libvirtxml.DomainChardevSource{
			UNIX: &libvirtxml.DomainChardevSourceUNIX{Mode: "bind", Path: path},
		}
	case ch.Type == "unix":
		// unix channel with no explicit path — the qemu-guest-agent idiom.
		// Bind a libvirt-managed socket: with mode=bind and no path, libvirt
		// auto-assigns the socket under the domain's per-VM lib dir, and the
		// channel renders type="unix" from the UNIX source presence.
		out.Source = &libvirtxml.DomainChardevSource{
			UNIX: &libvirtxml.DomainChardevSourceUNIX{Mode: "bind"},
		}
	}
	out.Target = &libvirtxml.DomainChannelTarget{VirtIO: &libvirtxml.DomainChannelTargetVirtIO{Name: ch.Name}}
	return out
}

// expandVmPathTemplate substitutes Go-template variables in a libvirt
// path attribute. Supports {{.VmStateDir}} and {{.VmName}}; passes
// templates that error or contain no `{{` through unchanged so the
// fast path stays free.
//
// Authored as a defensive measure against literal `/home/<user>/...`
// paths in libvirt snippets — see /charly-internals:libvirt-renderer for the
// design rationale and the prior R10 incident where a hardcoded
// `/home/user/...` qga.sock path blocked libvirt-backend boot for
// every user not literally named "user".
func expandVmPathTemplate(path string, rt VmRuntimeParams) string {
	if path == "" || !strings.Contains(path, "{{") {
		return path
	}
	t, err := template.New("vmpath").Parse(path)
	if err != nil {
		return path
	}
	var buf strings.Builder
	if err := t.Execute(&buf, struct {
		VmStateDir string
		VmName     string
	}{
		VmStateDir: rt.VmStateDir,
		VmName:     rt.Name,
	}); err != nil {
		return path
	}
	return buf.String()
}

func mapSerial(s LibvirtSerial) libvirtxml.DomainSerial {
	out := libvirtxml.DomainSerial{Source: mapChardevSource(s.Type, s.Source)}
	if len(s.Target) > 0 {
		t := &libvirtxml.DomainSerialTarget{}
		t.Type = s.Target["type"]
		if v := s.Target["port"]; v != "" {
			t.Port = uintPtrOrNil(v)
		}
		out.Target = t
	}
	return out
}

func mapConsole(c LibvirtConsole) libvirtxml.DomainConsole {
	out := libvirtxml.DomainConsole{Source: mapChardevSource("pty", nil)}
	if len(c.Target) > 0 {
		t := &libvirtxml.DomainConsoleTarget{}
		t.Type = c.Target["type"]
		if v := c.Target["port"]; v != "" {
			t.Port = uintPtrOrNil(v)
		}
		out.Target = t
	}
	return out
}

func mapParallel(p LibvirtParallel) libvirtxml.DomainParallel {
	return libvirtxml.DomainParallel{Source: mapChardevSource(p.Type, p.Source)}
}

func mapChardevSource(typ string, src map[string]string) *libvirtxml.DomainChardevSource {
	switch typ {
	case "", "pty":
		return &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}}
	case "unix":
		return &libvirtxml.DomainChardevSource{UNIX: &libvirtxml.DomainChardevSourceUNIX{
			Path: src["path"],
			Mode: src["mode"],
		}}
	case "tcp":
		return &libvirtxml.DomainChardevSource{TCP: &libvirtxml.DomainChardevSourceTCP{
			Host:    src["host"],
			Mode:    src["mode"],
			Service: src["service"],
		}}
	case "file":
		return &libvirtxml.DomainChardevSource{File: &libvirtxml.DomainChardevSourceFile{Path: src["path"]}}
	case "dev":
		return &libvirtxml.DomainChardevSource{Dev: &libvirtxml.DomainChardevSourceDev{Path: src["path"]}}
	case "spicevmc":
		return &libvirtxml.DomainChardevSource{SpiceVMC: &libvirtxml.DomainChardevSourceSpiceVMC{}}
	}
	return &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}}
}

// mapGraphics handles divergence #3 (listen scalar/map/list → one or
// more <listen> children). The accepted YAML shapes are documented
// on LibvirtGraphicsListeners in libvirt_yaml_listen.go.
func mapGraphics(g LibvirtGraphics) libvirtxml.DomainGraphic {
	out := libvirtxml.DomainGraphic{}
	listeners := buildGraphicsListeners(g.Listen)
	switch g.Type {
	case "vnc":
		vnc := &libvirtxml.DomainGraphicVNC{
			Port:     g.Port,
			AutoPort: g.AutoPort,
			Passwd:   g.Passwd,
			Keymap:   g.Keymap,
		}
		if len(listeners) > 0 {
			vnc.Listeners = listeners
			// Populate the deprecated scalar attribute only for a
			// single address-type listener; libvirt accepts both.
			if len(listeners) == 1 && listeners[0].Address != nil {
				vnc.Listen = listeners[0].Address.Address
			}
		}
		out.VNC = vnc
	case "spice":
		spice := &libvirtxml.DomainGraphicSpice{
			Port:     g.Port,
			AutoPort: g.AutoPort,
			Passwd:   g.Passwd,
			Keymap:   g.Keymap,
		}
		if len(listeners) > 0 {
			spice.Listeners = listeners
		}
		if g.GL != "" {
			spice.GL = &libvirtxml.DomainGraphicSpiceGL{Enable: g.GL}
		}
		out.Spice = spice
	case "rdp":
		out.RDP = &libvirtxml.DomainGraphicRDP{
			Port:     g.Port,
			AutoPort: g.AutoPort,
		}
	case "sdl":
		out.SDL = &libvirtxml.DomainGraphicSDL{}
	case "egl-headless":
		out.EGLHeadless = &libvirtxml.DomainGraphicEGLHeadless{}
	}
	return out
}

// buildGraphicsListeners renders each LibvirtGraphicsListen into a
// libvirtxml.DomainGraphicListener. Empty listener lists produce nil;
// unknown types are skipped with no XML emission (the YAML unmarshaler
// rejects them up front, so this branch is defensive).
func buildGraphicsListeners(ll LibvirtGraphicsListeners) []libvirtxml.DomainGraphicListener {
	if len(ll) == 0 {
		return nil
	}
	out := make([]libvirtxml.DomainGraphicListener, 0, len(ll))
	for _, l := range ll {
		switch l.Type {
		case "address", "":
			out = append(out, libvirtxml.DomainGraphicListener{
				Address: &libvirtxml.DomainGraphicListenerAddress{Address: l.Address},
			})
		case "socket":
			out = append(out, libvirtxml.DomainGraphicListener{
				Socket: &libvirtxml.DomainGraphicListenerSocket{Socket: l.Socket},
			})
		case "network":
			out = append(out, libvirtxml.DomainGraphicListener{
				Network: &libvirtxml.DomainGraphicListenerNetwork{Network: l.Network},
			})
		}
	}
	return out
}

// mapVideo handles divergence #1 (accel3d scalar bool → nested <acceleration accel3d="yes|no"/>).
func mapVideo(v LibvirtVideo) libvirtxml.DomainVideo {
	out := libvirtxml.DomainVideo{
		Model: libvirtxml.DomainVideoModel{
			Type:    v.Model,
			VRam:    uint(v.VRAM),
			Heads:   uint(v.Heads),
			Primary: boolPtrToYesNo(v.Primary),
		},
	}
	if v.Accel3D != nil {
		out.Model.Accel = &libvirtxml.DomainVideoAccel{
			Accel3D: boolPtrToYesNo(v.Accel3D),
		}
	}
	return out
}

func mapAudio(a LibvirtAudio) libvirtxml.DomainAudio {
	out := libvirtxml.DomainAudio{ID: a.ID}
	switch a.Type {
	case "none":
		out.None = &libvirtxml.DomainAudioNone{}
	case "alsa":
		out.ALSA = &libvirtxml.DomainAudioALSA{}
	case "coreaudio":
		out.CoreAudio = &libvirtxml.DomainAudioCoreAudio{}
	case "oss":
		out.OSS = &libvirtxml.DomainAudioOSS{}
	case "pulseaudio", "pulse":
		out.PulseAudio = &libvirtxml.DomainAudioPulseAudio{}
	case "spice":
		out.SPICE = &libvirtxml.DomainAudioSPICE{}
	case "file":
		out.File = &libvirtxml.DomainAudioFile{}
	case "jack":
		out.Jack = &libvirtxml.DomainAudioJack{}
	default:
		out.None = &libvirtxml.DomainAudioNone{}
	}
	return out
}

func mapRedirDev(r LibvirtRedirDev) libvirtxml.DomainRedirDev {
	bus := r.Bus
	if bus == "" {
		bus = "usb"
	}
	typ := r.Type
	if typ == "" {
		typ = "spicevmc"
	}
	return libvirtxml.DomainRedirDev{
		Bus:    bus,
		Source: mapChardevSource(typ, nil),
	}
}

func mapHostdev(h LibvirtHostdev) *libvirtxml.DomainHostdev {
	mode := h.Mode
	if mode == "" {
		mode = "subsystem"
	}
	if mode != "subsystem" {
		return nil
	}
	out := &libvirtxml.DomainHostdev{Managed: h.Managed}
	switch h.Type {
	case "pci":
		addr := pciAddress(h.Source)
		if addr == nil {
			return nil
		}
		out.SubsysPCI = &libvirtxml.DomainHostdevSubsysPCI{
			Source: &libvirtxml.DomainHostdevSubsysPCISource{Address: addr},
			Driver: mapHostdevPCIDriver(h.Driver),
		}
	case "usb":
		sub := &libvirtxml.DomainHostdevSubsysUSB{
			Source: &libvirtxml.DomainHostdevSubsysUSBSource{},
		}
		if v, ok := h.Source["vendor"]; ok {
			sub.Source.Vendor = &libvirtxml.DomainHostDevProductVendorID{ID: v}
		}
		if p, ok := h.Source["product"]; ok {
			sub.Source.Product = &libvirtxml.DomainHostDevProductVendorID{ID: p}
		}
		out.SubsysUSB = sub
	case "scsi":
		out.SubsysSCSI = &libvirtxml.DomainHostdevSubsysSCSI{}
	case "mdev":
		out.SubsysMDev = &libvirtxml.DomainHostdevSubsysMDev{Model: h.Source["model"]}
	default:
		return nil
	}
	// ROM passthrough applies regardless of subsystem type (commonly
	// `<rom bar='off'/>` for a secondary GPU, or `file=` for a dumped VBIOS).
	out.ROM = mapHostdevROM(h.ROM)
	return out
}

// mapHostdevROM renders the optional <rom .../> element from the YAML rom map
// (keys: bar | file | enabled). Returns nil when no rom config is present.
func mapHostdevROM(rom map[string]string) *libvirtxml.DomainROM {
	if len(rom) == 0 {
		return nil
	}
	out := &libvirtxml.DomainROM{Bar: rom["bar"], Enabled: rom["enabled"]}
	if f, ok := rom["file"]; ok {
		out.File = &f
	}
	return out
}

// mapHostdevPCIDriver renders the optional <driver .../> element on a PCI
// hostdev (keys: name | model | iommufd). `name: vfio` is the usual value.
func mapHostdevPCIDriver(drv map[string]string) *libvirtxml.DomainHostdevSubsysPCIDriver {
	if len(drv) == 0 {
		return nil
	}
	return &libvirtxml.DomainHostdevSubsysPCIDriver{
		Name:    drv["name"],
		Model:   drv["model"],
		IommuFD: drv["iommufd"],
	}
}

func pciAddress(src map[string]string) *libvirtxml.DomainAddressPCI {
	if src == nil {
		return nil
	}
	return &libvirtxml.DomainAddressPCI{
		Domain:   hexUintPtr(src["domain"]),
		Bus:      hexUintPtr(src["bus"]),
		Slot:     hexUintPtr(src["slot"]),
		Function: hexUintPtr(src["function"]),
	}
}

func mapFilesystem(f LibvirtFilesystem) libvirtxml.DomainFilesystem {
	typ := f.Type
	if typ == "" {
		typ = "mount"
	}
	out := libvirtxml.DomainFilesystem{
		AccessMode: f.AccessMode,
	}
	if f.Driver != "" {
		out.Driver = &libvirtxml.DomainFilesystemDriver{Type: f.Driver}
	}
	if f.Source != "" {
		src := &libvirtxml.DomainFilesystemSource{}
		switch typ {
		case "mount":
			src.Mount = &libvirtxml.DomainFilesystemSourceMount{Dir: f.Source}
		case "block":
			src.Block = &libvirtxml.DomainFilesystemSourceBlock{Dev: f.Source}
		case "file":
			src.File = &libvirtxml.DomainFilesystemSourceFile{File: f.Source}
		case "template":
			src.Template = &libvirtxml.DomainFilesystemSourceTemplate{Name: f.Source}
		}
		out.Source = src
	}
	if f.Target != "" {
		out.Target = &libvirtxml.DomainFilesystemTarget{Dir: f.Target}
	}
	if boolPtrTrue(f.Readonly) {
		out.ReadOnly = &libvirtxml.DomainFilesystemReadOnly{}
	}
	if b := mapFilesystemBinary(f.Binary); b != nil {
		out.Binary = b
	}
	return out
}

// mapFilesystemBinary renders the optional virtiofsd <binary> knobs. Useful
// for rootless qemu:///session where the daemon path or sandbox mode may need
// pinning (e.g. sandbox: chroot when the namespace sandbox is unavailable).
// Returns nil when no knobs are set so the element is omitted.
func mapFilesystemBinary(m map[string]string) *libvirtxml.DomainFilesystemBinary {
	if len(m) == 0 {
		return nil
	}
	out := &libvirtxml.DomainFilesystemBinary{}
	set := false
	if v := m["path"]; v != "" {
		out.Path = v
		set = true
	}
	if v := m["xattr"]; v != "" {
		out.XAttr = v
		set = true
	}
	if v := m["cache"]; v != "" {
		out.Cache = &libvirtxml.DomainFilesystemBinaryCache{Mode: v}
		set = true
	}
	if v := m["sandbox"]; v != "" {
		out.Sandbox = &libvirtxml.DomainFilesystemBinarySandbox{Mode: v}
		set = true
	}
	if v := m["thread_pool"]; v != "" {
		out.ThreadPool = &libvirtxml.DomainFilesystemBinaryThreadPool{Size: uintFromStr(v)}
		set = true
	}
	if !set {
		return nil
	}
	return out
}

// mapRNG handles divergence #7 (rng.backend scalar path → nested <backend model="random">/path</backend>).
func mapRNG(r LibvirtRNG) libvirtxml.DomainRNG {
	model := r.Model
	if model == "" {
		model = "virtio"
	}
	out := libvirtxml.DomainRNG{Model: model}
	if r.Backend != "" {
		out.Backend = &libvirtxml.DomainRNGBackend{
			Random: &libvirtxml.DomainRNGBackendRandom{
				Device: r.Backend,
			},
		}
	}
	if p, ok := r.Rate["period"]; ok {
		out.Rate = &libvirtxml.DomainRNGRate{Period: uintFromStr(p)}
		if b, ok := r.Rate["bytes"]; ok {
			out.Rate.Bytes = uintFromStr(b)
		}
	}
	return out
}

func mapTPM(t LibvirtTPM) libvirtxml.DomainTPM {
	model := t.Model
	if model == "" {
		model = "tpm-crb"
	}
	out := libvirtxml.DomainTPM{Model: model}
	if typ, ok := t.Backend["type"]; ok {
		backend := &libvirtxml.DomainTPMBackend{}
		switch typ {
		case "emulator":
			backend.Emulator = &libvirtxml.DomainTPMBackendEmulator{
				Version: t.Backend["version"],
			}
		case "passthrough":
			backend.Passthrough = &libvirtxml.DomainTPMBackendPassthrough{
				Device: &libvirtxml.DomainTPMBackendDevice{
					Path: t.Backend["path"],
				},
			}
		}
		out.Backend = backend
	}
	return out
}

// mapMemBalloon handles divergence #6 (memballoon.model scalar → <memballoon model="..."/>).
func mapMemBalloon(m *LibvirtMemBalloon) *libvirtxml.DomainMemBalloon {
	out := &libvirtxml.DomainMemBalloon{
		Model:       m.Model,
		AutoDeflate: m.Autodeflate,
	}
	if period, ok := m.Stats["period"]; ok {
		out.Stats = &libvirtxml.DomainMemBalloonStats{Period: uint(period)}
	}
	return out
}

func mapShmem(s LibvirtShmem) libvirtxml.DomainShmem {
	out := libvirtxml.DomainShmem{Name: s.Name, Role: s.Role}
	if len(s.Model) > 0 {
		out.Model = &libvirtxml.DomainShmemModel{Type: s.Model["type"]}
	}
	if s.Size != "" {
		out.Size = &libvirtxml.DomainShmemSize{Value: uint(parseIntOr(s.Size, 0)), Unit: "M"}
	}
	return out
}

func mapIOMMU(i *LibvirtIOMMU) *libvirtxml.DomainIOMMU {
	out := &libvirtxml.DomainIOMMU{Model: i.Model}
	return out
}

func mapVsock(v *LibvirtVsock) *libvirtxml.DomainVSock {
	model := v.Model
	if model == "" {
		model = "virtio"
	}
	out := &libvirtxml.DomainVSock{Model: model}
	if len(v.CID) > 0 {
		out.CID = &libvirtxml.DomainVSockCID{Auto: v.CID["auto"], Address: v.CID["address"]}
	}
	return out
}

func mapSmartcard(s LibvirtSmartcard) libvirtxml.DomainSmartcard {
	out := libvirtxml.DomainSmartcard{}
	switch s.Mode {
	case "host":
		out.Host = &libvirtxml.DomainSmartcardHost{}
	case "passthrough":
		out.Passthrough = &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}}
	}
	return out
}

// ---------------- xml_passthrough ----------------

// mergeXMLPassthrough parses each XML fragment in the passthrough
// string and merges its contents into the main Domain. Fragments are
// wrapped in <domain>…</domain> for libvirtxml parsing, then their
// non-nil child fields are appended into the target.
//
// The merge is additive: repeated elements concatenate; singleton
// elements are set if the target doesn't have one yet.
func mergeXMLPassthrough(d *libvirtxml.Domain, passthrough string) error {
	passthrough = strings.TrimSpace(passthrough)
	if passthrough == "" {
		return nil
	}
	wrapped := "<domain>" + passthrough + "</domain>"
	frag := &libvirtxml.Domain{}
	if err := xml.Unmarshal([]byte(wrapped), frag); err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if d.LaunchSecurity == nil && frag.LaunchSecurity != nil {
		d.LaunchSecurity = frag.LaunchSecurity
	}
	if d.GenID == nil && frag.GenID != nil {
		d.GenID = frag.GenID
	}
	if d.IDMap == nil && frag.IDMap != nil {
		d.IDMap = frag.IDMap
	}
	if d.PM == nil && frag.PM != nil {
		d.PM = frag.PM
	}
	if d.Perf == nil && frag.Perf != nil {
		d.Perf = frag.Perf
	}
	if d.KeyWrap == nil && frag.KeyWrap != nil {
		d.KeyWrap = frag.KeyWrap
	}

	if frag.Devices != nil && d.Devices != nil {
		d.Devices.Disks = append(d.Devices.Disks, frag.Devices.Disks...)
		d.Devices.Interfaces = append(d.Devices.Interfaces, frag.Devices.Interfaces...)
		d.Devices.Channels = append(d.Devices.Channels, frag.Devices.Channels...)
		d.Devices.Serials = append(d.Devices.Serials, frag.Devices.Serials...)
		d.Devices.Consoles = append(d.Devices.Consoles, frag.Devices.Consoles...)
		d.Devices.Parallels = append(d.Devices.Parallels, frag.Devices.Parallels...)
		d.Devices.Graphics = append(d.Devices.Graphics, frag.Devices.Graphics...)
		d.Devices.Videos = append(d.Devices.Videos, frag.Devices.Videos...)
		d.Devices.Audios = append(d.Devices.Audios, frag.Devices.Audios...)
		d.Devices.Sounds = append(d.Devices.Sounds, frag.Devices.Sounds...)
		d.Devices.Inputs = append(d.Devices.Inputs, frag.Devices.Inputs...)
		d.Devices.Controllers = append(d.Devices.Controllers, frag.Devices.Controllers...)
		d.Devices.Hostdevs = append(d.Devices.Hostdevs, frag.Devices.Hostdevs...)
		d.Devices.RedirDevs = append(d.Devices.RedirDevs, frag.Devices.RedirDevs...)
		d.Devices.Smartcards = append(d.Devices.Smartcards, frag.Devices.Smartcards...)
		d.Devices.Hubs = append(d.Devices.Hubs, frag.Devices.Hubs...)
		d.Devices.RNGs = append(d.Devices.RNGs, frag.Devices.RNGs...)
		d.Devices.TPMs = append(d.Devices.TPMs, frag.Devices.TPMs...)
		d.Devices.Watchdogs = append(d.Devices.Watchdogs, frag.Devices.Watchdogs...)
		d.Devices.Shmems = append(d.Devices.Shmems, frag.Devices.Shmems...)
		d.Devices.Panics = append(d.Devices.Panics, frag.Devices.Panics...)
		d.Devices.Filesystems = append(d.Devices.Filesystems, frag.Devices.Filesystems...)
		if d.Devices.MemBalloon == nil && frag.Devices.MemBalloon != nil {
			d.Devices.MemBalloon = frag.Devices.MemBalloon
		}
		if d.Devices.IOMMU == nil && frag.Devices.IOMMU != nil {
			d.Devices.IOMMU = frag.Devices.IOMMU
		}
		if d.Devices.VSock == nil && frag.Devices.VSock != nil {
			d.Devices.VSock = frag.Devices.VSock
		}
	}

	d.SysInfo = append(d.SysInfo, frag.SysInfo...)
	d.SecLabel = append(d.SecLabel, frag.SecLabel...)
	return nil
}

// ---------------- Small helpers ----------------

func uintPtrOrNil(s string) *uint {
	if s == "" {
		return nil
	}
	v := uintFromStr(s)
	return &v
}

func uintFromStr(s string) uint {
	var n uint
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func parseIntOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

// sizeValue extracts the numeric portion of sizes like "2M", "1G".
func sizeValue(s string) uint {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	last := s[len(s)-1]
	if (last >= 'A' && last <= 'Z') || (last >= 'a' && last <= 'z') {
		s = s[:len(s)-1]
	}
	return uintFromStr(s)
}

func sizeUnit(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	last := s[len(s)-1]
	switch last {
	case 'T', 't':
		return "T"
	case 'G', 'g':
		return "G"
	case 'M', 'm':
		return "M"
	case 'K', 'k':
		return "K"
	}
	return ""
}

func sizeKiB(s string) uint64 {
	v := sizeValue(s)
	mult := uint64(1)
	switch sizeUnit(s) {
	case "T":
		mult = 1024 * 1024 * 1024
	case "G":
		mult = 1024 * 1024
	case "M":
		mult = 1024
	case "K", "":
		mult = 1
	}
	return uint64(v) * mult
}

func hexUintPtr(s string) *uint {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var n uint64
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if _, err := fmt.Sscanf(s[2:], "%x", &n); err != nil {
			return nil
		}
	} else {
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
			return nil
		}
	}
	v := uint(n)
	return &v
}

func hexU64Ptr(s string) *uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var n uint64
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if _, err := fmt.Sscanf(s[2:], "%x", &n); err != nil {
			return nil
		}
	} else {
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
			return nil
		}
	}
	return &n
}
