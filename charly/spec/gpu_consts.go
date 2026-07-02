package spec

// gpu_consts.go — the GPU driver-MODE + host-driver constants shared between
// charly's core and candy/plugin-gpu (cutover C9). The DRIVER-SWITCH logic moved
// into candy/plugin-gpu (the vfio<->nvidia rebind), but a handful of core sites
// (the arbiter mode-math seams, vm_gpu_cmd shims) still name these values, so they
// live in package spec — the ONE importable home — and core aliases them
// (in gpu_shim.go) so any residual core reference stays clean (R3).

const (
	// GpuModeVfio — EVERY function of the GPU's IOMMU group bound to vfio-pci
	// (free for VM passthrough; the boot default on a passthrough host).
	GpuModeVfio = "vfio"
	// GpuModeNvidia — each function on its correct host driver (display -> nvidia
	// for CDI-shared pods, HDMI-audio -> snd_hda_intel).
	GpuModeNvidia = "nvidia"

	// NvidiaVendorID is the normalized PCI vendor of NVIDIA cards (the device_lock
	// wedge is an nvidia-driver concept).
	NvidiaVendorID = "0x10de"

	// HostDriverDisplay / HostDriverAudio / HostDriverVfio are the host drivers a
	// group function binds to: the display (VGA/3D, class 0x03xx) takes nvidia in
	// host mode; the HDMI-audio (class 0x0403) takes snd_hda_intel; everything takes
	// vfio-pci for passthrough.
	HostDriverDisplay = "nvidia"
	HostDriverAudio   = "snd_hda_intel"
	HostDriverVfio    = "vfio-pci"
)

// GpuSwitchInput is the action-multiplexed input the core driver-switch shims ship
// to verb:gpu over OpRun for the DRIVER-SWITCH actions (cutover C9). It rides the
// SAME verb:gpu provider as the C11 detection actions (GpuProbeInput); the action
// vocabularies are disjoint, so the plugin's Invoke dispatches by which envelope
// decodes — detection actions decode GpuProbeInput, switch actions decode this.
type GpuSwitchInput struct {
	Action string   `json:"action"`           // switch-mode | ensure-cdi | wedge-detected | group-in-mode | current-mode | display-driver | switch-plan
	Gpu    *VFIOGpu `json:"gpu,omitempty"`    // switch-mode / group-in-mode / current-mode / switch-plan
	Mode   string   `json:"mode,omitempty"`   // switch-mode / group-in-mode / switch-plan
	Addr   string   `json:"addr,omitempty"`   // display-driver (a single PCI function)
	Vendor string   `json:"vendor,omitempty"` // switch-mode (tolerant vendor-select) / ensure-cdi-N/A
}

// GpuSwitchReply is the action-multiplexed reply for the DRIVER-SWITCH actions.
type GpuSwitchReply struct {
	Bool   bool     `json:"bool,omitempty"`   // wedge-detected / group-in-mode
	Str    string   `json:"str,omitempty"`    // current-mode / display-driver
	Plan   []string `json:"plan,omitempty"`   // switch-plan: the exact rebind commands (DRY-RUN, no sysfs touched)
	Wedged bool     `json:"wedged,omitempty"` // switch-mode: the switch wedged the device_lock (errGPUSwitchWedged)
	Error  string   `json:"error,omitempty"`  // switch-mode / ensure-cdi op failure
}

// GPU driver-switch actions served by verb:gpu (cutover C9).
const (
	GpuSwitchActionMode          = "switch-mode"     // GpuSwitchInput{Gpu?,Vendor?,Mode} -> flip the group's driver mode (Gpu set = exact card; Vendor set = tolerant vendor-select)
	GpuSwitchActionEnsureCDI     = "ensure-cdi-root" // regenerate /etc/cdi/nvidia.yaml as ROOT (distinct from the C11 user-scope "ensure-cdi" detection action)
	GpuSwitchActionWedgeDetected = "wedge-detected"  // read-only D-state probe -> Bool
	GpuSwitchActionGroupInMode   = "group-in-mode"   // GpuSwitchInput{Gpu,Mode} -> Bool
	GpuSwitchActionCurrentMode   = "current-mode"    // GpuSwitchInput{Gpu} -> Str (vfio|nvidia)
	GpuSwitchActionDisplayDriver = "display-driver"  // GpuSwitchInput{Addr} -> Str (live driver of a PCI function)
	GpuSwitchActionPlan          = "switch-plan"     // GpuSwitchInput{Gpu,Mode} -> Plan (DRY-RUN commands; NO sysfs write) — the C9 cred/hw-free dispatch proof
)
