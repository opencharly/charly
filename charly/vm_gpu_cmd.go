package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// VmGpuCmd groups the host-side VFIO/GPU-passthrough inspection + mode verbs.
type VmGpuCmd struct {
	Status  VmGpuStatusCmd  `cmd:"" help:"Report host IOMMU readiness for GPU passthrough"`
	List    VmGpuListCmd    `cmd:"" help:"List passthrough-capable GPUs and emit a ready-to-paste hostdevs block"`
	Mode    VmGpuModeCmd    `cmd:"" help:"Show or set the GPU driver mode: vfio (VM passthrough) | nvidia (shared CDI pods)"`
	Plan    VmGpuPlanCmd    `cmd:"" help:"DRY-RUN: print the exact host commands a mode flip WOULD run, without touching sysfs"`
	Recover VmGpuRecoverCmd `cmd:"" help:"Recover a card left unbound/half-switched back to vfio-pci (refuses + reports reboot-required on a true device_lock wedge)"`
}

// VmGpuPlanCmd is the DRY-RUN preview of the vfio<->nvidia driver switch: it prints the EXACT
// host rebind commands `charly vm gpu mode <mode>` would run, WITHOUT touching sysfs (cutover C9,
// the driver-switch dispatch proof). It uses the vendor-matched card when one is present, else a
// documented synthetic example card — so it is completely cred/hardware-free and works on a
// GPU-less host (the check-step assertion the R10 bed makes).
type VmGpuPlanCmd struct {
	Mode   string `arg:"" optional:"" default:"vfio" help:"Mode to preview: vfio (passthrough) | nvidia (CDI pods). Default vfio."`
	Vendor string `long:"vendor" default:"0x10de" help:"PCI vendor of the GPU to preview (default 0x10de = NVIDIA); a synthetic example card is used when absent."`
}

func (c *VmGpuPlanCmd) Run() error {
	if c.Mode != gpuModeVfio && c.Mode != gpuModeNvidia {
		return fmt.Errorf("invalid mode %q — want %q or %q", c.Mode, gpuModeVfio, gpuModeNvidia)
	}
	var gptr *VFIOGpu
	if gpu, found := selectGPUByVendor(DetectVFIO(), c.Vendor); found {
		gptr = &gpu
	}
	plan, err := gpuSwitchPlan(gptr, c.Mode)
	if err != nil {
		return err
	}
	src := "the detected card"
	if gptr == nil {
		src = "a synthetic example card (no matching GPU on this host)"
	}
	fmt.Printf("# DRY RUN — the exact host commands `charly vm gpu mode %s` would run on %s.\n", c.Mode, src)
	fmt.Println("# NOTHING is executed; no sysfs write, no sudo. This is the driver-switch dispatch proof.")
	for _, ln := range plan {
		fmt.Println(ln)
	}
	return nil
}

// VmGpuModeCmd shows or sets the host GPU driver mode for a vendor-matched card.
// This is the manual operator interface for the vfio<->nvidia switch (the
// charly-CLI way, vs ad-hoc sysfs). The arbiter (charly/preempt.go) flips
// automatically for requires_exclusive (vfio) / requires_shared (nvidia)
// claims; this verb is the explicit override + inspector. It does NOT consult
// the arbiter ledger — a manual flip while an exclusive lease is active is the
// operator's call. The flip switches the WHOLE IOMMU group (display + audio),
// never just the display function (see candy/plugin-gpu/switch.go).
type VmGpuModeCmd struct {
	Mode   string `arg:"" optional:"" help:"Target mode: vfio (passthrough) | nvidia (CDI pods). Omit to SHOW the current mode."`
	Vendor string `long:"vendor" default:"0x10de" help:"PCI vendor of the GPU to switch (default 0x10de = NVIDIA)."`
}

func (c *VmGpuModeCmd) Run() error {
	if c.Mode != "" && c.Mode != gpuModeVfio && c.Mode != gpuModeNvidia {
		return fmt.Errorf("invalid mode %q — want %q or %q (or omit to show the current mode)", c.Mode, gpuModeVfio, gpuModeNvidia)
	}
	gpu, found := selectGPUByVendor(DetectVFIO(), c.Vendor)
	if !found {
		return fmt.Errorf("no GPU matching vendor %s on this host — check `charly vm gpu status`", normalizePCIVendor(c.Vendor))
	}
	cur := currentGPUMode(gpu)
	ids := trim0x(gpu.VendorID) + ":" + trim0x(gpu.DeviceID)
	if c.Mode == "" {
		fmt.Printf("%s (%s) GPU driver mode: %s\n", gpu.Addr, ids, cur)
		return nil
	}
	if groupInMode(gpu, c.Mode) {
		fmt.Printf("%s (%s) already in %s mode (whole IOMMU group)\n", gpu.Addr, ids, c.Mode)
		return nil
	}
	if err := switchGPUDriverMode(gpu, c.Mode); err != nil {
		if errors.Is(err, errGPUSwitchWedged) {
			fmt.Fprintf(os.Stderr, "%s: the switch WEDGED — a host reboot is required.\n", gpu.Addr)
		}
		return err
	}
	if c.Mode == gpuModeNvidia {
		ensureCDIRoot() // /etc/cdi is root-owned — generate the CDI spec as root
	}
	fmt.Printf("%s (%s) switched %s -> %s (whole IOMMU group)\n", gpu.Addr, ids, cur, c.Mode)
	return nil
}

