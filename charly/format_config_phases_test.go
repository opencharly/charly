package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// Tests for the new PhaseSet / PhaseTemplates lookup added for the
// BuildTarget refactor (Task 4). Verifies (a) fallback to legacy
// install_template when phases: is absent, (b) fallback never kicks in
// outside the (install, container) cell, (c) phase lookups return the
// correct cell when phases: is present.

func TestFormatDefPhaseTemplateLegacyFallback(t *testing.T) {
	// Legacy shape: only InstallTemplate set.
	f := &FormatDef{InstallTemplate: "RUN dnf install -y {{.Packages}}"}

	// (install, container) falls back to InstallTemplate.
	if got := formatPhaseTemplate(f, spec.PhaseInstall, spec.VenueContainerBuilder); got != f.InstallTemplate {
		t.Errorf("legacy fallback for (install, container) = %q, want %q", got, f.InstallTemplate)
	}
	// All other phase/venue combinations return "" (no legacy equivalent).
	for _, p := range []spec.Phase{spec.PhasePrepare, spec.PhaseInstall, spec.PhaseCleanup} {
		for _, v := range []spec.Venue{spec.VenueHostNative, spec.VenueContainerBuilder} {
			if p == spec.PhaseInstall && v == spec.VenueContainerBuilder {
				continue
			}
			if got := formatPhaseTemplate(f, p, v); got != "" {
				t.Errorf("expected empty template for (%v, %v), got %q", p, v, got)
			}
		}
	}
}

func TestFormatDefPhaseTemplateNewPathPreferred(t *testing.T) {
	f := &FormatDef{
		InstallTemplate: "RUN legacy",
		Phases: &vmshared.PhaseSet{
			Install: &vmshared.PhaseTemplates{
				Container: "RUN new-container",
				Host:      "new-host",
			},
			Prepare: &vmshared.PhaseTemplates{
				Container: "RUN prepare-container",
				Host:      "prepare-host",
			},
		},
	}

	// New path wins over legacy for (install, container).
	if got := formatPhaseTemplate(f, spec.PhaseInstall, spec.VenueContainerBuilder); got != "RUN new-container" {
		t.Errorf("(install, container) = %q, want RUN new-container", got)
	}
	// Host rendering comes from new path (no legacy equivalent).
	if got := formatPhaseTemplate(f, spec.PhaseInstall, spec.VenueHostNative); got != "new-host" {
		t.Errorf("(install, host) = %q, want new-host", got)
	}
	// Prepare is only in new path.
	if got := formatPhaseTemplate(f, spec.PhasePrepare, spec.VenueContainerBuilder); got != "RUN prepare-container" {
		t.Errorf("(prepare, container) = %q", got)
	}
	if got := formatPhaseTemplate(f, spec.PhasePrepare, spec.VenueHostNative); got != "prepare-host" {
		t.Errorf("(prepare, host) = %q", got)
	}
	// Cleanup phase is nil in PhaseSet → empty return.
	if got := formatPhaseTemplate(f, spec.PhaseCleanup, spec.VenueContainerBuilder); got != "" {
		t.Errorf("(cleanup, container) = %q, want empty", got)
	}
}

func TestFormatDefPhaseTemplateNilSafe(t *testing.T) {
	var f *FormatDef
	if got := formatPhaseTemplate(f, spec.PhaseInstall, spec.VenueContainerBuilder); got != "" {
		t.Errorf("nil FormatDef lookup = %q, want empty", got)
	}
}

func TestBuilderDefPhaseTemplateLegacyFallbacks(t *testing.T) {
	// Inline builder → falls back to InstallTemplate.
	inline := &BuilderDef{Inline: true, InstallTemplate: "RUN cargo install"}
	if got := builderPhaseTemplate(inline, spec.PhaseInstall, spec.VenueContainerBuilder); got != inline.InstallTemplate {
		t.Errorf("inline builder fallback = %q, want %q", got, inline.InstallTemplate)
	}
	// A non-inline builder without phases → empty for both venues: multi-stage
	// builders render via their plugin's OpResolve, not this fallback (the former
	// in-core stage template is gone, C10).
	multi := &BuilderDef{}
	if got := builderPhaseTemplate(multi, spec.PhaseInstall, spec.VenueContainerBuilder); got != "" {
		t.Errorf("multi-stage container fallback = %q, want empty", got)
	}
	if got := builderPhaseTemplate(multi, spec.PhaseInstall, spec.VenueHostNative); got != "" {
		t.Errorf("host-venue legacy = %q, want empty", got)
	}
}

func TestBuilderDefPathContributionsOptional(t *testing.T) {
	// Older build.yml entries don't have path_contributions — field is
	// optional and zero-value is nil/empty.
	b := &BuilderDef{}
	if len(b.PathContributions) != 0 {
		t.Errorf("default PathContributions len = %d, want 0", len(b.PathContributions))
	}
}
