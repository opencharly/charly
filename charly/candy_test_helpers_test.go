package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
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

// testConstructStepExecutor returns the SAME in-proc reverse-channel executor
// the invokeOpCompile helper (bundle_compile_parity_test.go) threads onto the ctx it hands
// command:bundle's OpCompile (K5-A item 1, compile-seam ctx-threading): a test
// exercising deploykit.BuildDeployPlan/CompileOpSteps directly (in-process, package
// main) needs a REAL executor reaching the provider registry for any `run: plugin:
// <word>` op, exactly as the real compile path does — the "construct-step" HostBuild
// seam has no other way to be reached from a plain function call.
func testConstructStepExecutor() (context.Context, *specexec.Executor) {
	ex := specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}})
	return specexec.ContextWithExecutor(context.Background(), ex), ex
}

// testCompileOpSteps drives deploykit.CompileOpSteps with a fresh
// testConstructStepExecutor + testResolvedBox() — the fixed shape every
// compile-timeline test in this package used before K5-A item 1 threaded ctx/exec
// through CompileOpSteps. t.Fatalf's on error so callers don't have to.
func testCompileOpSteps(t *testing.T, layer spec.CandyReader) []spec.InstallStep {
	t.Helper()
	ctx, ex := testConstructStepExecutor()
	steps, err := deploykit.CompileOpSteps(ctx, ex, layer, testResolvedBox())
	if err != nil {
		t.Fatalf("CompileOpSteps: %v", err)
	}
	return steps
}

// testCompileServiceSteps (the deploykit.CompileServiceSteps-driving twin of testCompileOpSteps
// above) relocated to candy/plugin-bundle (#55 decoupling, Batch A) with its own copy — its
// last charly-side consumer, service_distro_filter_test.go, moved there too.
