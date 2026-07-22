// Package gpu is the GPU/VFIO HOST-DETECTION plugin (cutover C11): the sysfs/exec
// probing that charly core formerly held in charly/devices.go. It was carved out behind
// thin in-core resolve+Invoke shims (DetectGPU / DetectAMDGPU / DetectVFIO /
// DetectHostDevices / EnsureCDI / MemlockLimitBytes / VfioGroupAccessible +
// detectAMDGFXVersion, all in charly/gpu_shim.go). The DRIVER-SWITCH (vfio<->nvidia rebind)
// ALSO lives in this plugin now (cutover C9, switch.go — served over verb:gpu's OpRun
// DRIVER-SWITCH actions); auto-allocation (gpu_allocate.go) stays in core as a host-side
// VmSpec orchestrator consuming the DetectVFIO shim.
//
// Compiled-in (an in-proc inprocProvider): the deploy/config hot paths call the shims
// many times, and MemlockLimitBytes must read charly's OWN process RLIMIT_MEMLOCK — both
// require in-process placement.
//
// The three static data tables (device_patterns / gpu_vendors / pci_class_labels) are NOT
// baked here: they live in charly's embedded charly.yml (a must-stay core consumer,
// `charly doctor`, reads device_patterns) and are threaded in via spec.GpuProbeInput, so
// there is ONE data source (R3).
package gpu

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/spec"
	"golang.org/x/sys/unix"
)

// ---------------- NVIDIA / AMD GPU detection ----------------

// defaultDetectGPU checks whether an NVIDIA GPU is usable via CDI: the driver must be
// loaded AND a CDI spec must be reachable (existing at /etc/cdi/nvidia.yaml or
// /var/run/cdi/nvidia.yaml, OR nvidia-ctk on PATH so ensureCDI() can generate one).
// Driver-only is NOT enough — emitting AddDevice=nvidia.com/gpu=all in a quadlet whose
// host has no CDI spec causes podman setup to fail.
func defaultDetectGPU() bool {
	cmd := exec.Command("nvidia-smi")
	cmd.Stdout = nil
	cmd.Stderr = nil
	driverLoaded := cmd.Run() == nil
	return gpuUsableViaCDI(driverLoaded,
		func(p string) error { _, err := os.Stat(p); return err },
		func(name string) error { _, err := exec.LookPath(name); return err },
	)
}

// gpuUsableViaCDI is the pure decision helper: given whether the NVIDIA driver is loaded
// plus injected stat / look-path probes, return whether the GPU is usable via CDI. Tests
// inject closures so the real filesystem and PATH are not touched.
func gpuUsableViaCDI(driverLoaded bool, statFn func(string) error, lookPathFn func(string) error) bool {
	if !driverLoaded {
		return false
	}
	for _, p := range []string{"/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"} {
		if statFn(p) == nil {
			return true
		}
	}
	return lookPathFn("nvidia-ctk") == nil
}

// defaultDetectAMDGPU checks whether an AMD GPU is available by reading the DRM driver
// name from sysfs. Returns true if any DRM card uses the "amdgpu" driver.
func defaultDetectAMDGPU() bool {
	matches, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device/driver")
	for _, driverLink := range matches {
		target, err := os.Readlink(driverLink)
		if err == nil && filepath.Base(target) == "amdgpu" {
			return true
		}
	}
	return false
}

// detectAMDGFXVersion reads the AMD GPU architecture version from KFD topology.
// Returns a version string like "10.3.0" or "" if not available.
// Reads /sys/class/kfd/kfd/topology/nodes/*/properties for gfx_target_version.
func detectAMDGFXVersion() string {
	matches, _ := filepath.Glob("/sys/class/kfd/kfd/topology/nodes/*/properties")
	for _, path := range matches {
		ver := parseKFDGFXVersion(path)
		if ver != "" {
			return ver
		}
	}
	return ""
}

// parseKFDGFXVersion reads a KFD node properties file and extracts the GFX version.
// The gfx_target_version field encodes MAJOR*10000 + MINOR*100 + STEPPING.
// Returns "MAJOR.MINOR.0" (stepping dropped) or "" if not found/zero.
func parseKFDGFXVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "gfx_target_version "); ok {
			valStr := after
			val, err := strconv.Atoi(strings.TrimSpace(valStr))
			if err != nil || val == 0 {
				return "" // node 0 is CPU (version 0)
			}
			major := val / 10000
			minor := (val % 10000) / 100
			return fmt.Sprintf("%d.%d.0", major, minor)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: KFD properties scan error: %v\n", err)
	}
	return ""
}

