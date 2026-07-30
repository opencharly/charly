package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/spec/spec"
)

var updateResolvedProjectGolden = flag.Bool("update-resolved-project-golden", false,
	"regenerate the resolved-project golden testdata")

// canonKey folds a JSON key to its case/underscore-insensitive form so a snake_case #ResolvedBoxView
// key (base / build_formats / bootstrap_builder_image) maps 1:1 to the corresponding json.Marshal
// key of buildkit.ResolvedBox (Base / BuildFormats / BootstrapBuilderImage). This is why the
// completeness assertion below can compare the two field sets without a per-field name table.
func canonKey(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// fullResolvedBoxFixture returns a ResolvedBox with EVERY non-json:"-" field set to a distinct
// non-zero value, plus the InitSystem json:"-" cache set — so the completeness test proves (a) every
// field `charly box inspect` serializes survives the projection and (b) the host-only compute caches
// are DROPPED (InitSystem is the flagged judgment call: it is json:"-", so inspect never emits it).
func fullResolvedBoxFixture() *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "demo", Version: "2026.100.0001", EffectiveVersion: "2026.100.0002", Status: "working", Info: "a demo box", CheckLevel: "noagent", Base: "fedora:43", From: "builder:pacstrap", BootstrapBuilderImage: "ghcr.io/opencharly/builder", Platforms: []string{"linux/amd64"}, Tag: "2026.100.0003", Registry: "ghcr.io/opencharly", Pkg: "rpm", Distro: []string{"fedora:43", "fedora"}, BuildFormats: []string{"rpm"}, Tags: []string{"all", "fedora"}, Candy: []string{"base", "charly"}, User: "user", UID: 1000, GID: 1000, Home: "/home/user", UserAdopted: true, Merge: &vmshared.MergeConfig{Auto: true, MaxMB: 512, MaxTotalMB: 4096}, Builder: spec.BuilderMap{"pixi": "ghcr.io/opencharly/pixi"}, BuilderCapabilities: []string{"pixi"}, Auto: true, Network: "host", DataImage: true, IsExternalBase: true, FullTag: "ghcr.io/opencharly/demo:2026.100.0003"}, // Host-only json:"-" compute cache (must NOT leak into the wire view):
		InitSystem: "supervisord"}
}

// TestProjectResolvedBox_CompleteAndNoCacheLeak proves the two design invariants of the box view:
// COMPLETENESS (every field `charly box inspect` serializes — json.Marshal(*ResolvedBox) — survives
// projectResolvedBox with an equal value; a dropped/renamed field FAILS here) and NO CACHE LEAK (none
// of the 6 host-only json:"-" compute caches, InitSystem among them, appears in the wire view).
func TestProjectResolvedBox_CompleteAndNoCacheLeak(t *testing.T) {
	box := fullResolvedBoxFixture()
	view := deploykit.ProjectResolvedBox(box)

	boxJSON, err := json.Marshal(box)
	if err != nil {
		t.Fatalf("marshal ResolvedBox: %v", err)
	}
	viewJSON, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal ResolvedBoxView: %v", err)
	}
	var boxMapVar, viewMap map[string]json.RawMessage
	if err := json.Unmarshal(boxJSON, &boxMapVar); err != nil {
		t.Fatalf("unmarshal ResolvedBox: %v", err)
	}
	if err := json.Unmarshal(viewJSON, &viewMap); err != nil {
		t.Fatalf("unmarshal ResolvedBoxView: %v", err)
	}

	viewCanon := make(map[string]json.RawMessage, len(viewMap))
	for k, v := range viewMap {
		viewCanon[canonKey(k)] = v
	}

	// Completeness: box inspect's serialized field set ⊆ the projection, value-for-value.
	for k, bv := range boxMapVar {
		ck := canonKey(k)
		vv, ok := viewCanon[ck]
		if !ok {
			t.Fatalf("ResolvedBox field %q (canon %q) is DROPPED by projectResolvedBox — inspect exposes it", k, ck)
		}
		if !bytes.Equal(bv, vv) {
			t.Fatalf("field %q value differs: inspect=%s view=%s", k, bv, vv)
		}
	}

	// No host-only compute cache leaks into the wire view. The 3 RESOLVE-time vocab pointers
	// (DistroConfig/DistroDef/BuilderConfig) STAY host-only — the plugin render re-attaches them
	// from the project vocab (NewSpecResolvedBox), so they must never cross the wire. The
	// build-RENDER caches (BakedMetadata/Caps/RenderCandyOrder/InitSystem/InitDef/ActiveInits)
	// ARE wire data now (#67 render-DRIVE move — the plugin render reads them from the envelope
	// WITHOUT the live *Candy graph), so they are asserted in the positive set below.
	for _, cache := range []string{"distroconfig", "distrodef", "builderconfig"} {
		if _, leaked := viewCanon[cache]; leaked {
			t.Fatalf("host-only vocab pointer %q leaked into ResolvedBoxView (must stay json:%q, never wire data)", cache, "-")
		}
	}
}