// VmGpuRecoverCmd recovers a card left UNBOUND or half-switched (e.g. an
// interrupted flip) back to the clean vfio-pci default — but ONLY when the card
// is NOT wedged. A read-only probe detects a true device_lock wedge (an nvidia
// `.remove` stuck in D-state) FIRST; on a wedge it reports reboot-required and
// attempts NOTHING (a bind on a wedged device would add a second permanent
// D-state). On a healthy/unbound card it rebinds the whole IOMMU group to
// vfio-pci and clears any stale poison marker.
type VmGpuRecoverCmd struct {
	Vendor string `long:"vendor" default:"0x10de" help:"PCI vendor of the GPU to recover (default 0x10de = NVIDIA)."`
}

func (c *VmGpuRecoverCmd) Run() error {
	gpu, found := selectGPUByVendor(DetectVFIO(), c.Vendor)
	if !found {
		return fmt.Errorf("no GPU matching vendor %s on this host — check `charly vm gpu status`", normalizePCIVendor(c.Vendor))
	}
	// Read-only wedge probe FIRST — NEVER bind a wedged card (device_driver_attach
	// would D-state behind the stuck .remove = a second permanent wedge).
	if gpuWedgeDetected() {
		fmt.Printf("%s: WEDGED — the nvidia driver's .remove is stuck holding the device_lock.\n", gpu.Addr)
		fmt.Println("  Recovery is REBOOT-ONLY: no userspace operation can release a held device_lock or")
		fmt.Println("  abort an in-kernel driver callback. NOT attempting any bind (that would add a second")
		fmt.Println("  permanent D-state). Reboot the host to clear it.")
		return fmt.Errorf("GPU %s wedged (nvidia .remove holding the device_lock) — host reboot required", gpu.Addr)
	}
	if groupInMode(gpu, gpuModeVfio) {
		clearVendorPoison(c.Vendor)
		fmt.Printf("%s: healthy — all IOMMU-group functions already bound to vfio-pci. Nothing to recover.\n", gpu.Addr)
		return nil
	}
	fmt.Printf("%s: not fully on vfio-pci and NOT wedged — rebinding the whole IOMMU group to vfio-pci...\n", gpu.Addr)
	if err := switchGPUDriverMode(gpu, gpuModeVfio); err != nil {
		if errors.Is(err, errGPUSwitchWedged) {
			fmt.Fprintf(os.Stderr, "%s: the rebind WEDGED — a host reboot is required.\n", gpu.Addr)
		}
		return err
	}
	clearVendorPoison(c.Vendor)
	fmt.Printf("%s: recovered — all IOMMU-group functions bound to vfio-pci.\n", gpu.Addr)
	return nil
}

// gpuWedgeDetected (the read-only device_lock-wedge probe) moved to candy/plugin-gpu with the
// driver-switch (cutover C9); vm_gpu_cmd reaches it via the gpu_shim.go shim.

// clearVendorPoison removes the poison marker of every arbiter token whose gpu
// selector matches `vendor` — called after `charly vm gpu recover` verifies the
// card is healthy, so a later acquire is no longer refused.
func clearVendorPoison(vendor string) {
	want := normalizePCIVendor(vendor)
	a := newResourceArbiter()
	for tok, rdef := range gatherResources() {
		if rdef != nil && rdef.Gpu != nil && normalizePCIVendor(rdef.Gpu.Vendor) == want {
			a.clearPoison(tok)
		}
	}
}

// VmGpuStatusCmd reports whether the host is configured for VFIO passthrough.
type VmGpuStatusCmd struct{}

