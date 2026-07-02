package main

import (
	"fmt"
)

// --- Distro Config ---

// DistroConfig represents the `distro:` section of the embedded vocabulary (charly/charly.yml).
// Each distro defines bootstrap behavior AND package format definitions.
type DistroConfig struct {
	Distro map[string]*DistroDef `yaml:"distro" json:"distro"`
}

// PhaseTemplate looks up the template string for a (phase, venue)
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

// ResolveDistro finds the distro definition matching the image's distro tags.
// Walks tags in order, strips :version suffix to match base distro name.
// Follows inherits: chains with cycle detection, inheriting formats from parent.
func (dc *DistroConfig) ResolveDistro(distroTags []string) *DistroDef {
	if dc == nil {
		return nil
	}
	for _, tag := range distroTags {
		// Try exact match first (e.g., "fedora:43")
		if def, ok := dc.Distro[tag]; ok {
			return dc.resolveInherits(def, 10)
		}
		// Try base name (e.g., "fedora" from "fedora:43")
		base := tag
		if idx := indexOf(tag, ':'); idx >= 0 {
			base = tag[:idx]
		}
		if def, ok := dc.Distro[base]; ok {
			return dc.resolveInherits(def, 10)
		}
	}
	return nil
}

func (dc *DistroConfig) resolveInherits(def *DistroDef, maxDepth int) *DistroDef {
	if def.Inherits == "" || maxDepth <= 0 {
		return def
	}
	parent, ok := dc.Distro[def.Inherits]
	if !ok {
		return def
	}
	resolved := dc.resolveInherits(parent, maxDepth-1)

	// "Child wins per non-nil/non-empty field, else inherit from parent"
	// applied uniformly across every optional sub-block. This pattern
	// scales as new sub-blocks are added (Pacstrap, Bootloader, etc.).
	pickPtr := func(child, parent any) any {
		// Caller passes typed pointers; return whichever is non-nil.
		if child != nil && !isNilPtr(child) {
			return child
		}
		return parent
	}
	_ = pickPtr // (placeholder; explicit per-field merges below for clarity)

	baseUser := def.BaseUser
	if baseUser == nil {
		baseUser = resolved.BaseUser
	}
	pacstrap := def.Pacstrap
	if pacstrap == nil {
		pacstrap = resolved.Pacstrap
	}
	debootstrap := def.Debootstrap
	if debootstrap == nil {
		debootstrap = resolved.Debootstrap
	}
	alpineBootstrap := def.AlpineBootstrap
	if alpineBootstrap == nil {
		alpineBootstrap = resolved.AlpineBootstrap
	}
	bootloader := def.Bootloader
	if bootloader == nil {
		bootloader = resolved.Bootloader
	}
	dnf := def.Dnf
	if dnf == nil {
		dnf = resolved.Dnf
	}
	version := def.Version
	if version == "" {
		version = resolved.Version
	}

	if def.Bootstrap.InstallCmd != "" {
		// Child has its own bootstrap. Merge inherited optional sub-blocks
		// onto it.
		formats := def.Format
		if len(formats) == 0 {
			formats = resolved.Format
		}
		merged := &DistroDef{
			Inherits:        def.Inherits,
			Version:         version,
			Bootstrap:       def.Bootstrap,
			Workarounds:     def.Workarounds,
			Format:          formats,
			BaseUser:        baseUser,
			Pacstrap:        pacstrap,
			Debootstrap:     debootstrap,
			AlpineBootstrap: alpineBootstrap,
			Bootloader:      bootloader,
			Dnf:             dnf,
		}
		return merged
	}
	// Child has no bootstrap — inherit parent's bootstrap + workarounds,
	// overlay child's formats / baseuser / new sub-blocks.
	formats := resolved.Format
	if len(def.Format) > 0 {
		formats = def.Format
	}
	merged := &DistroDef{
		Inherits:        def.Inherits,
		Version:         version,
		Bootstrap:       resolved.Bootstrap,
		Workarounds:     resolved.Workarounds,
		Format:          formats,
		BaseUser:        baseUser,
		Pacstrap:        pacstrap,
		Debootstrap:     debootstrap,
		AlpineBootstrap: alpineBootstrap,
		Bootloader:      bootloader,
		Dnf:             dnf,
	}
	return merged
}