// ---------------- Host device auto-detection ----------------

// defaultDetectHostDevices probes the host for available devices. The patterns +
// gpuVendors tables are threaded in from charly's embedded charly.yml (the shim reads
// them core-side and passes them in the probe input).
func defaultDetectHostDevices(patterns []string, gpuVendors map[string]string) spec.DetectedDevices {
	result := spec.DetectedDevices{
		GPU:    defaultDetectGPU(),
		AMDGPU: defaultDetectAMDGPU(),
	}
	if result.AMDGPU {
		result.AMDGFXVersion = detectAMDGFXVersion()
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		result.Devices = append(result.Devices, matches...)
	}
	// Pick the render node for DRINODE/DRI_NODE auto-injection, preferring a real GPU
	// over the paravirtual virtio-gpu (see pickRenderNode).
	result.RenderNode = pickRenderNode(result.Devices, gpuVendors)
	return result
}

// renderNodeVendor reads the PCI vendor id of a /dev/dri/renderD* node from sysfs
// (e.g. "0x10de" NVIDIA, "0x1002" AMD, "0x8086" Intel, "0x1af4" virtio). Package var so
// tests can supply a fake without a real /sys.
var renderNodeVendor = func(node string) string {
	b, err := os.ReadFile("/sys/class/drm/" + filepath.Base(node) + "/device/vendor")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pickRenderNode chooses the DRINODE/DRI_NODE render node from the detected device list.
// It PREFERS a real GPU (whose vendor is in gpuVendors: NVIDIA/AMD/Intel) over the
// paravirtual virtio-gpu (0x1af4): on a GPU-passthrough VM the seat's virtio head is
// renderD128 and the passed-through card is renderD129, so the old first-wins default
// pointed VAAPI/encoder probing at the encode-incapable virtio node. Falls back to the
// first renderD* when no vendor distinguishes them, so single-GPU hosts are unchanged.
func pickRenderNode(devices []string, gpuVendors map[string]string) string {
	var first string
	for _, d := range devices {
		if !strings.HasPrefix(filepath.Base(d), "renderD") {
			continue
		}
		if first == "" {
			first = d
		}
		if _, ok := gpuVendors[renderNodeVendor(d)]; ok {
			return d // a real, encode-capable GPU (NVIDIA/AMD/Intel) over virtio-gpu
		}
	}
	return first
}

// ensureCDI checks if NVIDIA CDI specs exist for podman. If not, attempts to generate
// them via nvidia-ctk (user-scope, best-effort). This enables GPU access in nested
// containers where CDI specs from the host are not inherited.
func ensureCDI() {
	cdiPaths := []string{"/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"}
	for _, p := range cdiPaths {
		if _, err := os.Stat(p); err == nil {
			return // CDI spec exists
		}
	}
	ctk, err := exec.LookPath("nvidia-ctk")
	if err != nil {
		return // nvidia-ctk not installed, can't generate
	}
	_ = os.MkdirAll("/etc/cdi", 0755) // best-effort — nvidia-ctk surfaces a clear error otherwise
	cmd := exec.Command(ctk, "cdi", "generate", "--output=/etc/cdi/nvidia.yaml")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Run() // Best effort — if it fails, podman gives a clear CDI error
}

// ---------------- VFIO / GPU passthrough host detection ----------------

// defaultDetectVFIO probes the host for IOMMU readiness and passthrough-capable GPUs.
// The pci_class_labels table is threaded in from charly's embedded charly.yml.
func defaultDetectVFIO(classLabels map[string]string) spec.VFIOReport {
	return scanVFIO("/sys", "/proc/cmdline", classLabels)
}

// scanVFIO is the pure detector: it reads only from sysRoot + cmdlinePath, so tests
// inject a synthetic sysfs tree and a fake /proc/cmdline.
func scanVFIO(sysRoot, cmdlinePath string, classLabels map[string]string) spec.VFIOReport {
	rep := spec.VFIOReport{IOMMUKind: iommuKindFromCmdline(cmdlinePath)}

	groupsDir := filepath.Join(sysRoot, "kernel", "iommu_groups")
	if entries, err := os.ReadDir(groupsDir); err == nil && len(entries) > 0 {
		rep.IOMMUEnabled = true
	}

	// Enumerate every PCI function once so group members resolve via map lookup.
	devDirs, _ := filepath.Glob(filepath.Join(sysRoot, "bus", "pci", "devices", "*"))
	all := make(map[string]spec.VFIOPCIDevice, len(devDirs))
	for _, dir := range devDirs {
		d := readPCIDevice(sysRoot, filepath.Base(dir), classLabels)
		all[d.Addr] = d
	}

	for _, d := range all {
		if !isDisplayClass(d.Class) {
			continue
		}
		// VFIOGpu is flattened (SDD conversion) — spread via the shared
		// spec.NewVFIOGpu constructor instead of the former embedded-field
		// literal.
		gpu := spec.NewVFIOGpu(d)
		for _, m := range iommuGroupMembers(sysRoot, d.IOMMUGroup) {
			if md, ok := all[m]; ok {
				gpu.GroupMembers = append(gpu.GroupMembers, md)
			}
		}
		if len(gpu.GroupMembers) == 0 {
			gpu.GroupMembers = []spec.VFIOPCIDevice{d} // IOMMU off: the GPU alone
		}
		sort.Slice(gpu.GroupMembers, func(i, j int) bool {
			return gpu.GroupMembers[i].Addr < gpu.GroupMembers[j].Addr
		})
		rep.GPUs = append(rep.GPUs, gpu)
	}
	sort.Slice(rep.GPUs, func(i, j int) bool { return rep.GPUs[i].Addr < rep.GPUs[j].Addr })
	return rep
}

// iommuKindFromCmdline returns "intel" / "amd" / "" from the kernel cmdline.
func iommuKindFromCmdline(cmdlinePath string) string {
	b, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}
	c := string(b)
	if strings.Contains(c, "intel_iommu=on") {
		return "intel"
	}
	if strings.Contains(c, "amd_iommu=on") {
		return "amd"
	}
	return ""
}