func (c *VmGpuStatusCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	rep := DetectVFIO()
	fmt.Println("VFIO / GPU passthrough status")
	fmt.Println()

	if rep.IOMMUEnabled {
		kind := rep.IOMMUKind
		if kind == "" {
			kind = "unknown vendor"
		}
		fmt.Printf("  IOMMU:           enabled (%s)\n", kind)
	} else {
		fmt.Println("  IOMMU:           DISABLED — add intel_iommu=on (Intel) or amd_iommu=on (AMD)")
		fmt.Println("                   plus iommu=pt to the kernel cmdline, then reboot.")
	}

	if vfioPciAvailable() {
		fmt.Println("  vfio-pci driver: available")
	} else {
		fmt.Println("  vfio-pci driver: NOT loaded — `sudo modprobe vfio-pci` (libvirt managed='yes' loads it on VM start)")
	}

	// memlock: VFIO pins all guest RAM, so a rootless qemu:///session needs a
	// memlock limit >= guest RAM. The login-session default (often 8 MiB) is
	// far too low and produces a cryptic "cannot limit locked memory" failure.
	_, hard := MemlockLimitBytes()
	switch {
	case memlockUnlimited(hard):
		fmt.Println("  memlock limit:   unlimited")
	case hard >= 16<<30:
		fmt.Printf("  memlock limit:   %d MiB (ok)\n", hard>>20)
	default:
		fmt.Printf("  memlock limit:   %d MiB — TOO LOW for passthrough (needs >= guest RAM).\n", hard>>20)
		fmt.Println("                   Raise it for the libvirt session, e.g. add to /etc/security/limits.d:")
		fmt.Println("                       <user> hard memlock unlimited")
		fmt.Println("                   then re-login, or `sudo prlimit --pid $(pgrep -x virtqemud) --memlock=unlimited`.")
	}

	fmt.Println()
	if len(rep.GPUs) == 0 {
		fmt.Println("  GPUs: none detected")
		return nil
	}
	poisoned := poisonedTokensByVendor()
	// Probe for a live device_lock wedge (a sudo /proc scan) ONLY when an nvidia
	// card looks unhealthy (not cleanly vfio/nvidia) or is poisoned — a clean
	// status read needs no sudo.
	suspect := len(poisoned) > 0
	for _, g := range rep.GPUs {
		if normalizePCIVendor(g.VendorID) == nvidiaVendorID && !groupInMode(g, gpuModeVfio) && !groupInMode(g, gpuModeNvidia) {
			suspect = true
		}
	}
	wedged := false
	if suspect {
		wedged = gpuWedgeDetected()
	}
	fmt.Println("  GPUs:")
	for _, g := range rep.GPUs {
		grp := "no IOMMU group"
		access := ""
		if g.IOMMUGroup >= 0 {
			grp = fmt.Sprintf("group %d (%d device(s))", g.IOMMUGroup, len(g.GroupMembers))
			if VfioGroupAccessible(g.IOMMUGroup) {
				access = "  /dev/vfio: rw"
			} else {
				access = fmt.Sprintf("  /dev/vfio/%d: NO ACCESS (run `charly udev install`)", g.IOMMUGroup)
			}
		}
		drv := g.Driver
		if drv == "" {
			drv = "unbound"
		}
		fmt.Printf("    %s  %s:%s  %s  driver=%s  %s%s\n",
			g.Addr, trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel, drv, grp, access)
		// Per-function driver of the WHOLE IOMMU group — the switch unit. A group
		// is VFIO-viable (guest-usable) only when EVERY member is on vfio-pci.
		for _, m := range g.GroupMembers {
			md := m.Driver
			if md == "" {
				md = "unbound"
			}
			fmt.Printf("        fn %s  %-22s driver=%s\n", m.Addr, m.ClassLabel, md)
		}
		fmt.Printf("        mode: %s\n", gpuGroupModeLabel(g))
		if wedged && normalizePCIVendor(g.VendorID) == nvidiaVendorID {
			fmt.Println("        *** WEDGED: an nvidia .remove is stuck holding the device_lock — host reboot REQUIRED")
			fmt.Println("            (no userspace recovery exists; `charly vm gpu recover` confirms but cannot fix it) ***")
		}
		if poisoned[normalizePCIVendor(g.VendorID)] {
			fmt.Println("        *** POISONED: a prior driver switch wedged this card — claims are refused until a host reboot")
			fmt.Println("            (`charly vm gpu recover` clears the marker once the card is verified healthy) ***")
		}
	}
	return nil
}

// gpuGroupModeLabel describes the live driver mode of a GPU's IOMMU group:
// "vfio" (all functions vfio-pci, passthrough-ready), "nvidia" (host/CDI:
// display on nvidia), a plain "host driver" label for a card on its own host
// driver (amdgpu/i915/… — the host's display GPU, never vfio-switched), or
// "mixed/transitional" when a vfio-switchable card is half-switched.
func gpuGroupModeLabel(g VFIOGpu) string {
	switch {
	case groupInMode(g, gpuModeVfio):
		return "vfio (all functions vfio-pci — passthrough-ready)"
	case groupInMode(g, gpuModeNvidia):
		return "nvidia (host/CDI — display on nvidia, audio on snd_hda_intel)"
	}
	// Not a vfio/nvidia state. If the display fn is on some OTHER host driver
	// (amdgpu, i915, nouveau, …) it is the host's own GPU, not vfio-switchable.
	if disp := gpuDisplayDriver(g.Addr); disp != "" && disp != hostDriverVfio && disp != hostDriverDisplay {
		return fmt.Sprintf("host driver %q — not a vfio-switchable card", disp)
	}
	return "mixed/transitional (group half-switched — run `charly vm gpu recover` or `charly vm gpu mode <vfio|nvidia>`)"
}

