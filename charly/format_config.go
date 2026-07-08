package main

import (
	"fmt"
)

// The DistroConfig / BuilderConfig types and their vocabulary-resolution methods
// (ResolveDistro / FindFormat / AllFormatNames / ExpandPackageInheritance /
// ValidBuilderType / BuilderNames / distroTagChain / bareDistroName / wrapDistroDef)
// live in sdk/buildkit now (P3), aliased back via buildkit_aliases.go. This file
// keeps the pieces that stay charly-side: the Phase/Venue-coupled phase-template
// resolvers (Phase/Venue are the in-core IR enums) and the loader glue.

// formatPhaseTemplate looks up the template string for a (phase, venue)
// lookup, with documented fallback behavior: if the new phase: block
// lacks the requested cell, fall back to the legacy InstallTemplate for
// (PhaseInstall, container) only — the combination covered by the
// legacy field. All other lookups return "" when the new path is absent.
func formatPhaseTemplate(f *FormatDef, phase Phase, venue Venue) string {
	if f == nil {
		return ""
	}
	if f.Phases != nil {
		var pt *PhaseTemplates
		switch phase {
		case PhasePrepare:
			pt = f.Phases.Prepare
		case PhaseInstall:
			pt = f.Phases.Install
		case PhaseCleanup:
			pt = f.Phases.Cleanup
		}
		if pt != nil {
			switch venue {
			case VenueHostNative:
				if pt.Host != "" {
					return pt.Host
				}
			case VenueContainerBuilder:
				if pt.Container != "" {
					return pt.Container
				}
			}
		}
	}
	// Legacy fallback: the old InstallTemplate only describes the
	// install-phase in container venue.
	if phase == PhaseInstall && venue == VenueContainerBuilder {
		return f.InstallTemplate
	}
	return ""
}

// builderPhaseTemplate is the BuilderDef analog of formatPhaseTemplate.
// Same fallback rules apply: (PhaseInstall, container) falls back to the
// legacy inline InstallTemplate when Phases is absent.
//
//nolint:unparam // uniform phase-dispatch signature mirroring formatPhaseTemplate (which DOES vary phase via build_target_oci.go s.Phase); the phase param dispatches over the shared BuilderDef.Phases PhaseSet — builders author only the install phase today, but the schema + roadmap permit prepare/cleanup.
func builderPhaseTemplate(b *BuilderDef, phase Phase, venue Venue) string {
	if b == nil {
		return ""
	}
	if b.Phases != nil {
		var pt *PhaseTemplates
		switch phase {
		case PhasePrepare:
			pt = b.Phases.Prepare
		case PhaseInstall:
			pt = b.Phases.Install
		case PhaseCleanup:
			pt = b.Phases.Cleanup
		}
		if pt != nil {
			switch venue {
			case VenueHostNative:
				if pt.Host != "" {
					return pt.Host
				}
			case VenueContainerBuilder:
				if pt.Container != "" {
					return pt.Container
				}
			}
		}
	}
	// Legacy fallback: an inline builder (cargo) uses InstallTemplate for the
	// container-shaped template. Multi-stage builders render via their plugin's
	// OpResolve (kit.BuilderResolve), NOT this fallback.
	if phase == PhaseInstall && venue == VenueContainerBuilder && b.Inline && b.InstallTemplate != "" {
		return b.InstallTemplate
	}
	return ""
}

// --- Loading ---
//
// Build config resolution goes through LoadUnified — which reads charly.yml +
// includes: (local and remote-ref). See charly/unified.go.

// BuildFile is the on-disk schema of build.yml — three optional top-level
// sections that map directly onto DistroConfig/BuilderConfig/InitConfig.
type BuildFile struct {
	Distro  map[string]*DistroDef    `yaml:"distro" json:"distro"`
	Builder map[string]*BuilderDef   `yaml:"builder" json:"builder"`
	Init    map[string]*ResolvedInit `yaml:"init" json:"init"`
}

// LoadBuildConfigForBox loads distro, builder, and init configs for the
// project at dir. Post-unified-cutover this reads from charly.yml (via
// LoadUnified) rather than following a format_config: pointer.
//
// The init section is optional: projects without an `inits:` block return a
// nil *InitConfig (no init system, no entrypoint beyond the base image default).
func LoadBuildConfigForBox(dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, nil, nil, noCharlyYmlErr(dir)
	}
	return uf.ProjectDistroConfig(), uf.ProjectBuilderConfig(), uf.ProjectInitConfig(), nil
}

// LoadDefaultBuildConfig is retained as an alias for the single-argument form.
func LoadDefaultBuildConfig(dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	return LoadBuildConfigForBox(dir)
}
