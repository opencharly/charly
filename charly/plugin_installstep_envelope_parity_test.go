package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// plugin_installstep_envelope_parity_test.go — the mandatory R10 parity gate for K5-Unit-6b
// (candy/plugin-installstep moving its 4 former HOST-COUPLED step-emit words —
// system-packages/builder/local-pkg-install/op — off the per-render HostBuild("step-emit") round
// trip onto its OWN "resolved-project"-envelope-built deploykit.Generator, mirroring
// candy/plugin-build + candy/plugin-deploy-pod). It proves the ENVELOPE-DRIVEN Generator
// (deploykit.NewRenderGeneratorFromProject — the EXACT constructor the plugin now calls) renders
// EmitTasks / BuildStageContext BYTE-IDENTICAL to the box-build's LIVE-CORE Generator
// (gen.toDeploykit() — the Generator the real `charly box build` path renders through), across
// several real box shapes plus a candy carrying a `run: plugin:` verb step.
//
// Adapted from the throwaway K3-envelope RDD spike (spike/k3-envelope branch, never merged,
// archived at scratchpad/k3-envelope-spike-harness.go.txt) with its ONE documented gap closed: the
// spike explicitly SKIPPED every candy carrying a `plugin:` verb step because EmitPluginOp — the
// ONE render seam that stays genuinely host-coupled (a Go-level ProvisionActor type-assertion only
// charly core can perform) — needs a LIVE executor/registry the spike never wired for the
// envelope-driven side. This test wires a REAL in-proc executor (the SAME
// sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{build: …}}) mechanism
// production uses) for the envelope-driven Generator too, so a plugin:-verb candy compares.

// parityFixture names one real box + a human label for the report.
type parityFixture struct {
	dir   string
	box   string
	shape string
}

func TestPluginInstallstepEnvelopeParity(t *testing.T) {
	// Resolving a real box/fedora box connects every plugin candy transitively reachable in the
	// project's FULL discovered candy graph (not just the requested box), registering them into
	// the process-global providerRegistry — a side effect other tests in this package assume is
	// clean (e.g. TestExternalPluginStep_ReverseChannelEndToEnd registers its own examplestep
	// provider and errors on a pre-existing duplicate). Snapshot + restore around this whole test.
	t.Cleanup(snapshotProviderState())

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot = filepath.Dir(repoRoot) // charly/ -> repo root

	fixtures := make([]parityFixture, 0, 5)
	fixtures = append(fixtures,
		parityFixture{dir: filepath.Join(repoRoot, "box", "fedora"), box: "fedora", shape: "candy-only base distro image"},
		parityFixture{dir: filepath.Join(repoRoot, "box", "fedora"), box: "fedora-builder", shape: "builder-based image (pixi/npm/aur/cargo detection builders)"},
		parityFixture{dir: filepath.Join(repoRoot, "box", "fedora"), box: "jupyter", shape: "builder-based image w/ a real pixi.toml candy (multi-stage BuildStageContext exercise)"},
		parityFixture{dir: filepath.Join(repoRoot, "box", "fedora"), box: "nvidia", shape: "GPU/label-heavy image"},
	)
	// The 5th fixture is a LOCAL, hermetic project (no @github remote fetch — zero network
	// dependency) whose one candy authors a `run: plugin:` verb step (unix_group, the SAME
	// act-emit shape check-stack-layer uses in box/fedora's check-pod bed) — the exact case the
	// spike's own skip comment named as out of scope.
	fixtures = append(fixtures, parityFixture{
		dir: writePluginVerbParityFixture(t), box: "pluginverb-fixture",
		shape: "a candy authoring a run: plugin: verb step (unix_group act-emit enabler)",
	})

	totals := parityTotals{}
	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.box, func(t *testing.T) { runParityFixture(t, fx, &totals) })
	}

	// No silent 0-comparison pass (a false pass proves nothing — R4/testing-validator standard).
	if totals.candyCompares == 0 {
		t.Fatal("ZERO EmitTasks comparisons ran across all fixtures — parity gate is a false pass")
	}
	if totals.pluginVerbCompares == 0 {
		t.Fatal("ZERO plugin:-verb candy comparisons ran — the exact gap the throwaway spike left is still uncovered")
	}
	fmt.Printf("PLUGIN-INSTALLSTEP ENVELOPE PARITY TOTALS: %d EmitTasks comparisons (%d carrying a plugin: verb), %d BuildStageContext comparisons, across %d fixtures\n",
		totals.candyCompares, totals.pluginVerbCompares, totals.builderCompares, len(fixtures))
}

// parityTotals accumulates comparison counts across every fixture subtest, so the outer test can
// assert non-vacuous coverage (R4/testing-validator: a 0-comparison pass is a false pass).
type parityTotals struct {
	candyCompares      int
	pluginVerbCompares int
	builderCompares    int
}