// poisonedTokensByVendor maps each normalized PCI vendor to whether ANY of its
// arbiter tokens is currently poisoned (boot-id keyed) — so status can flag the
// card whose claims the arbiter is refusing.
func poisonedTokensByVendor() map[string]bool {
	out := map[string]bool{}
	a := newResourceArbiter()
	for tok, rdef := range gatherResources() {
		if rdef != nil && rdef.Gpu != nil && a.resourcePoisoned(tok) {
			out[normalizePCIVendor(rdef.Gpu.Vendor)] = true
		}
	}
	return out
}

// VmGpuListCmd lists each GPU plus a ready-to-paste hostdevs YAML block.
type VmGpuListCmd struct{}

func (c *VmGpuListCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	rep := DetectVFIO()
	if len(rep.GPUs) == 0 {
		fmt.Println("No GPUs detected under /sys/bus/pci/devices.")
		return nil
	}
	if !rep.IOMMUEnabled {
		fmt.Fprintln(os.Stderr, "warning: IOMMU is not enabled — the hostdevs block below will not work until")
		fmt.Fprintln(os.Stderr, "         you enable IOMMU (intel_iommu=on / amd_iommu=on iommu=pt) and reboot.")
		fmt.Fprintln(os.Stderr)
	}

	for i, g := range rep.GPUs {
		fmt.Printf("# GPU %d: %s  %s:%s  %s\n", i, g.Addr, trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel)
		if g.IOMMUGroup >= 0 {
			fmt.Printf("#   IOMMU group %d — every function below must be passed through together:\n", g.IOMMUGroup)
		}
		for _, m := range g.GroupMembers {
			drv := m.Driver
			if drv == "" {
				drv = "unbound"
			}
			fmt.Printf("#     %s  %s  driver=%s\n", m.Addr, m.ClassLabel, drv)
		}
		fmt.Print(renderHostdevsBlock(g.GroupMembers))
		fmt.Println()
	}

	fmt.Println("# Paste the block under the kind:vm entity, e.g.:")
	fmt.Println("#   vm:")
	fmt.Println("#     my-gpu-vm:")
	fmt.Println("#       firmware: uefi-insecure   # passthrough wants UEFI")
	fmt.Println("#       libvirt:")
	fmt.Println("#         devices:")
	fmt.Println("#           <hostdevs block here>")
	return nil
}

// renderHostdevsBlock emits a `hostdevs:` YAML fragment (managed='yes') covering
// every device in the IOMMU group. managed='yes' lets libvirt auto-bind each
// function to vfio-pci on VM start and re-bind the host driver on stop. It is a
// text view over vfioGpuToHostdevs (gpu_allocate.go) — the SINGLE builder of
// which devices + PCI fields — so this `charly vm gpu list` output and the
// create-time auto-allocation can never diverge (R3).
func renderHostdevsBlock(members []VFIOPCIDevice) string {
	var b strings.Builder
	b.WriteString("hostdevs:\n")
	for _, h := range vfioGpuToHostdevs(members) {
		b.WriteString("  - type: pci\n")
		b.WriteString("    managed: \"yes\"\n")
		b.WriteString("    source:\n")
		fmt.Fprintf(&b, "      domain: %q\n", h.Source["domain"])
		fmt.Fprintf(&b, "      bus: %q\n", h.Source["bus"])
		fmt.Fprintf(&b, "      slot: %q\n", h.Source["slot"])
		fmt.Fprintf(&b, "      function: %q\n", h.Source["function"])
	}
	return b.String()
}

// parsePCIAddr splits "0000:01:00.0" into libvirt-form hex fields.
func parsePCIAddr(addr string) (domain, bus, slot, function string, ok bool) {
	// DDDD:BB:SS.F
	colon := strings.Split(addr, ":")
	if len(colon) != 3 {
		return "", "", "", "", false
	}
	dot := strings.SplitN(colon[2], ".", 2)
	if len(dot) != 2 {
		return "", "", "", "", false
	}
	return "0x" + colon[0], "0x" + colon[1], "0x" + dot[0], "0x" + dot[1], true
}

// trim0x drops a leading 0x for compact "vendor:device" display.
func trim0x(s string) string { return strings.TrimPrefix(s, "0x") }

// vfioPciAvailable reports whether the vfio-pci driver is present on the host.
func vfioPciAvailable() bool {
	for _, p := range []string{"/sys/bus/pci/drivers/vfio-pci", "/sys/module/vfio_pci"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
