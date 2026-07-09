package main

import "path/filepath"

// candy_model_accessors.go — field accessors on the runtime Candy so it satisfies
// deploykit.CandyModel, the read-only interface the deploy-plan compiler
// (BuildDeployPlan, moved to sdk/deploykit in P4) reads a candy through. The
// compiler depends on the abstraction, so the Candy struct + its tests STAY in
// charly (boundary law: kernel depends on an interface, the concrete kind
// implements it). Exported-field accessors use a Get* name to avoid colliding with
// the same-named struct field.

func (l *Candy) GetName() string         { return l.Name }
func (l *Candy) GetSourceDir() string    { return l.SourceDir }
func (l *Candy) GetVersion() string      { return l.Version }
func (l *Candy) Vars() map[string]string { return l.vars }
func (l *Candy) PlanSteps() []Step       { return l.plan }
func (l *Candy) Reboot() bool            { return l.reboot }

// HasFile reports whether the candy ships a detect file (pixi.toml/etc.) or an
// arbitrary file under its source dir — the builder-detection probe.
func (l *Candy) HasFile(filename string) bool {
	switch filename {
	case "pixi.toml":
		return l.HasPixiToml
	case "pyproject.toml":
		return l.HasPyprojectToml
	case "environment.yml":
		return l.HasEnvironmentYml
	case "package.json":
		return l.HasPackageJson
	case "Cargo.toml":
		return l.HasCargoToml
	default:
		return fileExists(filepath.Join(l.SourceDir, filename))
	}
}
