package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// nvidiaReport builds a synthetic VFIOReport with one NVIDIA GPU (vendor
// 0x10de) whose IOMMU group has two functions (the canonical RTX 4080 shape:
// VGA + audio), plus an AMD display GPU that must NOT be selected.
func nvidiaReport() VFIOReport {
	// VFIOGpu is flattened (SDD conversion) — spread via the shared
	// spec.NewVFIOGpu constructor instead of the former embedded-field literal.
	nvidia := spec.NewVFIOGpu(spec.VFIOPCIDevice{Addr: "0000:01:00.0", VendorID: "0x10de", DeviceID: "0x2702", IOMMUGroup: 13, Driver: "vfio-pci"})
	nvidia.GroupMembers = []VFIOPCIDevice{
		{Addr: "0000:01:00.0", VendorID: "0x10de", IOMMUGroup: 13},
		{Addr: "0000:01:00.1", VendorID: "0x10de", IOMMUGroup: 13},
	}
	amd := spec.NewVFIOGpu(spec.VFIOPCIDevice{Addr: "0000:19:00.0", VendorID: "0x1002", DeviceID: "0x13c0", IOMMUGroup: 25, Driver: "amdgpu"})
	amd.GroupMembers = []VFIOPCIDevice{{Addr: "0000:19:00.0", VendorID: "0x1002", IOMMUGroup: 25}}
	return VFIOReport{
		IOMMUEnabled: true,
		IOMMUKind:    "amd",
		GPUs:         []VFIOGpu{nvidia, amd},
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

func TestRequiredGPUResource(t *testing.T) {
	resources := map[string]*ResolvedResource{"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}}}
	node := spec.BundleNode{Target: "vm", From: "gpu-vm", RequiresExclusive: []string{"nvidia-gpu"}}
	tok, sel, ok := requiredGPUResource(&node, resources)
	if !ok || tok != "nvidia-gpu" || sel.Vendor != "0x10de" {
		t.Fatalf("requiredGPUResource = (%q,%v,%v), want nvidia-gpu/0x10de/true", tok, sel, ok)
	}
	// A token with no gpu selector (free arbitration token) → not a GPU resource.
	free := map[string]*ResolvedResource{"some-lock": {}}
	if _, _, ok := requiredGPUResource(&spec.BundleNode{RequiresExclusive: []string{"some-lock"}}, free); ok {
		t.Error("a selector-less resource token must not trigger GPU allocation")
	}
	if _, _, ok := requiredGPUResource(nil, resources); ok {
		t.Error("nil claimant → no GPU resource")
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

// The GPU auto-allocation tests (TestVfioGpuToHostdevs / TestAutoAllocate_* /
// TestWriteInstanceOverride_PreservesClassification) moved to candy/plugin-vm
// with the `charly vm create` create pipeline (autoAllocateExclusiveGPUs +
// vfioGpuToHostdevs + the instance-override persistence). Core keeps only the
// resource-vocabulary predicates tested above.
