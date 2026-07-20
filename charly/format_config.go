package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/buildkit"
)

// The DistroConfig / BuilderConfig types and their vocabulary-resolution methods
// (ResolveDistro / FindFormat / AllFormatNames / ExpandPackageInheritance /
// ValidBuilderType / BuilderNames / distroTagChain / bareDistroName / wrapDistroDef)
// live in sdk/buildkit now (P3) — every charly/*.go caller references buildkit.DistroConfig /
// buildkit.BuilderConfig directly (K3 ZERO-ALIASES dissolved charly/buildkit_aliases.go). The
// (phase, venue) phase-template resolvers moved to sdk/buildkit too (P8b — they are
// PURE over the CUE-sourced spec types: FormatDef = spec.Format, BuilderDef =
// spec.Builder, Phase/Venue = spec enums); callers reference buildkit.FormatPhaseTemplate /
// buildkit.BuilderPhaseTemplate directly (K3 ZERO-ALIASES dissolution — this file keeps only
// the loader glue).

// --- Loading ---
//
// Build config resolution goes through LoadUnified — which reads charly.yml +
// includes: (local and remote-ref). See charly/unified.go.

// BuildFile is the on-disk schema of build.yml — three optional top-level
// sections that map directly onto DistroConfig/BuilderConfig/InitConfig.
type BuildFile struct {
	Distro  map[string]*spec.ResolvedDistro `yaml:"distro" json:"distro"`
	Builder map[string]*BuilderDef          `yaml:"builder" json:"builder"`
	Init    map[string]*ResolvedInit        `yaml:"init" json:"init"`
}

// LoadBuildConfigForBox loads distro, builder, and init configs for the
// project at dir. Post-unified-cutover this reads from charly.yml (via
// LoadUnified) rather than following a format_config: pointer.
//
// The init section is optional: projects without an `inits:` block return a
// nil *InitConfig (no init system, no entrypoint beyond the base image default).
func LoadBuildConfigForBox(dir string) (*buildkit.DistroConfig, *buildkit.BuilderConfig, *InitConfig, error) {
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
func LoadDefaultBuildConfig(dir string) (*buildkit.DistroConfig, *buildkit.BuilderConfig, *InitConfig, error) {
	return LoadBuildConfigForBox(dir)
}
