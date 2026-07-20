package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// candy_test_helpers_test.go — the shared spec.CandyReader test-fixture constructor (W9). Every
// test in this package that used to build a candy fixture as a literal *Candy{...} now builds a
// (spec.CandyModel, spec.CandyView) pair instead — the SAME split the real scan pipeline produces
// (CandyModel = the build-plan/package/env half, CandyView = the identity/graph half) — and wraps
// it via testCandy, matching production's deploykit.NewSpecCandyModel(m, v) call exactly.

// testCandy wraps a CandyModel + CandyView into a spec.CandyReader fixture, stamping name onto
// BOTH views (GetName()/GetSourceDir() read CandyModel; identity/graph fields like Remote/RepoPath
// read CandyView, so both need the same Name for a fixture that behaves consistently regardless of
// which accessor a test path happens to call).
func testCandy(name string, m spec.CandyModel, v spec.CandyView) spec.CandyReader {
	m.Name = name
	v.Name = name
	return deploykit.NewSpecCandyModel(m, v)
}

// pixiCandy builds a spec.CandyReader fixture that owns a REAL pixi.toml at a fresh t.TempDir(),
// so the specCandyAdapter's live fs-probe HasFile("pixi.toml") reports it — mirroring production's
// *Candy.HasFile() semantics the old map[string]*Candy{..., HasPixiToml: true} fixtures exercised.
// Identical precedent: sdk/deploykit/intermediates_move_test.go's pixiCandy helper (W3/#36) — that
// sibling keeps CandyModel/CandyView params since its callers vary them; every charly-side call
// site only needs the pixi.toml probe + a name, so this one drops both (no callsite ever varies
// them — widen back to a (m, v) signature if a future test needs to).
func pixiCandy(t *testing.T, name string) spec.CandyReader {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pixi.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write pixi.toml: %v", err)
	}
	return testCandy(name, spec.CandyModel{SourceDir: dir}, spec.CandyView{})
}
