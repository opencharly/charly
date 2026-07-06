package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nvidiaReport builds a synthetic VFIOReport with one NVIDIA GPU (vendor
// 0x10de) whose IOMMU group has two functions (the canonical RTX 4080 shape:
// VGA + audio), plus an AMD display GPU that must NOT be selected.
func nvidiaReport() VFIOReport {
	return VFIOReport{
		IOMMUEnabled: true,
		IOMMUKind:    "amd",
		GPUs: []VFIOGpu{
			{
				VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0", VendorID: "0x10de", DeviceID: "0x2702", IOMMUGroup: 13, Driver: "vfio-pci"},
				GroupMembers: []VFIOPCIDevice{
					{Addr: "0000:01:00.0", VendorID: "0x10de", IOMMUGroup: 13},
					{Addr: "0000:01:00.1", VendorID: "0x10de", IOMMUGroup: 13},
				},
			},
			{
				VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:19:00.0", VendorID: "0x1002", DeviceID: "0x13c0", IOMMUGroup: 25, Driver: "amdgpu"},
				GroupMembers:  []VFIOPCIDevice{{Addr: "0000:19:00.0", VendorID: "0x1002", IOMMUGroup: 25}},
			},
		},
	}
}

func TestNormalizePCIVendor(t *testing.T) {
	cases := map[string]string{
		"0x10de": "0x10de", "10de": "0x10de", "0X10DE": "0x10de", "10DE": "0x10de", "": "",
	}
	for in, want := range cases {
		if got := normalizePCIVendor(in); got != want {
			t.Errorf("normalizePCIVendor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSelectGPUByVendor(t *testing.T) {
	rep := nvidiaReport()
	g, ok := selectGPUByVendor(rep, "10DE") // case/prefix-insensitive
	if !ok {
		t.Fatal("expected to select the NVIDIA GPU")
	}
	if g.Addr != "0000:01:00.0" {
		t.Errorf("selected %s, want 0000:01:00.0 (NVIDIA, not the AMD card)", g.Addr)
	}
	if _, ok := selectGPUByVendor(rep, "0x8086"); ok {
		t.Error("expected no match for absent Intel vendor 0x8086")
	}
	if _, ok := selectGPUByVendor(VFIOReport{}, "0x10de"); ok {
		t.Error("expected no match on an empty report")
	}
}

func TestVfioGpuToHostdevs(t *testing.T) {
	g, _ := selectGPUByVendor(nvidiaReport(), "0x10de")
	hostdevs := vfioGpuToHostdevs(g.GroupMembers)
	if len(hostdevs) != 2 {
		t.Fatalf("got %d hostdevs, want 2 (GPU + audio function)", len(hostdevs))
	}
	h0 := hostdevs[0]
	if h0.Type != "pci" || h0.Managed != "yes" {
		t.Errorf("hostdev[0] type=%q managed=%q, want pci/yes", h0.Type, h0.Managed)
	}
	want := map[string]string{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"}
	for k, v := range want {
		if h0.Source[k] != v {
			t.Errorf("hostdev[0].source[%s] = %q, want %q", k, h0.Source[k], v)
		}
	}
	if hostdevs[1].Source["function"] != "0x1" {
		t.Errorf("hostdev[1] function = %q, want 0x1", hostdevs[1].Source["function"])
	}
	// renderHostdevsBlock (charly vm gpu list) must agree with the structured form (R3).
	block := renderHostdevsBlock(g.GroupMembers)
	if strings.Count(block, "- type: pci") != 2 {
		t.Errorf("renderHostdevsBlock emitted %q; expected 2 pci entries", block)
	}
}

func TestRequiredGPUResource(t *testing.T) {
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}
	node := BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	tok, sel, ok := requiredGPUResource(&node, resources)
	if !ok || tok != "nvidia-gpu" || sel.Vendor != "0x10de" {
		t.Fatalf("requiredGPUResource = (%q,%v,%v), want nvidia-gpu/0x10de/true", tok, sel, ok)
	}
	// A token with no gpu selector (free arbitration token) → not a GPU resource.
	free := map[string]*ResolvedResource{"some-lock": {}}
	if _, _, ok := requiredGPUResource(&BundleNode{RequiresExclusive: []string{"some-lock"}}, free); ok {
		t.Error("a selector-less resource token must not trigger GPU allocation")
	}
	if _, _, ok := requiredGPUResource(nil, resources); ok {
		t.Error("nil claimant → no GPU resource")
	}
}

func TestAutoAllocate_NoClaimant_NoOp(t *testing.T) {
	spec := &VmSpec{}
	got, err := autoAllocateExclusiveGPUs(spec, nil, nil, nil, "charly-x", "libvirt")
	if err != nil || got != nil {
		t.Fatalf("no-claimant should be a no-op, got (%v,%v)", got, err)
	}
}

func TestAutoAllocate_HitPersistsAndInjects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := DetectVFIO
	DetectVFIO = nvidiaReport
	defer func() { DetectVFIO = orig }()

	spec := &VmSpec{}
	node := &BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}

	ovr, err := autoAllocateExclusiveGPUs(spec, nil, node, resources, "charly-gpu-vm", "libvirt")
	if err != nil {
		t.Fatalf("auto-allocate with a matching card must succeed: %v", err)
	}
	if !ovrHasHostdev(ovr) || len(ovr.Libvirt.Devices.Hostdevs) != 2 {
		t.Fatalf("expected 2 hostdevs in the returned override, got %#v", ovr)
	}
	// Persisted to instance.yml, re-readable + preserved across a reload.
	path, _ := VmInstanceOverridePath("charly-gpu-vm")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected instance.yml written at %s: %v", path, err)
	}
	reloaded, err := LoadVmInstanceOverride("charly-gpu-vm")
	if err != nil || !ovrHasHostdev(reloaded) {
		t.Fatalf("reloaded instance.yml lost its hostdevs: %v / %#v", err, reloaded)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Auto-allocated") {
		t.Errorf("expected provenance header comment in %s", path)
	}
}

func TestAutoAllocate_MissFailsHard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := DetectVFIO
	DetectVFIO = func() VFIOReport { return VFIOReport{IOMMUEnabled: true} } // no GPUs
	defer func() { DetectVFIO = orig }()

	node := &BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}
	_, err := autoAllocateExclusiveGPUs(&VmSpec{}, nil, node, resources, "charly-gpu-vm", "libvirt")
	if err == nil {
		t.Fatal("a required-but-absent GPU resource must FAIL HARD, got nil error")
	}
	if !strings.Contains(err.Error(), "nvidia-gpu") || !strings.Contains(err.Error(), "0x10de") {
		t.Errorf("fail-hard error should name the token + vendor: %v", err)
	}
}

