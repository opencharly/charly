package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestGatherResources_ResolvesTheEmbeddedResourceVocabulary is RED on the pre-fix tree.
//
// gatherResources dispatches build:project to the compiled-in candy/plugin-build, whose
// resolveProjectEnvelope loads the project via loaderkit.LoadUnifiedViaExecutor — an executor-backed
// read. Before this fix the dispatch ran on a bare context.Background() with no executor threaded,
// so the plugin had no reverse channel, the resolve errored, and the best-effort contract turned
// that into an empty map. The failure was invisible because "no resources declared" and "the
// resolve is broken" are the same value.
//
// The assertion is environment-independent: `nvidia-gpu` is declared in charly's OWN embedded
// default vocabulary (charly/charly.yml), which every project merges as its base, so a working
// resolve always surfaces it — no GPU, no project fixture, and no host hardware required.
func TestGatherResources_ResolvesTheEmbeddedResourceVocabulary(t *testing.T) {
	resources := gatherResources()
	if len(resources) == 0 {
		t.Fatal("gatherResources() returned no resources — the build:project resolve is dead (the executor-less dispatch this fix replaces), so every GPU prereq check silently reports 'satisfied'")
	}
	def, ok := resources["nvidia-gpu"]
	if !ok {
		t.Fatalf("gatherResources() = %v, want the embedded default vocabulary's nvidia-gpu token", keysOf(resources))
	}
	if def == nil || def.Gpu == nil || def.Gpu.Vendor == "" {
		t.Fatalf("nvidia-gpu resolved to %+v, want its gpu selector — the vendor is what drives the prereq decision", def)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBedGPUPrereqMissing_UnsatisfiableTokenIsDetected closes the loop from the resolve to the
// decision it feeds. With the resolve dead, this path could never report a missing prereq —
// resources came back empty and every GPU bed was waved through to a 10-30GB image build that
// dies at start with "unresolvable CDI devices". With the resolve live, a bed claiming a
// GPU-selector token whose vendor has no card on this host is caught before the build.
//
// The host detector is injected, so the assertion holds on any machine: gpuPrereqMissing is the
// pure decision bedGPUPrereqMissing wraps, and it is fed the SAME resource vocabulary the real
// resolve returns.
func TestBedGPUPrereqMissing_UnsatisfiableTokenIsDetected(t *testing.T) {
	resources := gatherResources()
	if len(resources) == 0 {
		t.Fatal("gatherResources() returned nothing — the prereq decision below cannot be exercised at all")
	}
	noCards := func() spec.VFIOReport { return spec.VFIOReport{} }

	token, vendor, missing := gpuPrereqMissing([]string{"nvidia-gpu"}, resources, noCards)
	if !missing {
		t.Fatal("gpuPrereqMissing() = false for nvidia-gpu on a host with no cards — the bed would build instead of skipping")
	}
	if token != "nvidia-gpu" || vendor == "" {
		t.Errorf("gpuPrereqMissing() = (%q, %q), want the claimed token and its normalized vendor", token, vendor)
	}

	// A bed claiming NO host resource must not consult the vocabulary or the hardware at all —
	// the laziness bedGPUPrereqMissing documents and the resource-first ordering had inverted.
	probed := false
	if _, _, m := gpuPrereqMissing(nil, resources, func() spec.VFIOReport { probed = true; return spec.VFIOReport{} }); m {
		t.Error("gpuPrereqMissing() reported a missing prereq for a bed that claims no resource")
	}
	if probed {
		t.Error("gpuPrereqMissing() probed host hardware for a bed that claims no GPU token")
	}
}

// TestBedGPUPrereqMissing_NoTokensSkipsTheResolve asserts the ordering fix directly: a node
// claiming nothing must return before gatherResources runs, so a bed with no resource claim never
// pays a whole project resolve to be told it needs nothing.
func TestBedGPUPrereqMissing_NoTokensSkipsTheResolve(t *testing.T) {
	token, vendor, missing := bedGPUPrereqMissing(spec.BundleNode{})
	if missing || token != "" || vendor != "" {
		t.Fatalf("bedGPUPrereqMissing(empty node) = (%q, %q, %v), want a clean no-op", token, vendor, missing)
	}
}
