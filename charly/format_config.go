package main

import (
	"fmt"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/loaderkit"
)

// The DistroConfig / BuilderConfig types and their vocabulary-resolution methods
// (ResolveDistro / FindFormat / AllFormatNames / ExpandPackageInheritance /
// ValidBuilderType / BuilderNames / distroTagChain / bareDistroName / wrapDistroDef)
// live in sdk/buildkit now (P3) — every charly/*.go caller references spec.DistroConfig /
// spec.BuilderConfig directly (K3 ZERO-ALIASES dissolved charly/buildkit_aliases.go). The
// (phase, venue) phase-template resolvers moved to sdk/buildkit too (P8b — they are
// PURE over the CUE-sourced spec types: spec.Format, spec.Builder,
// Phase/Venue = spec enums); callers reference buildkit.FormatPhaseTemplate /
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
	Builder map[string]*spec.BuilderDef     `yaml:"builder" json:"builder"`
	Init    map[string]*ResolvedInit        `yaml:"init" json:"init"`
}

// LoadBuildConfigForBox loads distro, builder, and init configs for the
// project at dir. Post-unified-cutover this reads from charly.yml (via
// LoadUnified) rather than following a format_config: pointer.
//
// The init section is optional: projects without an `inits:` block return a
// nil *buildkit.InitConfig (no init system, no entrypoint beyond the base image default).
func LoadBuildConfigForBox(dir string) (*spec.DistroConfig, *spec.BuilderConfig, *buildkit.InitConfig, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, nil, nil, noCharlyYmlErr(dir)
	}
	// The build-vocab projections live in loaderkit (K3 Unit 1 — the ONE home charly core and
	// candy/plugin-build both call, R3). charly supplies its in-proc registry OpResolve callbacks
	// for the opaque distro/init bodies; the builder bodies decode purely (no callback).
	return loaderkit.ProjectDistroConfig(uf, resolveDistroViaPlugin),
		loaderkit.ProjectBuilderConfig(uf),
		loaderkit.ProjectInitConfig(uf, resolveInitConfigViaPlugin), nil
}

// LoadDefaultBuildConfig is retained as an alias for the single-argument form.
func LoadDefaultBuildConfig(dir string) (*spec.DistroConfig, *spec.BuilderConfig, *buildkit.InitConfig, error) {
	return LoadBuildConfigForBox(dir)
}
