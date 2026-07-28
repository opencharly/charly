package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
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

// testConstructStepExecutor returns the SAME in-proc reverse-channel executor
// the invokeOpCompile helper (bundle_compile_parity_test.go) threads onto the ctx it hands
// command:bundle's OpCompile (K5-A item 1, compile-seam ctx-threading): a test
// exercising deploykit.BuildDeployPlan/CompileOpSteps directly (in-process, package
// main) needs a REAL executor reaching the provider registry for any `run: plugin:
// <word>` op, exactly as the real compile path does — the "construct-step" HostBuild
// seam has no other way to be reached from a plain function call.
func testConstructStepExecutor() (context.Context, *sdk.Executor) {
	ex := sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}})
	return sdk.ContextWithExecutor(context.Background(), ex), ex
}

// testCompileOpSteps drives deploykit.CompileOpSteps with a fresh
// testConstructStepExecutor + testResolvedBox() — the fixed shape every
// compile-timeline test in this package used before K5-A item 1 threaded ctx/exec
// through CompileOpSteps. t.Fatalf's on error so callers don't have to.
func testCompileOpSteps(t *testing.T, layer spec.CandyReader) []deploykit.InstallStep {
	t.Helper()
	ctx, ex := testConstructStepExecutor()
	steps, err := deploykit.CompileOpSteps(ctx, ex, layer, testResolvedBox())
	if err != nil {
		t.Fatalf("CompileOpSteps: %v", err)
	}
	return steps
}

// testCompileServiceSteps drives deploykit.CompileServiceSteps with a fresh
// testConstructStepExecutor — every service-compile test in this package used a bare
// 3-arg call before K5-A item 1 increment B threaded ctx/exec through
// CompileServiceSteps (needed for the rare systemd-custom-entry render leg, over the
// "render-service" seam; the packaged/supervisord paths never touch it).
// t.Fatalf's on error so callers don't have to.
func testCompileServiceSteps(t *testing.T, layer spec.CandyReader, img *buildkit.ResolvedBox, hostCtx deploykit.HostContext) []deploykit.InstallStep {
	t.Helper()
	ctx, ex := testConstructStepExecutor()
	steps, err := deploykit.CompileServiceSteps(ctx, ex, layer, img, hostCtx)
	if err != nil {
		t.Fatalf("CompileServiceSteps: %v", err)
	}
	return steps
}
