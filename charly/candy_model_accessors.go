package main

import (
	"path/filepath"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// candy_model_accessors.go — field accessors on the runtime Candy so it satisfies
// deploykit.CandyModel, the read-only interface EVERY consumer (the deploy-plan
// compiler, the build-render engine in sdk/deploykit + candy/plugin-build, the
// graph_shim/intermediates_shim pure helpers) reads a candy through — none of
// them need the concrete *Candy type, only this interface (K3 confirmed this
// empirically: CandyModel is already fully data-shaped and consumed plugin-side
// today). What keeps the CONCRETE Candy struct + its construction in charly is
// NOT "the compiler depends on an interface" (an interface never pins its
// implementer's location) — it's that ScanCandy/scanCandy/parseCandyYAML (the
// construction path) call the loader (LoadUnified/requireLoaderParser/buildCandy),
// a genuine core Mechanism. Exported-field accessors use a Get* name to avoid
// colliding with the same-named struct field.

func (l *Candy) GetName() string         { return l.Name }
func (l *Candy) GetSourceDir() string    { return l.SourceDir }
func (l *Candy) GetVersion() string      { return l.Version }
func (l *Candy) Vars() map[string]string { return l.vars }
func (l *Candy) PlanSteps() []spec.Step  { return l.plan }
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
		return kit.FileExists(filepath.Join(l.SourceDir, filename))
	}
}