// fixedResolvedProjectFixture assembles a deterministic spec.ResolvedProject from the box + a fully
// populated candy (via projectCandyView, exercising every #CandyView projection arm) + a deploy tree
// node — no time-dependent inputs, so its marshaling is a stable golden.
func fixedResolvedProjectFixture(t *testing.T) *spec.ResolvedProject {
	t.Helper()
	// projectCandyView/projectCandyModel (which took the live *Candy) are gone (W9): the
	// resolved-project host now gets the (Model, View) pair straight from the wrapped
	// spec.CandyReader via the RawCandy() escape hatch (rawCandyPair) — no projection step
	// left to exercise, so the fixture constructs the View directly with the SAME field
	// values the old projection used to derive from the live *Candy's accessors.
	candy := testCandy("charly",
		spec.CandyModel{Version: "2026.100.0004"},
		spec.CandyView{
			Version:       "2026.100.0004",
			Description:   "the charly toolchain",
			Status:        "working",
			Info:          "the charly toolchain",
			Remote:        true,
			RepoPath:      "github.com/opencharly/charly",
			Require:       []string{"base"},
			IncludedCandy: []string{"gnupg"},
			EnvProvides:   map[string]string{"CHARLY_HOME": "/opt/charly"},
			MCPProvide:    []spec.MCPServerYAML{{Name: "charly-mcp", URL: "http://localhost:9000", Transport: "http"}},
			Ports:         []int64{9000},
			ServiceNames:  []string{"charly-daemon"},
		},
	)
	_, candyView, ok := deploykit.RawCandyPair(candy)
	if !ok {
		t.Fatal("rawCandyPair: candy fixture does not expose RawCandy()")
	}

	rp := &spec.ResolvedProject{
		Version: "2026.100.0000",
		Boxes:   map[string]spec.ResolvedBoxView{"demo": deploykit.ProjectResolvedBox(fullResolvedBoxFixture())},
		Candies: map[string]spec.CandyView{"charly": candyView},
	}
	bundle := map[string]spec.BundleNode{"demo-pod": {Target: "pod", Description: "demo deploy"}}
	for k, v := range bundle {
		node := v
		if rp.Deploy == nil {
			rp.Deploy = make(map[string]*spec.Deploy, len(bundle))
		}
		rp.Deploy[k] = &node
	}
	return rp
}

// Per-init-trigger completion (formerly TestProjectCandyViewPreservesPerInitTriggers /
// TestResolvedProjectCompletesPerInitTriggersBeforeProjection, which exercised the pre-W9
// resolve-time projectCandyView/*Candy path) is proven at the new SCAN-time choke point by
// TestScanAllCandyWithConfigOpts_LocalCandyGetsInitSystemsCompletion (layers_test.go) — there is
// no later separate loaderkit.PopulateCandyInitSystem(map[string]*Candy, ...) call left to exercise here.

// TestResolvedProject_ByteStableGolden proves the assembled spec.ResolvedProject is deterministic
// (two marshals identical) and byte-stable against the committed golden. A dropped field, a reordered
// struct, or a changed projection all FAIL here. Regenerate with -update-resolved-project-golden.
func TestResolvedProject_ByteStableGolden(t *testing.T) {
	rp := fixedResolvedProjectFixture(t)

	got, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		t.Fatalf("marshal ResolvedProject: %v", err)
	}
	got2, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		t.Fatalf("marshal ResolvedProject (2nd): %v", err)
	}
	if !bytes.Equal(got, got2) {
		t.Fatalf("ResolvedProject marshaling is not deterministic:\n1st: %s\n2nd: %s", got, got2)
	}

	golden := filepath.Join("testdata", "resolved_project_golden.json")
	if *updateResolvedProjectGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-resolved-project-golden to create it): %v", err)
	}
	if !bytes.Equal(bytes.TrimRight(want, "\n"), got) {
		t.Fatalf("golden mismatch (run -update-resolved-project-golden if intended):\n got:\n%s\nwant:\n%s", got, want)
	}
}

