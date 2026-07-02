package gpu

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests were carved out of charly/vfio_test.go + charly/devices_test.go +
// charly/devices_render_node_test.go alongside the detection primitives they exercise
// (cutover C11). They test the pure, host-independent leaves — scanVFIO against a
// synthetic sysfs tree, gpuUsableViaCDI with injected probes, parseKFDGFXVersion on a
// temp file, and pickRenderNode with a faked renderNodeVendor.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestScanVFIO(t *testing.T) {
	sys := t.TempDir()
	cmdline := filepath.Join(sys, "cmdline")
	writeFile(t, cmdline, "BOOT_IMAGE=/vmlinuz root=UUID=x amd_iommu=on iommu=pt rw\n")

	// IOMMU group 13: a GPU (0300) + its audio function (0403).
	gpu := filepath.Join(sys, "bus", "pci", "devices", "0000:01:00.0")
	aud := filepath.Join(sys, "bus", "pci", "devices", "0000:01:00.1")
	writeFile(t, filepath.Join(gpu, "class"), "0x030000\n")
	writeFile(t, filepath.Join(gpu, "vendor"), "0x10de\n")
	writeFile(t, filepath.Join(gpu, "device"), "0x2704\n")
	writeFile(t, filepath.Join(aud, "class"), "0x040300\n")
	writeFile(t, filepath.Join(aud, "vendor"), "0x10de\n")
	writeFile(t, filepath.Join(aud, "device"), "0x22bb\n")

	// driver + iommu_group symlinks (scanVFIO reads basename only).
	symlink(t, "../../../bus/pci/drivers/vfio-pci", filepath.Join(gpu, "driver"))
	symlink(t, "../../../bus/pci/drivers/snd_hda_intel", filepath.Join(aud, "driver"))
	symlink(t, "../../../kernel/iommu_groups/13", filepath.Join(gpu, "iommu_group"))
	symlink(t, "../../../kernel/iommu_groups/13", filepath.Join(aud, "iommu_group"))

	// iommu group membership listing.
	grpDev := filepath.Join(sys, "kernel", "iommu_groups", "13", "devices")
	writeFile(t, filepath.Join(grpDev, "0000:01:00.0"), "")
	writeFile(t, filepath.Join(grpDev, "0000:01:00.1"), "")

	rep := scanVFIO(sys, cmdline, nil)

	if !rep.IOMMUEnabled {
		t.Error("expected IOMMUEnabled=true (iommu_groups populated)")
	}
	if rep.IOMMUKind != "amd" {
		t.Errorf("IOMMUKind = %q, want amd", rep.IOMMUKind)
	}
	if len(rep.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1 (only the 0x0300 device)", len(rep.GPUs))
	}
	g := rep.GPUs[0]
	if g.Addr != "0000:01:00.0" || g.VendorID != "0x10de" || g.DeviceID != "0x2704" {
		t.Errorf("GPU id mismatch: %+v", g)
	}
	if g.Driver != "vfio-pci" {
		t.Errorf("GPU driver = %q, want vfio-pci", g.Driver)
	}
	if g.IOMMUGroup != 13 {
		t.Errorf("GPU IOMMUGroup = %d, want 13", g.IOMMUGroup)
	}
	if len(g.GroupMembers) != 2 {
		t.Fatalf("GroupMembers = %d, want 2 (GPU + audio)", len(g.GroupMembers))
	}
	// Members sorted by Addr → GPU first, audio second.
	if g.GroupMembers[0].Addr != "0000:01:00.0" || g.GroupMembers[1].Addr != "0000:01:00.1" {
		t.Errorf("group members not sorted/expected: %+v", g.GroupMembers)
	}
}

func TestScanVFIO_NoIOMMU(t *testing.T) {
	sys := t.TempDir()
	cmdline := filepath.Join(sys, "cmdline")
	writeFile(t, cmdline, "BOOT_IMAGE=/vmlinuz root=UUID=x rw\n") // no iommu flag
	rep := scanVFIO(sys, cmdline, nil)
	if rep.IOMMUEnabled {
		t.Error("expected IOMMUEnabled=false with empty iommu_groups")
	}
	if rep.IOMMUKind != "" {
		t.Errorf("IOMMUKind = %q, want empty", rep.IOMMUKind)
	}
}

