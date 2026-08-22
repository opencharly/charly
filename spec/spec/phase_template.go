package spec

// phase_template.go — the (phase, venue) → template-string resolvers for the build-vocabulary
// Format / Builder. Pure over the CUE-sourced spec types (Format, Builder, Phase/Venue enums).
// #55 import-purity: relocated from sdk/buildkit DOWN to spec (the value leaf) so charly core reads
// them over its spec+proto-only import surface; sdk/buildkit keeps thin var-forwarders for its
// build-render callers.

// FormatPhaseTemplate looks up the template string for a (phase, venue)
// lookup, with documented fallback behavior: if the new phase: block
// lacks the requested cell, fall back to the legacy InstallTemplate for
// (PhaseInstall, container) only — the combination covered by the
// legacy field. All other lookups return "" when the new path is absent.
func FormatPhaseTemplate(f *Format, phase Phase, venue Venue) string {
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

// BuilderPhaseTemplate is the Builder analog of FormatPhaseTemplate.
// Same fallback rules apply: (PhaseInstall, container) falls back to the
// legacy inline InstallTemplate when Phases is absent.
func BuilderPhaseTemplate(b *Builder, phase Phase, venue Venue) string {
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