// writeResolvedProjectFixtureProject writes a minimal unified project (charly.yml + one discovered
// candy) into a temp dir — the hermetic, box-free (no ResolveBox/vocab dependency) fixture the
// projection round-trip test resolves.
func writeResolvedProjectFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(
		"version: 2026.204.1223\n"+
			"discover:\n"+
			"    - path: candy\n"+
			"      recursive: true\n"), 0o644); err != nil {
		t.Fatalf("write charly.yml: %v", err)
	}
	candyDir := filepath.Join(dir, "candy", "rp-fixture")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatalf("mkdir candy: %v", err)
	}
	candy := "rp-fixture:\n" +
		"    candy:\n" +
		"        version: 2026.179.0000\n" +
		"        description: a fixture candy proving the resolved-project seam round-trips\n" +
		"        plan:\n" +
		"            - check: the true command runs\n" +
		"              id: rp-fixture-true\n" +
		"              context:\n" +
		"                  - build\n" +
		"              command: \"true\"\n"
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candy), 0o644); err != nil {
		t.Fatalf("write candy charly.yml: %v", err)
	}
	return dir
}

// testProjectResolvedProjectWithBoxes is the test-only reproduction of the deleted production
// projectResolvedProjectWithBoxes (#55 step3 3-II: its last production caller, the pod-overlay
// seam, now fetches its render-prepped envelope plugin-side instead via
// InvokeProvider("build","generate",OpResolve,…) — candy/plugin-build's own resolveBuildEngine runs
// the SAME loaderkit.ProjectResolvedProject call this test-side reproduction makes, wiring the
// SAME core-only ResolveProjectSeams closures (fillNamespacedBoxes/resolveResources/
// ComputeIntermediates/externalizedBuilders) a plugin-side caller cannot supply itself). Kept
// test-side because this exact "project a *spec.ResolvedProject from a live cfg/layers/uf/
// pre-resolved-boxes" shape has no OTHER core-resident caller anymore, and this file's + the
// parity test's coverage still needs it directly (not through a cross-module Invoke).
func testProjectResolvedProjectWithBoxes(cfg *Config, layers map[string]spec.CandyReader, uf *spec.UnifiedFile, distroCfg *spec.DistroConfig, builderCfg *spec.BuilderConfig, initCfg *buildkit.InitConfig, dir, version string, opts loaderkit.ResolveOpts, diags *spec.Diagnostics, preResolvedBoxes map[string]*buildkit.ResolvedBox) (*spec.ResolvedProject, error) {
	if opts.DistroCfg == nil {
		opts.DistroCfg = distroCfg
	}
	if opts.BuilderCfg == nil {
		opts.BuilderCfg = builderCfg
	}
	calver := ComputeCalVer()
	seams := loaderkit.ResolveProjectSeams{
		ResolveBox: func(cfg *spec.Config, name, calver, dir string) (*buildkit.ResolvedBox, error) {
			bkopts, oerr := testBkOpts(dir, opts)
			if oerr != nil {
				return nil, oerr
			}
			return buildkit.ResolveBox(cfg, name, calver, dir, bkopts)
		},
		FillNamespacedBoxes: func(nsUF *spec.UnifiedFile, ic *buildkit.InitConfig, prefix, calver, dir string, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
			fillNamespacedBoxes(nsUF, ic, prefix, calver, dir, opts, rp, visited)
		},
		ResolveResources:      resolveResources,
		ShouldIncludeDisabled: opts.ShouldIncludeDisabled,
		ComputeIntermediates:  testComputeIntermediates,
		ExternalizedBuilders:  externalizedBuilders,
	}
	rp, err := loaderkit.ProjectResolvedProject(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, calver, seams, diags, preResolvedBoxes)
	if rp != nil {
		rp.Primaries = loaderThreaded().Primaries
	}
	return rp, err
}

// testBkOpts reproduces the former core build-vocab resolve-opts projection (removed in #55 Cluster-B — charly
// core no longer names buildkit.ResolveOpts): fill the build vocabulary via resolveVocabOpts, then
// project onto buildkit.ResolveOpts (a test MAY import buildkit; only non-test charly may not).
func testBkOpts(dir string, opts loaderkit.ResolveOpts) (buildkit.ResolveOpts, error) {
	vopts, err := resolveVocabOpts(dir, opts)
	if err != nil {
		return buildkit.ResolveOpts{}, err
	}
	return buildkit.ResolveOpts{
		IncludeDisabled:      vopts.IncludeDisabled,
		IncludeDisabledNames: vopts.IncludeDisabledNames,
		RequestedBoxes:       vopts.RequestedBoxes,
		DistroCfg:            vopts.DistroCfg,
		BuilderCfg:           vopts.BuilderCfg,
	}, nil
}

