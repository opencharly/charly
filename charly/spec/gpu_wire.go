package spec

// gpu_wire.go — the GPU/VFIO HOST-DETECTION wire types shared between charly's
// core (package main) and the compiled-in candy/plugin-gpu (cutover C11).
//
// These types live in package spec — the ONE importable home — because BOTH the
// host (the in-core Detect* / EnsureCDI / MemlockLimitBytes / VfioGroupAccessible
// shims, gpu_shim.go) AND the plugin (candy/plugin-gpu, via the replace → ../../charly
// module edge) construct and exchange them across the verb:gpu Invoke boundary. The
// host aliases the detection RESULT types (VFIOReport/VFIOGpu/VFIOPCIDevice/
// DetectedDevices) back into package main so the ~10 GPU consumers compile unchanged;
// the plugin owns the sysfs/exec detection LOGIC that produces them. There is NO
// duplicate type for any of these concepts (R3).
//
// The three embedded data tables (device_patterns / gpu_vendors / pci_class_labels)
// are NOT baked into the plugin: they live in charly's embedded charly.yml because a
// must-stay core consumer (`charly doctor`'s device report) reads device_patterns, so
// keeping ONE data source in core and threading it into the plugin via GpuProbeInput
// avoids an R3-duplicated copy.

// VFIOPCIDevice is a single PCI function discovered under sysfs.
type VFIOPCIDevice struct {
	Addr       string `json:"addr"`        // 0000:01:00.0 (sysfs device-directory name = libvirt domain:bus:slot.function)
	VendorID   string `json:"vendor_id"`   // 0x10de
	DeviceID   string `json:"device_id"`   // 0x2704
	Class      string `json:"class"`       // 0x0300 (high 16 bits of the PCI class code)
	ClassLabel string `json:"class_label"` // human label, e.g. "VGA controller"
	Driver     string `json:"driver"`      // nvidia | nouveau | vfio-pci | "" (unbound)
	IOMMUGroup int    `json:"iommu_group"` // -1 when the device has no iommu_group (IOMMU disabled)
}

// VFIOGpu is a display-class device plus every other function sharing its IOMMU
// group. Passthrough must move the whole group together, so the renderer emits one
// <hostdev> per GroupMember.
type VFIOGpu struct {
	VFIOPCIDevice
	GroupMembers []VFIOPCIDevice `json:"group_members"` // includes the GPU function itself; sorted by Addr
}

// VFIOReport summarizes host readiness for VFIO GPU passthrough.
type VFIOReport struct {
	IOMMUEnabled bool      `json:"iommu_enabled"` // /sys/kernel/iommu_groups is populated
	IOMMUKind    string    `json:"iommu_kind"`    // intel | amd | "" (from kernel cmdline)
	GPUs         []VFIOGpu `json:"gpus"`
}

// DetectedDevices holds the results of host device auto-detection.
type DetectedDevices struct {
	GPU           bool     `json:"gpu"`             // NVIDIA GPU detected AND CDI achievable (driver + (spec OR nvidia-ctk))
	AMDGPU        bool     `json:"amd_gpu"`         // AMD GPU detected (/dev/kfd + video/render groups)
	AMDGFXVersion string   `json:"amd_gfx_version"` // AMD GFX version for HSA_OVERRIDE_GFX_VERSION (e.g. "10.3.0")
	RenderNode    string   `json:"render_node"`     // First /dev/dri/renderD* path for DRINODE/DRI_NODE
	Devices       []string `json:"devices"`         // Other device paths to pass via --device
}

// GpuProbeInput is the action-multiplexed input the host ships to verb:gpu over
// OpRun. Action selects the host probe; the three data tables are threaded in from
// charly's embedded charly.yml (they stay in core for `charly doctor`, R3).
type GpuProbeInput struct {
	Action         string            `json:"action"`                     // detect-gpu | detect-amd-gpu | detect-vfio | detect-host-devices | ensure-cdi | memlock | vfio-group-accessible | amd-gfx-version
	Group          int               `json:"group,omitempty"`            // vfio-group-accessible
	DevicePatterns []string          `json:"device_patterns,omitempty"`  // detect-host-devices
	GpuVendors     map[string]string `json:"gpu_vendors,omitempty"`      // detect-host-devices (pickRenderNode)
	PCIClassLabels map[string]string `json:"pci_class_labels,omitempty"` // detect-vfio (readPCIDevice)
}

// GpuProbeReply is the action-multiplexed reply from verb:gpu. Each action populates
// only the field(s) it produces.
type GpuProbeReply struct {
	Bool        bool             `json:"bool,omitempty"`         // detect-gpu / detect-amd-gpu / vfio-group-accessible
	Str         string           `json:"str,omitempty"`          // amd-gfx-version
	Vfio        *VFIOReport      `json:"vfio,omitempty"`         // detect-vfio
	HostDevices *DetectedDevices `json:"host_devices,omitempty"` // detect-host-devices
	MemlockSoft uint64           `json:"memlock_soft,omitempty"` // memlock (RLIMIT_MEMLOCK soft)
	MemlockHard uint64           `json:"memlock_hard,omitempty"` // memlock (RLIMIT_MEMLOCK hard)
}
