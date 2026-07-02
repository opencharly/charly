package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestBuildDeployPlan_BuilderPurity_NoPluginRPC is the externalization purity gate (operator
// requirement): BuildDeployPlan is a PURE function of its inputs and NEVER dials a builder plugin.
// The externalized detection-builder's stage context + teardown ops are resolved out-of-process in
// the host-side build PRE-PASS and threaded in via HostContext.BuilderContext; the compiler only
// READS that pre-populated map. This test connects NO plugin, so if the compiler tried to RPC a
// builder it would fail/skip — instead it must succeed and faithfully reflect the supplied data,
// AND succeed with base-only context when none is supplied. The reverse-op derivation that moved
// out-of-process is covered by plugin/kit/builder_test.go.
func TestBuildDeployPlan_BuilderPurity_NoPluginRPC(t *testing.T) {
	img := &ResolvedBox{
		Name: "purity",
		Home: "/home/u",
		BuilderConfig: &BuilderConfig{Builder: map[string]*BuilderDef{
			"pixi": {DetectFiles: []string{"pixi.toml"}},
		}},
	}
	layer := &Candy{Name: "c", HasPixiToml: true}

	// (a) Pre-resolved by the (simulated) pre-pass: the compiler must read it verbatim — no RPC.
	wantRev := []ReverseOp{{Kind: ReverseOpPixiEnvRemove, Targets: []string{"myenv"}, Scope: ScopeUser, Extra: map[string]string{"layer": "c"}}}
	pre := HostContext{BuilderContext: map[string]builderPreresolved{
		builderCtxKey("c", "pixi"): {Context: map[string]any{"env_name": "myenv"}, Reverse: wantRev},
	}}
	plan, err := BuildDeployPlan(layer, img, pre)
	if err != nil {
		t.Fatalf("BuildDeployPlan (pre-resolved): %v", err)
	}
	bs := firstBuilderStep(t, plan)
	if got := bs.RawStageContext["env_name"]; got != "myenv" {
		t.Fatalf("RawStageContext[env_name] = %v, want the pre-resolved %q (compiler must read pre-pass data, not RPC)", got, "myenv")
	}
	if bs.RawStageContext["builder"] != "pixi" || bs.RawStageContext["layer"] != "c" {
		t.Fatalf("base context lost: %+v", bs.RawStageContext)
	}
	if len(bs.Reverse()) != 1 || bs.Reverse()[0].Kind != ReverseOpPixiEnvRemove {
		t.Fatalf("Reverse() = %+v, want the pre-resolved [pixi-env-remove]", bs.Reverse())
	}

	// (b) No pre-pass (HostContext{}): the compiler still succeeds with base-only context + nil
	// teardown — it never dials a plugin (none is connected here), proving purity.
	plan2, err := BuildDeployPlan(layer, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan (no pre-pass): %v", err)
	}
	bs2 := firstBuilderStep(t, plan2)
	if _, present := bs2.RawStageContext["env_name"]; present {
		t.Fatalf("env_name present without a pre-pass = %+v (the compiler must not derive/RPC it)", bs2.RawStageContext)
	}
	if bs2.Reverse() != nil {
		t.Fatalf("Reverse() without a pre-pass = %+v, want nil", bs2.Reverse())
	}
}

func firstBuilderStep(t *testing.T, plan *InstallPlan) *BuilderStep {
	t.Helper()
	for _, s := range plan.Steps {
		if bs, ok := s.(*BuilderStep); ok {
			return bs
		}
	}
	t.Fatalf("no BuilderStep in plan: %s", DescribePlan(plan))
	return nil
}

// Integration-ish tests for BuildDeployPlan using the project's own
// candy definitions. Not unit tests in the strict sense (they read
// real YAML via LoadConfig + ScanAllCandyWithConfig) but they catch
// compile-time regressions that pure unit tests can't.

// compilerTestProjectDir chdirs to the project root (the parent of charly/)
// and returns a cleanup callback. The compiler tests rely on being able
// to LoadConfig from charly.yml, which only exists in the project root.
func compilerTestProjectDir(t *testing.T) (string, func()) { //nolint:unparam // test helper returns (dir, cleanup); dir kept for symmetry
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up from current to find the project root (charly.yml marker).
	dir := prev
	for range 5 {
		if _, err := os.Stat(filepath.Join(dir, UnifiedFileName)); err == nil {
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir %s: %v", dir, err)
			}
			return dir, func() { _ = os.Chdir(prev) }
		}
		dir = filepath.Dir(dir)
	}
	t.Skipf("project root not found walking up from %s; skipping", prev)
	return "", func() {}
}