// runParityFixture resolves ONE fixture box through BOTH the live-core Generator
// (gen.toDeploykit()) and the envelope-driven Generator (deploykit.NewRenderGeneratorFromProject —
// the exact constructor candy/plugin-installstep now calls) and asserts EmitTasks/
// BuildStageContext/BakedMetadata render byte-identically between them, accumulating comparison
// counts into totals.
func runParityFixture(t *testing.T, fx parityFixture, totals *parityTotals) {
	t.Helper()
	// --- live-core path: NewGenerator + render-prep, the SAME recipe hostBuildBuildResolve itself
	// runs (called in-process directly to avoid standing up the reverse-channel broker). ---
	gen, err := NewGenerator(fx.dir, "parity", boxResolveOpts([]string{fx.box}, false))
	if err != nil {
		t.Fatalf("NewGenerator(%s): %v", fx.box, err)
	}
	if err := renderPrepAll(gen); err != nil {
		t.Fatalf("renderPrepAll: %v", err)
	}
	img := gen.Boxes[fx.box]
	if img == nil {
		t.Fatalf("box %q not found after resolve", fx.box)
	}
	gen.resolveUserContext(img)

	// --- project the resolved-project envelope exactly as hostBuildBuildResolve does ---
	lp, err := loadProjectForResolve(fx.dir, boxResolveOpts([]string{fx.box}, false), nil)
	if err != nil {
		t.Fatalf("loadProjectForResolve: %v", err)
	}
	if lp.empty {
		t.Fatalf("loadProjectForResolve returned empty project for %s", fx.dir)
	}
	rp, err := projectResolvedProjectWithBoxes(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, gen.InitConfig, fx.dir, lp.version, boxResolveOpts([]string{fx.box}, false), nil, gen.Boxes)
	if err != nil {
		t.Fatalf("projectResolvedProjectWithBoxes: %v", err)
	}
	rp.GlobalOrder = gen.GlobalOrder
	rp.ExternalizedBuilders = externalizedBuilders

	// --- cross the "wire": marshal + unmarshal the envelope (simulates the process boundary a
	// plugin actually crosses it over) ---
	raw, err := json.Marshal(rp)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	t.Logf("box=%s envelope wire size=%d bytes, boxes=%d candy_models=%d candies=%d",
		fx.box, len(raw), len(rp.Boxes), len(rp.CandyModels), len(rp.Candies))
	var rp2 spec.ResolvedProject
	if err := json.Unmarshal(raw, &rp2); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	// --- wire a REAL in-proc executor for the envelope-driven Generator's EmitPluginOp, so a
	// plugin:-verb candy compares too (the gap the throwaway spike left). Populate renderGenCache
	// the way hostBuildBuildResolve does (the render-seam's EmitPluginOp/inline-builder handlers
	// look the Generator up there by dir). ---
	renderGenCache.Store(fx.dir, gen)
	t.Cleanup(func() { renderGenCache.Delete(fx.dir) })
	build := buildEngineContext{Generator: gen, Box: img}
	ex := sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{build: build}})
	ctx := sdk.ContextWithExecutor(context.Background(), ex)

	// --- build the envelope-driven deploykit.Generator (the EXACT production constructor
	// candy/plugin-installstep / candy/plugin-build / candy/plugin-deploy-pod already use) ---
	dg2, err := deploykit.NewRenderGeneratorFromProject(ctx, ex, &rp2, fx.dir, false)
	if err != nil {
		t.Fatalf("NewRenderGeneratorFromProject: %v", err)
	}
	img2 := dg2.Boxes[fx.box]
	if img2 == nil {
		t.Fatalf("envelope-round-tripped box %q missing from dg2.Boxes", fx.box)
	}

	// live-core deploykit.Generator (toDeploykit(), the SAME object the box-build path already
	// renders through today).
	dg1 := gen.toDeploykit()

	candyNames := make([]string, 0, len(img.Candy))
	candyNames = append(candyNames, img.Candy...)
	sort.Strings(candyNames)

	compareEmitTasks(t, dg1, dg2, img, img2, gen, candyNames, totals)
	compareBuildStageContext(t, dg1, dg2, img, img2, gen, lp, rp2, candyNames, totals)

	// --- label-set comparison: BakedMetadata is the exact wire form WriteLabels reads. Proves
	// render-prep's own output survives the envelope round-trip byte-identically. ---
	bm1, _ := json.Marshal(img.BakedMetadata)
	bm2, _ := json.Marshal(img2.BakedMetadata)
	if string(bm1) != string(bm2) {
		t.Errorf("box %q: BakedMetadata DIFFERS after envelope round-trip\n--- live-core ---\n%s\n--- envelope ---\n%s", fx.box, bm1, bm2)
	} else {
		t.Logf("box %q: BakedMetadata byte-identical (%d bytes)", fx.box, len(bm1))
	}
}

