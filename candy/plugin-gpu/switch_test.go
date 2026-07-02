package gpu

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// switch_test.go — the GPU driver-switch unit suite, relocated from charly core
// (charly/gpu_driver_switch_test.go) with the driver-switch (cutover C9). Adapted: the mode +
// host-driver consts are spec.Gpu*/spec.HostDriver*, VFIOGpu is spec.VFIOGpu, and
// switchGPUDriverMode returns (wedged, err).

// twoFnGPU is the canonical multifunction passthrough GPU: a VGA display function + its sibling
// HDMI-audio function in one IOMMU group.
func twoFnGPU() spec.VFIOGpu {
	return spec.VFIOGpu{
		VFIOPCIDevice: spec.VFIOPCIDevice{Addr: "0000:01:00.0", VendorID: "0x10de", DeviceID: "0x2702", Class: "0x0300", Driver: "vfio-pci", IOMMUGroup: 13},
		GroupMembers: []spec.VFIOPCIDevice{
			{Addr: "0000:01:00.0", Class: "0x0300", ClassLabel: "VGA controller", Driver: "vfio-pci"},
			{Addr: "0000:01:00.1", Class: "0x0403", ClassLabel: "Audio device", Driver: "vfio-pci"},
		},
	}
}

func TestGpuModeFromDriver(t *testing.T) {
	cases := map[string]string{
		"nvidia":   spec.GpuModeNvidia,
		"vfio-pci": spec.GpuModeVfio,
		"":         spec.GpuModeVfio,
		"nouveau":  spec.GpuModeVfio,
	}
	for driver, want := range cases {
		if got := gpuModeFromDriver(driver); got != want {
			t.Errorf("gpuModeFromDriver(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestCurrentGPUMode(t *testing.T) {
	orig := gpuDisplayDriver
	defer func() { gpuDisplayDriver = orig }()
	gpuDisplayDriver = func(addr string) string { return "nvidia" }
	gpu := spec.VFIOGpu{VFIOPCIDevice: spec.VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if got := currentGPUMode(gpu); got != spec.GpuModeNvidia {
		t.Errorf("currentGPUMode = %q, want nvidia", got)
	}
}

func TestHostDriverForFunction(t *testing.T) {
	cases := []struct{ class, mode, want string }{
		{"0x0300", spec.GpuModeNvidia, spec.HostDriverDisplay},
		{"0x0302", spec.GpuModeNvidia, spec.HostDriverDisplay},
		{"0x0403", spec.GpuModeNvidia, spec.HostDriverAudio},
		{"0x0c03", spec.GpuModeNvidia, spec.HostDriverVfio},
		{"0x0300", spec.GpuModeVfio, spec.HostDriverVfio},
		{"0x0403", spec.GpuModeVfio, spec.HostDriverVfio},
	}
	for _, c := range cases {
		if got := hostDriverForFunction(c.class, c.mode); got != c.want {
			t.Errorf("hostDriverForFunction(%q,%q)=%q want %q", c.class, c.mode, got, c.want)
		}
	}
}

func TestSwitchScripts_GroupAware(t *testing.T) {
	gpu := twoFnGPU()

	nv := switchScriptToNvidia(gpu)
	for _, addr := range []string{"0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(nv, addr) {
			t.Errorf("nvidia switch script must cover group member %s", addr)
		}
	}
	if !strings.Contains(nv, "nvidia") || !strings.Contains(nv, "snd_hda_intel") {
		t.Error("nvidia switch must bind display->nvidia AND audio->snd_hda_intel")
	}
	if !strings.Contains(nv, "nvidia-modprobe") {
		t.Error("nvidia switch must create /dev/nvidia* nodes")
	}

	vf := switchScriptToVfio(gpu)
	for _, addr := range []string{"0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(vf, addr) {
			t.Errorf("vfio switch script must cover group member %s", addr)
		}
	}
	if !strings.Contains(vf, "modprobe -r nvidia") {
		t.Error("vfio switch MUST detach nvidia via `modprobe -r` (the safe EBUSY gate)")
	}
	if strings.Contains(vf, "drivers/nvidia/unbind") {
		t.Error("vfio switch MUST NEVER sysfs-unbind nvidia — that is the reboot-only device_lock wedge")
	}
	if !strings.Contains(vf, "exit 3") {
		t.Error("vfio switch must refuse (exit 3) when nvidia is still in use rather than force-unbind")
	}
	if !strings.Contains(vf, "nv_pci_remove") {
		t.Error("vfio switch must self-detect a wedge (nv_pci_remove D-state) and exit 4")
	}
}

func TestGroupInMode_GroupAware(t *testing.T) {
	gpu := twoFnGPU()
	orig := gpuDisplayDriver
	defer func() { gpuDisplayDriver = orig }()

	gpuDisplayDriver = func(addr string) string {
		if addr == "0000:01:00.0" {
			return "nvidia"
		}
		return "vfio-pci"
	}
	if groupInMode(gpu, spec.GpuModeNvidia) {
		t.Error("half-switched group (audio still vfio) must NOT read as in nvidia mode")
	}
	gpuDisplayDriver = func(addr string) string {
		if addr == "0000:01:00.0" {
			return "nvidia"
		}
		return "snd_hda_intel"
	}
	if !groupInMode(gpu, spec.GpuModeNvidia) {
		t.Error("display=nvidia + audio=snd_hda_intel must read as in nvidia mode")
	}
	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	if !groupInMode(gpu, spec.GpuModeVfio) {
		t.Error("both functions vfio-pci must read as in vfio mode")
	}
}

func TestSwitchGPUDriverMode_IdempotentNoOp(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	called := false
	runGPUSwitchScript = func(string) ([]byte, error) { called = true; return nil, nil }

	if _, err := switchGPUDriverMode(twoFnGPU(), spec.GpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if called {
		t.Fatal("idempotent no-op must NOT invoke the sudo sysfs script when already in mode")
	}
}

func TestSwitchGPUDriverMode_ToNvidiaScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	if _, err := switchGPUDriverMode(twoFnGPU(), spec.GpuModeNvidia); err != nil {
		t.Fatalf("switch: %v", err)
	}
	for _, want := range []string{"driver_override", "nvidia", "snd_hda_intel", "nvidia-modprobe", "0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-nvidia script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_ToVfioScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "nvidia" }
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	if _, err := switchGPUDriverMode(twoFnGPU(), spec.GpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	for _, want := range []string{"modprobe -r nvidia", "modprobe vfio-pci", "driver_override", "drivers_probe"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-vfio script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_WedgeDetection(t *testing.T) {
	gpu := twoFnGPU()
	origD, origR := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origD, origR }()
	gpuDisplayDriver = func(string) string { return "nvidia" }

	// (a) deadline timeout (runGPUSwitchScript maps it to spec.ErrGPUSwitchWedged) → wedged=true.
	// The plugin carries the wedge as the BOOL over the wire; the CORE gpu shim (switchReplyErr)
	// re-wraps spec.ErrGPUSwitchWedged from that bool, so errors.Is is the CORE-side contract.
	runGPUSwitchScript = func(string) ([]byte, error) { return []byte("timed out"), spec.ErrGPUSwitchWedged }
	if wedged, err := switchGPUDriverMode(gpu, spec.GpuModeVfio); !wedged || err == nil {
		t.Errorf("deadline wedge must surface wedged=true + an error, got wedged=%v err=%v", wedged, err)
	}
	// (b) script self-detected wedge (WEDGED in output, generic exit error).
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio WEDGED: 0000:01:00.0 ..."), errors.New("exit status 4")
	}
	if wedged, err := switchGPUDriverMode(gpu, spec.GpuModeVfio); !wedged || err == nil {
		t.Errorf("self-detected WEDGED output must surface wedged, got wedged=%v err=%v", wedged, err)
	}
	// (c) an ordinary failure is NOT a wedge.
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio FAILED: 0000:01:00.0 driver=unbound"), errors.New("exit status 1")
	}
	if wedged, err := switchGPUDriverMode(gpu, spec.GpuModeVfio); err == nil || wedged {
		t.Errorf("ordinary failure must error but NOT be a wedge, got wedged=%v err=%v", wedged, err)
	}
}

func TestSwitchGPUDriverMode_UnknownMode(t *testing.T) {
	origDrv := gpuDisplayDriver
	defer func() { gpuDisplayDriver = origDrv }()
	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	if _, err := switchGPUDriverMode(twoFnGPU(), "bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestGpuSwitchModeTolerant_AbsentCardIsNoError(t *testing.T) {
	origDetect, origRun := vfioDetect, runGPUSwitchScript
	defer func() { vfioDetect, runGPUSwitchScript = origDetect, origRun }()

	vfioDetect = func(map[string]string) spec.VFIOReport { return spec.VFIOReport{} } // no GPUs
	called := false
	runGPUSwitchScript = func(string) ([]byte, error) { called = true; return nil, nil }

	if _, err := gpuSwitchModeTolerant("0x10de", spec.GpuModeVfio); err != nil {
		t.Fatalf("absent card must be tolerated, got %v", err)
	}
	if called {
		t.Fatal("must not run the switch script when no matching card exists")
	}
}

func TestFormatNvidiaHolders(t *testing.T) {
	if got := formatNvidiaHolders(nil); !strings.Contains(got, "could not be identified") || strings.Contains(got, "pid") {
		t.Fatalf("empty holders must render the generic clause, got %q", got)
	}
	one := formatNvidiaHolders([]NvidiaHolder{{PID: 237390, Comm: "btop"}})
	if one != "external process(es): btop (pid 237390)" {
		t.Fatalf("single holder format wrong, got %q", one)
	}
	many := formatNvidiaHolders([]NvidiaHolder{{PID: 237390, Comm: "btop"}, {PID: 41, Comm: "nvidia-smi"}})
	if many != "external process(es): btop (pid 237390), nvidia-smi (pid 41)" {
		t.Fatalf("multi holder format wrong, got %q", many)
	}
}

func TestNvidiaInUseRefusal_Message(t *testing.T) {
	err := nvidiaInUseRefusal([]NvidiaHolder{{PID: 237390, Comm: "btop"}})
	msg := err.Error()
	for _, want := range []string{"REFUSED", "btop (pid 237390)", "auto-preempts", "close these external GPU clients", "refusing to force-unbind"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q:\n%s", want, msg)
		}
	}
	if g := nvidiaInUseRefusal(nil).Error(); !strings.Contains(g, "could not be identified") || !strings.Contains(g, "force-unbind") {
		t.Errorf("empty-holder refusal must stay actionable, got %q", g)
	}
}

func TestSwitchGPUDriverMode_VfioRefusalNamesHolders(t *testing.T) {
	origDrv, origRun, origDisc := gpuDisplayDriver, runGPUSwitchScript, discoverNvidiaHolders
	defer func() { gpuDisplayDriver, runGPUSwitchScript, discoverNvidiaHolders = origDrv, origRun, origDisc }()

	gpuDisplayDriver = func(string) string { return "nvidia" }
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio REFUSED: " + nvidiaInUseMarker + " (a GPU client holds the card) — refusing to force-unbind (would wedge the device_lock)"), errors.New("exit status 3")
	}
	discoverNvidiaHolders = func() []NvidiaHolder { return []NvidiaHolder{{PID: 237390, Comm: "btop"}} }

	wedged, err := switchGPUDriverMode(twoFnGPU(), spec.GpuModeVfio)
	if err == nil {
		t.Fatal("an EBUSY refusal must surface an error")
	}
	if wedged {
		t.Fatalf("an external-holder refusal is NOT a wedge, got wedged=%v", wedged)
	}
	if !strings.Contains(err.Error(), "btop (pid 237390)") {
		t.Fatalf("refusal must name the external holder, got %q", err.Error())
	}
}

// TestSwitchPlan_DryRun proves the C9 cred/hardware-free DRY-RUN dispatch proof: switchPlan
// returns the exact rebind commands WITHOUT touching sysfs (no runGPUSwitchScript), for the
// synthetic example GPU on a GPU-less host.
func TestSwitchPlan_DryRun(t *testing.T) {
	origRun := runGPUSwitchScript
	defer func() { runGPUSwitchScript = origRun }()
	called := false
	runGPUSwitchScript = func(string) ([]byte, error) { called = true; return nil, nil }

	vfioPlan, err := switchPlan(nil, spec.GpuModeVfio)
	if err != nil {
		t.Fatalf("switchPlan vfio: %v", err)
	}
	if called {
		t.Fatal("switchPlan must NOT execute the sudo script (dry-run, hardware-free)")
	}
	joined := strings.Join(vfioPlan, "\n")
	for _, want := range []string{"vfio-pci", "driver_override", "modprobe -r nvidia"} {
		if !strings.Contains(joined, want) {
			t.Errorf("vfio dry-run plan missing %q:\n%s", want, joined)
		}
	}
	nvPlan, err := switchPlan(nil, spec.GpuModeNvidia)
	if err != nil {
		t.Fatalf("switchPlan nvidia: %v", err)
	}
	if !strings.Contains(strings.Join(nvPlan, "\n"), "snd_hda_intel") {
		t.Errorf("nvidia dry-run plan must bind audio->snd_hda_intel:\n%s", strings.Join(nvPlan, "\n"))
	}
}