func TestAMDGFXVersionParsing(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"RDNA2", "gfx_target_version 100306\n", "10.3.0"},
		{"RDNA3", "gfx_target_version 110000\n", "11.0.0"},
		{"CPU node", "gfx_target_version 0\n", ""},
		{"missing", "some_other_field 123\n", ""},
		{"RDNA1", "gfx_target_version 90012\n", "9.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "kfd-props-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name()) //nolint:errcheck
			if _, err := f.WriteString(tt.content); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()

			got := parseKFDGFXVersion(f.Name())
			if got != tt.expected {
				t.Errorf("parseKFDGFXVersion(%q) = %q, want %q", tt.content, got, tt.expected)
			}
		})
	}
}

// TestPickRenderNode_PrefersRealGPUOverVirtio guards the DRINODE/DRI_NODE auto-detect:
// on a GPU-passthrough VM the seat's virtio-gpu is renderD128 and the passed-through card
// is renderD129, so the first-wins default pointed the encoder/VAAPI probe at the wrong
// node. pickRenderNode must prefer the real GPU (vendor in the gpuVendors table), while
// staying first-wins on single-GPU hosts.
func TestPickRenderNode_PrefersRealGPUOverVirtio(t *testing.T) {
	vendors := map[string]string{"0x10de": "NVIDIA", "0x1002": "AMD", "0x8086": "Intel"}
	orig := renderNodeVendor
	defer func() { renderNodeVendor = orig }()

	// GPU-passthrough VM: virtio head (renderD128) + NVIDIA (renderD129).
	renderNodeVendor = func(node string) string {
		switch filepath.Base(node) {
		case "renderD128":
			return "0x1af4" // virtio-gpu (not in the gpuVendors table)
		case "renderD129":
			return "0x10de" // NVIDIA
		}
		return ""
	}
	if got := pickRenderNode([]string{"/dev/dri/renderD128", "/dev/dri/renderD129", "/dev/kfd"}, vendors); got != "/dev/dri/renderD129" {
		t.Fatalf("VM: want /dev/dri/renderD129 (NVIDIA), got %q", got)
	}

	// AMD-only host: the single render node is the AMD GPU — first-wins holds.
	renderNodeVendor = func(string) string { return "0x1002" }
	if got := pickRenderNode([]string{"/dev/dri/renderD128"}, vendors); got != "/dev/dri/renderD128" {
		t.Fatalf("AMD host: want /dev/dri/renderD128, got %q", got)
	}

	// No vendor info (unreadable /sys): fall back to the first render node.
	renderNodeVendor = func(string) string { return "" }
	if got := pickRenderNode([]string{"/dev/dri/renderD128", "/dev/dri/renderD129"}, vendors); got != "/dev/dri/renderD128" {
		t.Fatalf("fallback: want first /dev/dri/renderD128, got %q", got)
	}

	// No render nodes present → empty (kfd/kvm are not render nodes).
	if got := pickRenderNode([]string{"/dev/kfd", "/dev/kvm"}, vendors); got != "" {
		t.Fatalf("no render nodes: want empty, got %q", got)
	}
}

// TestGpuUsableViaCDI exercises the pure decision helper that backs defaultDetectGPU.
// The contract: GPU is usable only when the driver is loaded AND CDI is achievable
// (existing spec OR nvidia-ctk on PATH).
func TestGpuUsableViaCDI(t *testing.T) {
	missing := func(string) error { return os.ErrNotExist }
	present := func(string) error { return nil }
	specAt := func(target string) func(string) error {
		return func(p string) error {
			if p == target {
				return nil
			}
			return os.ErrNotExist
		}
	}

	tests := []struct {
		name         string
		driverLoaded bool
		statFn       func(string) error
		lookPathFn   func(string) error
		want         bool
	}{
		{"driver missing → false even when CDI tooling is present", false, present, present, false},
		{"driver loaded + no spec + no nvidia-ctk → false (the bug we fixed)", true, missing, missing, false},
		{"driver loaded + spec at /etc/cdi/nvidia.yaml → true", true, specAt("/etc/cdi/nvidia.yaml"), missing, true},
		{"driver loaded + spec at /var/run/cdi/nvidia.yaml → true", true, specAt("/var/run/cdi/nvidia.yaml"), missing, true},
		{"driver loaded + no spec + nvidia-ctk on PATH → true (ensureCDI can generate)", true, missing, present, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gpuUsableViaCDI(tc.driverLoaded, tc.statFn, tc.lookPathFn)
			if got != tc.want {
				t.Errorf("gpuUsableViaCDI(driverLoaded=%v) = %v, want %v", tc.driverLoaded, got, tc.want)
			}
		})
	}
}