// compareEmitTasks renders every candy with tasks through BOTH generators and asserts
// byte-identical Containerfile fragments, including candies carrying a `plugin:` verb step.
func compareEmitTasks(t *testing.T, dg1, dg2 *deploykit.Generator, img, img2 *buildkit.ResolvedBox, gen *Generator, candyNames []string, totals *parityTotals) {
	t.Helper()
	for _, name := range candyNames {
		layer1 := gen.Candies[name]
		if layer1 == nil || !layer1.HasTasks() {
			continue
		}
		layer2 := dg2.Candies[name]
		if layer2 == nil {
			t.Errorf("candy %q present in live Candies but missing from envelope-round-tripped dg2.Candies", name)
			continue
		}

		ops := layer1.RunOps()
		hasPluginVerb := false
		for _, op := range ops {
			if op.Plugin != "" {
				hasPluginVerb = true
				break
			}
		}

		var b1, b2 strings.Builder
		if _, err := dg1.EmitTasks(&b1, layer1, img, ops, gen.BuildDir, ""); err != nil {
			t.Fatalf("candy %q: live-core EmitTasks: %v", name, err)
		}
		if _, err := dg2.EmitTasks(&b2, layer2, img2, ops, dg2.BuildDir, ""); err != nil {
			t.Fatalf("candy %q: envelope EmitTasks: %v", name, err)
		}
		totals.candyCompares++
		if hasPluginVerb {
			totals.pluginVerbCompares++
		}
		if b1.String() != b2.String() {
			t.Errorf("candy %q: EmitTasks output DIFFERS between live-core and envelope path\n--- live-core ---\n%s\n--- envelope ---\n%s", name, b1.String(), b2.String())
		} else {
			t.Logf("candy %q: EmitTasks byte-identical (%d bytes, plugin-verb=%v)", name, b1.Len(), hasPluginVerb)
		}
	}
}

// compareBuildStageContext renders the builder-stage render context for every candy that DETECTS
// a multi-stage builder (mirroring the plugin's own bDef.DetectFiles/layer.HasFile detection in
// emitBuilder) through BOTH generators and asserts byte-identical JSON.
func compareBuildStageContext(t *testing.T, dg1, dg2 *deploykit.Generator, img, img2 *buildkit.ResolvedBox, gen *Generator, lp *loadedProject, rp2 spec.ResolvedProject, candyNames []string, totals *parityTotals) {
	t.Helper()
	for bName, bDef1 := range lp.builderCfg.Builder {
		if bDef1 == nil || len(bDef1.DetectFiles) == 0 {
			continue
		}
		bDef2 := rp2.Builder[bName]
		if bDef2 == nil {
			continue
		}
		for _, name := range candyNames {
			layer1 := gen.Candies[name]
			if layer1 == nil {
				continue
			}
			detected := false
			for _, f := range bDef1.DetectFiles {
				if layer1.HasFile(f) {
					detected = true
					break
				}
			}
			if !detected {
				continue
			}
			layer2 := dg2.Candies[name]
			if layer2 == nil {
				t.Errorf("builder candy %q missing from envelope dg2.Candies", name)
				continue
			}
			ref1 := img.Builder[bName]
			ctx1 := dg1.BuildStageContext(layer1, bName, bDef1, img, ref1)
			ctx2 := dg2.BuildStageContext(layer2, bName, bDef2, img2, ref1)
			j1, _ := json.Marshal(ctx1)
			j2, _ := json.Marshal(ctx2)
			totals.builderCompares++
			if string(j1) != string(j2) {
				t.Errorf("candy %q builder %q: BuildStageContext DIFFERS\n--- live-core ---\n%s\n--- envelope ---\n%s", name, bName, j1, j2)
			} else {
				t.Logf("candy %q builder %q: BuildStageContext byte-identical (ref=%q)", name, bName, ref1)
			}
		}
	}
}

// writePluginVerbParityFixture writes a minimal, hermetic (no @github remote fetch) project to a
// fresh temp dir: one image composing one candy whose plan: authors a `run: plugin:` verb step
// (unix_group — the SAME act-emit shape candy/check-stack-layer uses in box/fedora's check-pod
// bed), so the parity gate exercises a plugin:-verb candy without any network dependency.
func writePluginVerbParityFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := `version: ` + spec.SchemaVersion + `
discover:
    - path: candy
      recursive: true
pluginverb-fixture:
    candy:
        base: quay.io/fedora/fedora-minimal:43
        description: envelope-parity fixture image composing a plugin:-verb candy
        distro:
            - fedora
        build:
            - rpm
        candy:
            - mytool
        plan:
            - check: the fixture image is buildable
              command:
                command: "true"
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	candyDir := filepath.Join(dir, "candy", "mytool")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candy := `mytool:
    candy:
        version: 2026.201.0000
        description: "envelope-parity fixture candy authoring a plugin-verb step"
        plan:
            - check: the fixture candy is present
              command:
                command: "true"
            - run: create the checkgrp group at image build via the unix_group plugin verb
              id: pluginverb-fixture-unix-group-act
              context:
                - build
              run_as: root
              unix_group:
                unix_group: checkgrp
                gid: 4242
`
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candy), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