// testComputeIntermediates reproduces the former core ComputeIntermediates shim (deleted in #55
// Cluster-B; its production form is candy/plugin-build/resolve_legs.go): lift cfg.Defaults into a
// deploykit.IntermediateDefaults and delegate to deploykit.ComputeIntermediates.
func testComputeIntermediates(boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader, cfg *spec.Config, tag string) (map[string]*buildkit.ResolvedBox, error) {
	defaults := deploykit.IntermediateDefaults{
		Builder:   spec.BuilderMap(cfg.Defaults.Builder),
		UID:       cfg.Defaults.UID,
		User:      cfg.Defaults.User,
		GID:       cfg.Defaults.GID,
		Merge:     cfg.Defaults.Merge,
		Registry:  cfg.Defaults.Registry,
		Platforms: cfg.Defaults.Platforms,
		Distro:    cfg.Defaults.Distro,
		Build:     cfg.Defaults.Build,
	}
	return deploykit.ComputeIntermediates(boxes, layers, defaults, tag)
}

// testBuildResolvedProject is the test-only reproduction of the deleted buildResolvedProjectFromDir
// (#55 step3 unit 3b moved its production form to candy/plugin-build's resolveProjectEnvelope): load
// the project fail-fast via loadProjectForResolve, short-circuit to an empty envelope for a
// project-less dir, else project it via testProjectResolvedProjectWithBoxes (preResolvedBoxes=nil
// for a fresh per-box resolve).
func testBuildResolvedProject(t *testing.T, dir string, opts loaderkit.ResolveOpts) (*spec.ResolvedProject, error) {
	t.Helper()
	lp, err := loadProjectForResolve(dir, opts, nil)
	if err != nil {
		return nil, err
	}
	if lp.empty {
		return &spec.ResolvedProject{}, nil
	}
	return testProjectResolvedProjectWithBoxes(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, lp.initCfg, dir, lp.version, opts, nil, nil)
}

// TestResolvedProject_Projection proves the SHARED projection path (loadProjectForResolve +
// loaderkit.ProjectResolvedProject, wired via testProjectResolvedProjectWithBoxes — the test-only
// reproduction of the deleted projectResolvedProjectWithBoxes, #55 step3 3-II) decodes
// candy/candy-model/vocab data faithfully — the wire contract candy/plugin-build's `build:project`
// word (the plugin-side envelope-fetch seam, #55 step3 unit 3b) and its ~8 consumers depend on. The
// seam ROUND TRIP itself (marshal → host dispatch → unmarshal, over InvokeProvider) is proven live
// by the R10 exploratory run against a real project (box inspect / status / check-project / bundle
// resolve), not re-created here as a second fixture — this test's job is the SHARED projection
// logic staying core-resident stays correct.
func TestResolvedProject_Projection(t *testing.T) {
	dir := writeResolvedProjectFixtureProject(t)

	rp, err := testBuildResolvedProject(t, dir, loaderkit.ResolveOpts{})
	if err != nil {
		t.Fatalf("testBuildResolvedProject: %v", err)
	}

	cv, ok := rp.Candies["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy missing from the projected ResolvedProject: %+v", rp.Candies)
	}
	if cv.Version != "2026.179.0000" || !strings.Contains(cv.Description, "resolved-project seam round-trips") {
		t.Fatalf("candy view decoded wrong over the seam: %+v", cv)
	}

	// Collection A growth (would be ABSENT pre-#54): the candy BUILD model is projected — the
	// check-projection / validate / K3-D enabler. The fixture candy declares one plan step.
	cm, ok := rp.CandyModels["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy MODEL missing from CandyModels: %+v", rp.CandyModels)
	}
	if len(cm.Plan) == 0 {
		t.Fatalf("candy model Plan not projected over the seam (the check-include/validate enabler): %+v", cm)
	}
	// build VOCABULARY (the validate ENGINE consumer) is projected from the embedded charly.yml.
	if len(rp.Distro) == 0 {
		t.Fatalf("build-vocab Distro not projected into the envelope (validate needs it)")
	}
	if len(rp.Builder) == 0 {
		t.Fatalf("build-vocab Builder not projected into the envelope (validate needs it)")
	}
}
