package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
)

// TestWriteContextIgnore was removed alongside the dead charly.Generator.writeContextIgnore
// wrapper (K3 host-prep move, coneB-render): writeContextIgnore moved to
// candy/plugin-build/host_prep.go (a pure function of dir/cfg/baseline — no host-only
// dependency). Its coverage moved WITH the logic: candy/plugin-build/host_prep_test.go carries
// the equivalent test against the real implementation, using a literal baseline slice mirroring
// charly.yml's context_ignore_baseline: (a separate module can't //go:embed it).

// TestRenderDnfConfWrite covers the dnf.conf bootstrap fragment (Item 4).
func TestRenderDnfConfWrite(t *testing.T) {
	if got := deploykit.RenderDnfConfWrite(nil); got != "" {
		t.Errorf("nil Dnf should render empty, got %q", got)
	}
	if got := deploykit.RenderDnfConfWrite(&vmshared.DnfConfig{}); got != "" {
		t.Errorf("zero Dnf should render empty, got %q", got)
	}
	got := deploykit.RenderDnfConfWrite(&vmshared.DnfConfig{MaxParallelDownloads: 10, Fastestmirror: true})
	for _, want := range []string{"max_parallel_downloads=10", "fastestmirror=True", ">> /etc/dnf/dnf.conf", "&& \\"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered dnf.conf fragment missing %q, got: %q", want, got)
		}
	}
	// Only one knob set → only that line.
	onlyParallel := deploykit.RenderDnfConfWrite(&vmshared.DnfConfig{MaxParallelDownloads: 5})
	if strings.Contains(onlyParallel, "fastestmirror") {
		t.Errorf("fastestmirror should be absent when unset, got %q", onlyParallel)
	}
}
