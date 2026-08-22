// gpu.cue — the GPU/VFIO HOST-DETECTION wire types shared between charly's core
// and the compiled-in candy/plugin-gpu (cutover C11; SDD conversion, per the
// standing operator directive: a hand-written wire struct not yet CUE-sourced is
// conversion-in-progress, never a sanctioned exception). Plain structs —
// gengotypes generates them faithfully, no disjunction/inexpressibility escape
// needed. The DRIVER-SWITCH wire types (GpuSwitchInput/GpuSwitchReply,
// spec/gpu_consts.go) are a SEPARATE, PRE-EXISTING hand-written surface — out of
// scope for THIS conversion (untouched files stay tracked debt per the standing
// "modified code = full guideline compliance" directive; they are not modified
// here).

// #VFIOPCIDevice is a single PCI function discovered under sysfs.
#VFIOPCIDevice: {
	addr!:        string @go(Addr) // 0000:01:00.0 (sysfs device-directory name = libvirt domain:bus:slot.function)
	vendor_id!:   string @go(VendorID)   // 0x10de
	device_id!:   string @go(DeviceID)   // 0x2704
	class!:       string @go(Class)      // 0x0300 (high 16 bits of the PCI class code)
	class_label!: string @go(ClassLabel) // human label, e.g. "VGA controller"
	driver!:      string @go(Driver)     // nvidia | nouveau | vfio-pci | "" (unbound)
	iommu_group!: int    @go(IOMMUGroup,type=int) // -1 when the device has no iommu_group (IOMMU disabled)
}

// #VFIOGpu is a display-class device plus every other function sharing its
// IOMMU group. Passthrough must move the whole group together, so the
// renderer emits one <hostdev> per GroupMember.
//
// FLATTENED (never a Go anonymous-embedded field — CUE unification has no
// concept of Go embedding; it always structurally merges fields into one flat
// struct). The former hand type embedded VFIOPCIDevice anonymously; Go's
// encoding/json ALREADY promotes an anonymous embedded struct's fields to the
// parent JSON object on the wire (Go's own embedding-promotion rule), so this
// flattened shape is BYTE-IDENTICAL JSON to the former embedded type — the
// only consumer-visible change is Go SOURCE: a composite literal naming the
// embedded field (`VFIOGpu{VFIOPCIDevice: d}`) must spread d's fields instead
// (`VFIOGpu{Addr: d.Addr, ...}`). Fixed at every call site in the same
// cutover (candy/plugin-gpu, charly test fixtures).
#VFIOGpu: {
	#VFIOPCIDevice
	group_members!: [...#VFIOPCIDevice] @go(GroupMembers)
}

// #VFIOReport summarizes host readiness for VFIO GPU passthrough.
#VFIOReport: {
	iommu_enabled!: bool   @go(IOMMUEnabled) // /sys/kernel/iommu_groups is populated
	iommu_kind!:    string @go(IOMMUKind)    // intel | amd | "" (from kernel cmdline)
	gpus!: [...#VFIOGpu] @go(GPUs)
}

// #DetectedDevices holds the results of host device auto-detection.
#DetectedDevices: {
	gpu!:             bool   @go(GPU)             // NVIDIA GPU detected AND CDI achievable (driver + (spec OR nvidia-ctk))
	amd_gpu!:         bool   @go(AMDGPU)           // AMD GPU detected (/dev/kfd + video/render groups)
	amd_gfx_version!: string @go(AMDGFXVersion)    // AMD GFX version for HSA_OVERRIDE_GFX_VERSION (e.g. "10.3.0")
	render_node!:     string @go(RenderNode)       // First /dev/dri/renderD* path for DRINODE/DRI_NODE
	devices!: [...string] @go(Devices) // Other device paths to pass via --device
}

// #GpuProbeInput is the action-multiplexed input to verb:gpu over OpRun. Action selects the
// host probe. The three data tables (device_patterns/gpu_vendors/pci_class_labels) are now
// candy/plugin-gpu's OWN embedded data (K5 seam-death — they moved out of charly-core's
// embedded charly.yml alongside the "hostprobe" HostBuild kind's dissolution; plugin-gpu is
// the actual detection consumer, so it is the one data source, R3), so this input no longer
// threads them.
#GpuProbeInput: {
	action!: string @go(Action) // detect-gpu | detect-amd-gpu | detect-vfio | detect-host-devices | ensure-cdi | memlock | vfio-group-accessible | amd-gfx-version
	group?:  int    @go(Group,type=int) // vfio-group-accessible
}

// #GpuProbeReply is the action-multiplexed reply from verb:gpu. Each action
// populates only the field(s) it produces.
#GpuProbeReply: {
	// "bool" is quoted (a bare `bool` field name collides with the CUE builtin
	// type keyword — the arbiter.cue #ArbiterInvokeReply precedent).
	"bool"?:       bool             @go(Bool) // detect-gpu / detect-amd-gpu / vfio-group-accessible
	str?:          string           @go(Str)  // amd-gfx-version
	vfio?:         #VFIOReport      @go(Vfio,optional=nillable) // detect-vfio
	host_devices?: #DetectedDevices @go(HostDevices,optional=nillable) // detect-host-devices
	memlock_soft?: int @go(MemlockSoft,type=uint64) // memlock (RLIMIT_MEMLOCK soft)
	memlock_hard?: int @go(MemlockHard,type=uint64) // memlock (RLIMIT_MEMLOCK hard)
}