func readPCIDevice(sysRoot, addr string, classLabels map[string]string) spec.VFIOPCIDevice {
	base := filepath.Join(sysRoot, "bus", "pci", "devices", addr)
	d := spec.VFIOPCIDevice{
		Addr:       addr,
		VendorID:   readSysTrim(filepath.Join(base, "vendor")),
		DeviceID:   readSysTrim(filepath.Join(base, "device")),
		IOMMUGroup: -1,
	}
	// class file is e.g. "0x030000"; keep the high 16 bits (0x0300).
	if raw := readSysTrim(filepath.Join(base, "class")); len(raw) >= 6 {
		d.Class = raw[:6]
		d.ClassLabel = pciClassLabel(d.Class, classLabels)
	}
	if target, err := os.Readlink(filepath.Join(base, "driver")); err == nil {
		d.Driver = filepath.Base(target)
	}
	if target, err := os.Readlink(filepath.Join(base, "iommu_group")); err == nil {
		if n, err := strconv.Atoi(filepath.Base(target)); err == nil {
			d.IOMMUGroup = n
		}
	}
	return d
}

// iommuGroupMembers lists the PCI addresses in an IOMMU group.
func iommuGroupMembers(sysRoot string, group int) []string {
	if group < 0 {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(sysRoot, "kernel", "iommu_groups", strconv.Itoa(group), "devices", "*"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, filepath.Base(m))
	}
	return out
}

// memlockLimitBytes returns the current process's RLIMIT_MEMLOCK (soft, hard). VFIO
// passthrough pins all guest RAM, so QEMU needs a memlock limit ≥ guest RAM. Under
// rootless qemu:///session the limit is inherited from the login session (commonly
// 8 MiB), so this is a passthrough-readiness signal. Compiled-in, so it reads charly's
// own process limit.
func memlockLimitBytes() (soft, hard uint64) {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &rl); err != nil {
		return 0, 0
	}
	return rl.Cur, rl.Max
}

// vfioGroupAccessible reports whether the current user can open the VFIO group device
// node (/dev/vfio/<group>). Default perms are root-only, so rootless passthrough needs a
// udev rule / ownership grant. group < 0 → no IOMMU group.
func vfioGroupAccessible(group int) bool {
	if group < 0 {
		return false
	}
	return unix.Access(fmt.Sprintf("/dev/vfio/%d", group), unix.R_OK|unix.W_OK) == nil
}

func readSysTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// isDisplayClass reports whether a PCI class (0x03xx) is a display controller.
func isDisplayClass(class string) bool {
	return strings.HasPrefix(class, "0x03")
}

// pciClassLabel maps a PCI class code (high 16 bits) to a human label, falling back to
// the raw class string for an unknown class.
func pciClassLabel(class string, labels map[string]string) string {
	if label, ok := labels[class]; ok {
		return label
	}
	return class
}