// loadCompilerFixtures loads charly.yml + candies from the project and
// resolves the "fedora-coder" image. Returns nil, nil if fixtures can't
// load (used to gracefully skip in CI environments that might not have
// the fixture candies present).
func loadCompilerFixtures(t *testing.T, boxName string) (*Config, *ResolvedBox, map[string]*Candy) {
	t.Helper()
	dir, _ := os.Getwd()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// RegisterBuildVocabulary must run before candy scanning so format sections
	// (rpm:/deb:/pac:) are recognized. Post-unified-cutover LoadDefaultBuildConfig
	// reads charly.yml directly.
	{
		_ = cfg
		distroCfg, _, _, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			t.Fatalf("LoadDefaultBuildConfig: %v", err)
		}
		RegisterBuildVocabulary(distroCfg)
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		t.Fatalf("ScanAllCandyWithConfig: %v", err)
	}
	img, err := cfg.ResolveBox(boxName, "testing", dir, ResolveOpts{})
	if err != nil {
		t.Skipf("ResolveBox(%s): %v (fixture missing?)", boxName, err)
	}
	return cfg, img, layers
}

func TestBuildDeployPlanRipgrep(t *testing.T) {
	_, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	_, img, layers := loadCompilerFixtures(t, "fedora-coder")
	ripgrep, ok := layers["ripgrep"]
	if !ok {
		t.Skip("ripgrep layer not present in fixtures")
	}

	plan, err := BuildDeployPlan(ripgrep, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	if plan.Candy != "ripgrep" {
		t.Errorf("plan.Candy = %q, want ripgrep", plan.Candy)
	}

	// ripgrep is a pure rpm: package candy — expect exactly one
	// SystemPackagesStep at PhaseInstall with the ripgrep package.
	var pkgSteps []*SystemPackagesStep
	for _, s := range plan.Steps {
		if sp, ok := s.(*SystemPackagesStep); ok {
			pkgSteps = append(pkgSteps, sp)
		}
	}
	if len(pkgSteps) != 1 {
		t.Fatalf("expected 1 SystemPackagesStep, got %d; full plan: %s",
			len(pkgSteps), DescribePlan(plan))
	}
	if pkgSteps[0].Format != "rpm" {
		t.Errorf("pkg format = %q, want rpm", pkgSteps[0].Format)
	}
	found := slices.Contains(pkgSteps[0].Packages, "ripgrep")
	if !found {
		t.Errorf("ripgrep package not in step packages: %v", pkgSteps[0].Packages)
	}

	// Install-phase pkg step must be ungated.
	if got := pkgSteps[0].RequiresGate(); got != GateNone {
		t.Errorf("install phase gate = %v, want none", got)
	}

	// Reverse op should uninstall ripgrep.
	ops := pkgSteps[0].Reverse()
	if len(ops) != 1 || ops[0].Kind != ReverseOpPackageRemove {
		t.Errorf("Reverse ops = %+v, want [package-remove]", ops)
	}
}

func TestBuildDeployPlanDevTools(t *testing.T) {
	_, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	_, img, layers := loadCompilerFixtures(t, "fedora-coder")
	dt, ok := layers["dev-tools"]
	if !ok {
		t.Skip("dev-tools layer not present in fixtures")
	}

	plan, err := BuildDeployPlan(dt, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	// dev-tools has rpm: packages + a cmd: task.
	var pkgCount, taskCount int
	for _, s := range plan.Steps {
		switch s.(type) {
		case *SystemPackagesStep:
			pkgCount++
		case *OpStep:
			taskCount++
		}
	}
	if pkgCount < 1 {
		t.Errorf("expected ≥1 SystemPackagesStep, got %d; plan: %s",
			pkgCount, DescribePlan(plan))
	}
	if taskCount < 1 {
		t.Errorf("expected ≥1 OpStep, got %d; plan: %s",
			taskCount, DescribePlan(plan))
	}
}

func TestBuildDeployPlanPixiCandy(t *testing.T) {
	_, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	_, img, layers := loadCompilerFixtures(t, "fedora-coder")
	// pre-commit candy uses pixi builder (has pixi.toml).
	pc, ok := layers["pre-commit"]
	if !ok {
		t.Skip("pre-commit layer not present in fixtures")
	}
	if !pc.HasPixiToml {
		t.Skip("pre-commit doesn't have pixi.toml (fixture changed)")
	}

	plan, err := BuildDeployPlan(pc, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	var builders []*BuilderStep
	for _, s := range plan.Steps {
		if bs, ok := s.(*BuilderStep); ok {
			builders = append(builders, bs)
		}
	}
	if len(builders) == 0 {
		t.Fatalf("expected a BuilderStep for pixi, got none; plan: %s",
			DescribePlan(plan))
	}
	foundPixi := false
	for _, b := range builders {
		if b.Builder == "pixi" {
			foundPixi = true
			if b.Venue() != VenueContainerBuilder {
				t.Errorf("pixi builder venue = %v, want container-builder", b.Venue())
			}
			if b.Scope() != ScopeUser {
				t.Errorf("pixi builder scope = %v, want user", b.Scope())
			}
		}
	}
	if !foundPixi {
		t.Errorf("no pixi BuilderStep in plan; plan: %s", DescribePlan(plan))
	}
}

func TestComputeDeployIDDeterminism(t *testing.T) {
	a := computeDeployID("fedora-coder", []string{"ripgrep", "uv"}, nil)
	b := computeDeployID("fedora-coder", []string{"ripgrep", "uv"}, nil)
	if a != b {
		t.Errorf("deploy ID not deterministic: %s vs %s", a, b)
	}
	// Reordering candies changes the ID (candy order matters for reproducibility).
	c := computeDeployID("fedora-coder", []string{"uv", "ripgrep"}, nil)
	if a == c {
		t.Errorf("expected different IDs for different candy orders, both got %s", a)
	}
	// Adding an overlay changes the ID.
	d := computeDeployID("fedora-coder", []string{"ripgrep", "uv"}, []string{"my-extras"})
	if a == d {
		t.Errorf("expected different IDs with add_candies, both got %s", a)
	}
	if len(a) != 16 {
		t.Errorf("deploy ID length = %d, want 16 (first 16 hex chars of sha256)", len(a))
	}
}

func TestMergePlansOrderingAndID(t *testing.T) {
	p1 := &InstallPlan{Candy: "ripgrep", Distro: "fedora:43", Steps: []InstallStep{
		&SystemPackagesStep{Format: "rpm", Phase: PhaseInstall, Packages: []string{"ripgrep"}},
	}}
	p2 := &InstallPlan{Candy: "uv", Distro: "fedora:43", Steps: []InstallStep{
		&OpStep{CandyName: "uv", Op: &Op{Download: "https://…"}},
	}}

	merged := MergePlan([]*InstallPlan{p1, p2}, "fedora-coder", nil)
	if merged.Box != "fedora-coder" {
		t.Errorf("merged.Box = %q, want fedora-coder", merged.Box)
	}
	if len(merged.Steps) != 2 {
		t.Errorf("merged.Steps len = %d, want 2", len(merged.Steps))
	}
	if merged.CandiesIncluded[0] != "ripgrep" || merged.CandiesIncluded[1] != "uv" {
		t.Errorf("candy order wrong: %v", merged.CandiesIncluded)
	}
	if merged.DeployID == "" {
		t.Errorf("merged DeployID is empty")
	}
}

func TestEnsureServiceSuffix(t *testing.T) {
	tests := map[string]string{
		"postgresql":         "postgresql.service",
		"postgresql.service": "postgresql.service",
		"foo.timer":          "foo.timer",
		"foo.socket":         "foo.socket",
		"":                   "",
	}
	for in, want := range tests {
		if got := ensureServiceSuffix(in); got != want {
			t.Errorf("ensureServiceSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribePlanSummary(t *testing.T) {
	p := &InstallPlan{
		Candy:  "x",
		Box:    "y",
		Distro: "z",
		Steps: []InstallStep{
			&SystemPackagesStep{Format: "rpm", Phase: PhaseInstall},
			&SystemPackagesStep{Format: "rpm", Phase: PhaseInstall},
			&OpStep{Op: &Op{Mkdir: "/x"}},
		},
	}
	out := DescribePlan(p)
	if !strings.Contains(out, "candy=x") {
		t.Errorf("missing candy name in description: %s", out)
	}
	if !strings.Contains(out, "SystemPackages: 2") {
		t.Errorf("missing SystemPackages count: %s", out)
	}
	if !strings.Contains(out, "Op: 1") {
		t.Errorf("missing Op count: %s", out)
	}
}

// TestBuildSystemPackagesStepRepos guards the repo-key fix in
// buildSystemPackagesStep: repos are stored under the canonical "repo" key (what
// derivePackageSectionsFromCalamares writes + NewInstallContext reads), as a
// []map[string]any value. The prior code read raw["repos"] (plural) with a
// []interface{} assertion, so step.Repos was ALWAYS empty and the PhasePrepare
// repo-gate (SystemPackagesStep.RequiresGate) never saw a candy's repos.
func TestBuildSystemPackagesStepRepos(t *testing.T) {
	raw := map[string]any{
		"package": []string{"tailscale"},
		"repo": []map[string]any{{
			"name": "tailscale",
			"url":  "https://pkgs.tailscale.com/stable/debian",
		}},
	}
	step := buildSystemPackagesStep("deb", PhaseInstall, []string{"tailscale"}, raw, nil)
	if len(step.Repos) != 1 {
		t.Fatalf("step.Repos len = %d, want 1 (repo-key/type mismatch left it empty)", len(step.Repos))
	}
	if step.Repos[0].Raw["name"] != "tailscale" {
		t.Errorf("repo name = %v, want tailscale", step.Repos[0].Raw["name"])
	}
}