// isNilPtr is a small helper used by resolveInherits's per-field merge
// pattern. Returns true for typed nil pointers; false for everything else.
func isNilPtr(v any) bool {
	if v == nil {
		return true
	}
	// Reflection-free shortcut: the merger only passes nilable pointer
	// types; non-pointers reach here with non-nil interface boxes.
	switch p := v.(type) {
	case *BaseUserDef:
		return p == nil
	case *PacstrapDef:
		return p == nil
	case *DebootstrapDef:
		return p == nil
	case *AlpineBootstrapDef:
		return p == nil
	case *BootloaderDef:
		return p == nil
	}
	return false
}

// AllFormatNames returns a sorted, deduplicated list of all format names across all distros.
func (dc *DistroConfig) AllFormatNames() []string {
	if dc == nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, distro := range dc.Distro {
		resolved := dc.resolveInherits(distro, 10)
		for name := range resolved.Format {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// distroTagChain builds the most-specific-first distro tag chain used by the
// cascade resolver: [<distro>:<version>, <distro>] when a canonical version is
// known (e.g. ["ubuntu:24.04", "ubuntu"]), or just [<distro>] for a rolling
// distro with no version (arch/cachyos). It is the single helper that gives VM
// deploys the same per-version reach image builds get from their authored
// distro: tags — syntheticVmBox uses it so a target:vm deploy of an ubuntu
// guest can select an `ubuntu-24.04` tag section, not only the bare `ubuntu` one.
func distroTagChain(distro, version string) []string {
	if distro == "" {
		return nil
	}
	if version == "" {
		return []string{distro}
	}
	return []string{distro + ":" + version, distro}
}

// bareDistroName strips an optional ":<version>" suffix from a distro tag,
// returning the base distro name (`debian:13` → `debian`, `cachyos` → `cachyos`).
func bareDistroName(tag string) string {
	if i := indexOf(tag, ':'); i >= 0 {
		return tag[:i]
	}
	return tag
}

// expandPackageInheritance appends, AFTER the authored distro tags, every
// `inherits:` ancestor of a tag whose distro def opts into package inheritance
// via `inherit_packages: true` — most-specific authored tags kept first,
// ancestors appended as the least-specific levels. This is the SOLE driver of
// package-cascade inheritance, sourced entirely from the embedded build vocabulary:
//
//   - cachyos (inherits arch, inherit_packages: true) → [cachyos, arch]: an
//     `arch:` candy block reaches cachyos.
//   - arch (no inherit_packages)                       → [arch]: unchanged.
//   - ubuntu (inherits debian, no flag)                → [ubuntu]: debian
//     package sections do NOT leak onto ubuntu.
//
// The walk is transitive (a grandparent flagged on each hop is followed) and
// dedup-guarded against an already-present ancestor (a box that authored
// `[cachyos, arch]` explicitly resolves to the same set). Versioned tags are
// matched by bare name. Returns the input unchanged when dc is nil.
func (dc *DistroConfig) expandPackageInheritance(tags []string) []string {
	if dc == nil || len(tags) == 0 {
		return tags
	}
	out := append([]string(nil), tags...)
	seen := map[string]bool{}
	for _, t := range tags {
		seen[bareDistroName(t)] = true
	}
	for _, t := range tags {
		name := bareDistroName(t)
		for {
			def := dc.Distro[name]
			if def == nil || !def.InheritPackages || def.Inherits == "" {
				break
			}
			parent := def.Inherits
			if !seen[parent] {
				out = append(out, parent)
				seen[parent] = true
			}
			name = parent
		}
	}
	return out
}

// FindFormat returns the FormatDef for a format name (rpm/deb/pac/aur),
// resolving distro `inherits:` chains. The first distro that defines the format
// wins — a format's templates (install/uninstall, phase cells, cache mounts)
// are identical across same-format distros (the format IS the package-manager
// contract), so any distro's definition is the correct one for a host deploy
// keyed purely on the package format. Returns nil when no distro defines it.
// Used by the host-venue package install/uninstall renderers (the SAME FormatDef
// the OCI container path reads via t.DistroDef.Format[name]).
func (dc *DistroConfig) FindFormat(name string) *FormatDef {
	if dc == nil {
		return nil
	}
	for _, distro := range dc.Distro {
		resolved := dc.resolveInherits(distro, 10)
		if fd := resolved.Format[name]; fd != nil {
			return fd
		}
	}
	return nil
}

// wrapDistroDef presents one already-resolved DistroDef as a DistroConfig so the format-keyed
// FindFormat resolver returns that def's FormatDef. The pod-overlay build-emit (OCITarget) carries a
// box-resolved DistroDef, not a full DistroConfig; wrapping it lets the step-emit host-builder
// resolve the FormatDef through the SAME DistroConfig.FindFormat path the host deploy render uses
// (renderHostPackageCommand) — one format-resolution shape across build + deploy (R3). FindFormat on
// the single wrapped def yields exactly def.Format[name] (the def is already inherit-resolved), so
// the emitted fragment matches what the former in-proc OCITarget SystemPackages build-emit produced
// from t.DistroDef.Format[name]. Returns nil for a nil def (FindFormat then yields nil → the caller
// reports "no distro definition").
func wrapDistroDef(def *DistroDef) *DistroConfig {
	if def == nil {
		return nil
	}
	return &DistroConfig{Distro: map[string]*DistroDef{"resolved": def}}
}

// ValidFormat returns true if any distro defines this format name.
func (dc *DistroConfig) ValidFormat(name string) bool {
	if dc == nil {
		return false
	}
	for _, distro := range dc.Distro {
		resolved := dc.resolveInherits(distro, 10)
		if _, ok := resolved.Format[name]; ok {
			return true
		}
	}
	return false
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// --- Builder Config ---

// BuilderConfig represents the `builder:` section of the embedded vocabulary (charly/charly.yml).
type BuilderConfig struct {
	Builder map[string]*BuilderDef `yaml:"builder" json:"builder"`
}

// PhaseTemplate is the BuilderDef analog of FormatDef.PhaseTemplate.
// Same fallback rules apply: (PhaseInstall, container) falls back to
// legacy InstallTemplate or StageTemplate when Phases is absent.
//
//nolint:unparam // uniform phase-dispatch signature mirroring formatPhaseTemplate (which DOES vary phase via build_target_oci.go s.Phase); the phase param dispatches over the shared BuilderDef.Phases PhaseSet — builders author only the install phase today, but the schema + roadmap (install_build.go "Task 4 will split templates into phases") permit prepare/cleanup.
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
	// Legacy fallbacks. Builders have two legacy fields: Inline builders
	// (cargo) use InstallTemplate; non-inline (pixi/npm/aur) use
	// StageTemplate. The host path needs the container-shaped template to
	// synthesize a podman-run equivalent.
	if phase == PhaseInstall && venue == VenueContainerBuilder {
		if b.Inline && b.InstallTemplate != "" {
			return b.InstallTemplate
		}
		return b.StageTemplate
	}
	return ""
}

// ValidBuilderType returns true if the given name is a defined builder.
func (bc *BuilderConfig) ValidBuilderType(name string) bool {
	if bc == nil {
		return false
	}
	_, ok := bc.Builder[name]
	return ok
}

// BuilderNames returns sorted list of defined builder names.
func (bc *BuilderConfig) BuilderNames() []string {
	if bc == nil {
		return nil
	}
	names := make([]string, 0, len(bc.Builder))
	for name := range bc.Builder {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// --- Loading ---
//
// ResolveFormatConfigData has been removed. Build config resolution now goes
// through LoadUnified — which reads charly.yml + includes: (local and
// remote-ref). See charly/unified.go.

// BuildFile is the on-disk schema of build.yml — three optional top-level
// sections that map directly onto DistroConfig/BuilderConfig/InitConfig.
type BuildFile struct {
	Distro  map[string]*DistroDef  `yaml:"distro" json:"distro"`
	Builder map[string]*BuilderDef `yaml:"builder" json:"builder"`
	Init    map[string]*InitDef    `yaml:"init" json:"init"`
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
		return nil, nil, nil, fmt.Errorf("no charly.yml found in %s (run `charly box new project .` to scaffold one)", dir)
	}
	return uf.ProjectDistroConfig(), uf.ProjectBuilderConfig(), uf.ProjectInitConfig(), nil
}

// LoadDefaultBuildConfig is retained as an alias for the single-argument form.
// Former call sites pass just the project directory; the legacy (defaultRef,
// dir) two-argument form is gone.
func LoadDefaultBuildConfig(dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	return LoadBuildConfigForBox(dir)
}