func TestAutoAllocate_OperatorHostdevWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := DetectVFIO
	DetectVFIO = func() VFIOReport {
		t.Fatal("DetectVFIO must NOT run when a hostdev is already configured")
		return VFIOReport{}
	}
	defer func() { DetectVFIO = orig }()

	// vm.yml already committed a hostdev → auto-allocation defers, no detect.
	spec := &VmSpec{Libvirt: &LibvirtDomain{Devices: &LibvirtDevices{Hostdevs: []LibvirtHostdev{{Type: "pci"}}}}}
	node := &BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}
	if _, err := autoAllocateExclusiveGPUs(spec, nil, node, resources, "charly-gpu-vm", "libvirt"); err != nil {
		t.Fatalf("operator-hostdev path must be a no-op, got %v", err)
	}
}

func TestAutoAllocate_QemuBackendRejected(t *testing.T) {
	node := &BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}
	_, err := autoAllocateExclusiveGPUs(&VmSpec{}, nil, node, resources, "charly-gpu-vm", "qemu")
	if err == nil || !strings.Contains(err.Error(), "libvirt") {
		t.Fatalf("qemu backend must be rejected for GPU passthrough, got %v", err)
	}
}

// TestResourceKind_Loads verifies a node-form resource: kind loads through the plugin
// path (runPluginKind → uf.PluginKinds["resource"], validated against the served
// #ResourceInput) and is read back into the typed map[string]*ResolvedResource by the
// Resources() accessor — resource is a plugin kind now (candy/plugin-resource), no longer a
// typed core map (the former uf.Resource).
func TestResourceKind_Loads(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + LatestSchemaVersion().String() + `"
nvidia-gpu:
  resource:
    gpu:
      vendor: "0x10de"
some-lock:
  resource: {}
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified resource plugin kind: %v", err)
	}
	resources := uf.resolveResources()
	if resources["nvidia-gpu"] == nil || resources["nvidia-gpu"].Gpu == nil ||
		resources["nvidia-gpu"].Gpu.Vendor != "0x10de" {
		t.Fatalf("resource nvidia-gpu did not parse: %#v", resources["nvidia-gpu"])
	}
	if resources["nvidia-gpu"].Gpu == nil {
		t.Error("nvidia-gpu should carry a gpu selector")
	}
	if resources["some-lock"] == nil || resources["some-lock"].Gpu != nil {
		t.Error("selector-less some-lock should carry no gpu selector")
	}
}

// (The former TestMergeResourceMap_RootWins was removed with mergeResourceMap: resource
// is a plugin kind now, so the root-wins name-keyed merge is mergePluginKindsMap —
// covered by TestMergePluginKindsMap_NameKeyedOverride + the resource arm of
// TestEmbeddedDefaults_AllVocabKindsOverridable.)

// instance.yml round-trip helper sanity: writeInstanceOverrideHostdevs preserves
// disposable/lifecycle alongside the auto-written hostdevs.
func TestWriteInstanceOverride_PreservesClassification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	disp := true
	ovr := &VmInstanceOverride{
		Disposable: &disp,
		Lifecycle:  "test",
		Libvirt:    &LibvirtDomain{Devices: &LibvirtDevices{Hostdevs: vfioGpuToHostdevsFromVendor(t)}},
	}
	if err := writeInstanceOverrideHostdevs("charly-gpu-vm", ovr, "# hdr\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	reloaded, err := LoadVmInstanceOverride("charly-gpu-vm")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Disposable == nil || !*reloaded.Disposable || reloaded.Lifecycle != "test" {
		t.Errorf("classification not preserved: %#v", reloaded)
	}
	if !ovrHasHostdev(reloaded) {
		t.Error("hostdevs not preserved")
	}
	path, _ := VmInstanceOverridePath("charly-gpu-vm")
	abs, _ := filepath.Abs(path)
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("file missing: %v", err)
	}
}

func vfioGpuToHostdevsFromVendor(t *testing.T) []LibvirtHostdev {
	t.Helper()
	g, _ := selectGPUByVendor(nvidiaReport(), "0x10de")
	return vfioGpuToHostdevs(g.GroupMembers)
}
