package fleet

import (
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// install_build_test.go — relocated from charly/install_build_test.go (#55 decoupling, Batch A):
// every test here asserts deploykit.BuildDeployPlan / the install-compile helpers directly, zero
// charly coupling.

// TestBuildDeployPlan_BuilderPurity_NoPluginRPC is the externalization purity gate (operator
// requirement): BuildDeployPlan is a PURE function of its inputs and NEVER dials a builder plugin.
// The externalized detection-builder's stage context + teardown ops are resolved out-of-process in
// the host-side build PRE-PASS and threaded in via HostContext.BuilderContext; the compiler only
// READS that pre-populated map. This test connects NO plugin (a nil executor — the candy fixture
// carries no `plugin:` verb step), so if the compiler tried to RPC a builder it would fail/skip —
// instead it must succeed and faithfully reflect the supplied data, AND succeed with base-only
// context when none is supplied.
func TestBuildDeployPlan_BuilderPurity_NoPluginRPC(t *testing.T) {
	img := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "purity", Home: "/home/u"}, BuilderConfig: &spec.BuilderConfig{Builder: map[string]*buildkit.BuilderDef{
		"pixi": {DetectFiles: []string{"pixi.toml"}},
	}}}
	layer := pixiCandy(t, "c")
	ctx, ex := testConstructStepExecutor()

	// (a) Pre-resolved by the (simulated) pre-pass: the compiler must read it verbatim — no RPC.
	wantRev := []spec.ReverseOp{{Kind: spec.ReverseOpPixiEnvRemove, Targets: []string{"myenv"}, Scope: spec.ScopeUser, Extra: map[string]string{"layer": "c"}}}
	pre := deploykit.HostContext{BuilderContext: map[string]deploykit.BuilderPreresolved{
		deploykit.BuilderCtxKey("c", "pixi"): {Context: map[string]any{"env_name": "myenv"}, Reverse: wantRev},
	}}
	plan, err := deploykit.BuildDeployPlan(ctx, ex, layer, img, pre)
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
	if len(bs.Reverse()) != 1 || bs.Reverse()[0].Kind != spec.ReverseOpPixiEnvRemove {
		t.Fatalf("Reverse() = %+v, want the pre-resolved [pixi-env-remove]", bs.Reverse())
	}

	// (b) No pre-pass (HostContext{}): the compiler still succeeds with base-only context + nil
	// teardown — it never dials a plugin (none is connected here), proving purity.
	plan2, err := deploykit.BuildDeployPlan(ctx, ex, layer, img, deploykit.HostContext{})
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

func firstBuilderStep(t *testing.T, plan *spec.InstallPlan) *spec.BuilderStep {
	t.Helper()
	for _, s := range plan.Steps {
		if bs, ok := s.(*spec.BuilderStep); ok {
			return bs
		}
	}
	t.Fatalf("no BuilderStep in plan: %s", deploykit.DescribePlan(plan))
	return nil
}

// TestBuildDeployPlanRipgrep/DevTools/PixiCandy are integration-ish tests for BuildDeployPlan
// against this repo's OWN real candy definitions (ripgrep / dev-tools / pre-commit) — relocated
// from charly/install_build_test.go's loadCompilerFixtures, which drove the full charly project
// loader (LoadConfig/ScanAllCandyWithConfig/resolveBoxTest against "fedora-coder") to source these
// fixtures. That whole-project loader is charly-core territory this out-of-module plugin package
// cannot reach standalone, so the fixture is sourced instead via loadRealCandy (reads the real
// candy/<name>/charly.yml directly, pure loaderkit.ScanInlineCandy — no project load, no
// registry) against a synthetic fedora-rpm image (fedoraCoderLikeImg) standing in for the real
// project's resolved "fedora-coder" box. The layer content (packages/tasks/builders) is real; only
// the consuming image's exact inheritance chain is synthetic.

func TestBuildDeployPlanRipgrep(t *testing.T) {
	ripgrep := loadRealCandy(t, "ripgrep")
	img := fedoraCoderLikeImg()

	ctx, ex := testConstructStepExecutor()
	plan, err := deploykit.BuildDeployPlan(ctx, ex, ripgrep, img, deploykit.HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	if plan.Candy != "ripgrep" {
		t.Errorf("plan.Candy = %q, want ripgrep", plan.Candy)
	}

	// ripgrep is a pure rpm: package candy — expect exactly one
	// SystemPackagesStep at PhaseInstall with the ripgrep package.
	var pkgSteps []*spec.SystemPackagesStep
	for _, s := range plan.Steps {
		if sp, ok := s.(*spec.SystemPackagesStep); ok {
			pkgSteps = append(pkgSteps, sp)
		}
	}
	if len(pkgSteps) != 1 {
		t.Fatalf("expected 1 SystemPackagesStep, got %d; full plan: %s",
			len(pkgSteps), deploykit.DescribePlan(plan))
	}
	if pkgSteps[0].Format != "rpm" {
		t.Errorf("pkg format = %q, want rpm", pkgSteps[0].Format)
	}
	found := slices.Contains(pkgSteps[0].Packages, "ripgrep")
	if !found {
		t.Errorf("ripgrep package not in step packages: %v", pkgSteps[0].Packages)
	}

	// Install-phase pkg step must be ungated.
	if got := pkgSteps[0].RequiresGate(); got != spec.GateNone {
		t.Errorf("install phase gate = %v, want none", got)
	}

	// Reverse op should uninstall ripgrep.
	ops := pkgSteps[0].Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpPackageRemove {
		t.Errorf("Reverse ops = %+v, want [package-remove]", ops)
	}
}

func TestBuildDeployPlanDevTools(t *testing.T) {
	dt := loadRealCandy(t, "dev-tools")
	img := fedoraCoderLikeImg()

	ctx, ex := testConstructStepExecutor()
	plan, err := deploykit.BuildDeployPlan(ctx, ex, dt, img, deploykit.HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	// dev-tools has rpm: packages + a cmd: task.
	var pkgCount, taskCount int
	for _, s := range plan.Steps {
		switch s.(type) {
		case *spec.SystemPackagesStep:
			pkgCount++
		case *spec.OpStep:
			taskCount++
		}
	}
	if pkgCount < 1 {
		t.Errorf("expected ≥1 SystemPackagesStep, got %d; plan: %s",
			pkgCount, deploykit.DescribePlan(plan))
	}
	if taskCount < 1 {
		t.Errorf("expected ≥1 OpStep, got %d; plan: %s",
			taskCount, deploykit.DescribePlan(plan))
	}
}

func TestBuildDeployPlanPixiCandy(t *testing.T) {
	pc := loadRealCandy(t, "pre-commit")
	if !pc.HasFile("pixi.toml") {
		t.Skip("pre-commit doesn't have pixi.toml (fixture changed)")
	}
	img := fedoraCoderLikeImg()

	ctx, ex := testConstructStepExecutor()
	plan, err := deploykit.BuildDeployPlan(ctx, ex, pc, img, deploykit.HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	var builders []*spec.BuilderStep
	for _, s := range plan.Steps {
		if bs, ok := s.(*spec.BuilderStep); ok {
			builders = append(builders, bs)
		}
	}
	if len(builders) == 0 {
		t.Fatalf("expected a BuilderStep for pixi, got none; plan: %s",
			deploykit.DescribePlan(plan))
	}
	foundPixi := false
	for _, b := range builders {
		if b.Builder == "pixi" {
			foundPixi = true
			if b.Venue() != spec.VenueContainerBuilder {
				t.Errorf("pixi builder venue = %v, want container-builder", b.Venue())
			}
			if b.Scope() != spec.ScopeUser {
				t.Errorf("pixi builder scope = %v, want user", b.Scope())
			}
		}
	}
	if !foundPixi {
		t.Errorf("no pixi BuilderStep in plan; plan: %s", deploykit.DescribePlan(plan))
	}
}

func TestComputeDeployIDDeterminism(t *testing.T) {
	a := deploykit.ComputeDeployID("fedora-coder", []string{"ripgrep", "uv"}, nil)
	b := deploykit.ComputeDeployID("fedora-coder", []string{"ripgrep", "uv"}, nil)
	if a != b {
		t.Errorf("deploy ID not deterministic: %s vs %s", a, b)
	}
	// Reordering candies changes the ID (candy order matters for reproducibility).
	c := deploykit.ComputeDeployID("fedora-coder", []string{"uv", "ripgrep"}, nil)
	if a == c {
		t.Errorf("expected different IDs for different candy orders, both got %s", a)
	}
	// Adding an overlay changes the ID.
	d := deploykit.ComputeDeployID("fedora-coder", []string{"ripgrep", "uv"}, []string{"my-extras"})
	if a == d {
		t.Errorf("expected different IDs with add_candies, both got %s", a)
	}
	if len(a) != 16 {
		t.Errorf("deploy ID length = %d, want 16 (first 16 hex chars of sha256)", len(a))
	}
}

func TestMergePlansOrderingAndID(t *testing.T) {
	p1 := &spec.InstallPlan{Candy: "ripgrep", Distro: "fedora:43", Steps: []spec.InstallStep{
		&spec.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall, Packages: []string{"ripgrep"}},
	}}
	p2 := &spec.InstallPlan{Candy: "uv", Distro: "fedora:43", Steps: []spec.InstallStep{
		&spec.OpStep{CandyName: "uv", Op: &spec.Op{Download: "https://…"}},
	}}

	merged := deploykit.MergePlan([]*spec.InstallPlan{p1, p2}, "fedora-coder", nil)
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
		if got := deploykit.EnsureServiceSuffix(in); got != want {
			t.Errorf("ensureServiceSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribePlanSummary(t *testing.T) {
	p := &spec.InstallPlan{
		Candy:  "x",
		Box:    "y",
		Distro: "z",
		Steps: []spec.InstallStep{
			&spec.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall},
			&spec.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall},
			&spec.OpStep{Op: &spec.Op{Mkdir: "/x"}},
		},
	}
	out := deploykit.DescribePlan(p)
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
// loaderkit's derivePackageSections writes + NewInstallContext reads), as a
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
	step := deploykit.BuildSystemPackagesStep("deb", spec.PhaseInstall, []string{"tailscale"}, raw, nil)
	if len(step.Repos) != 1 {
		t.Fatalf("step.Repos len = %d, want 1 (repo-key/type mismatch left it empty)", len(step.Repos))
	}
	if step.Repos[0].Raw["name"] != "tailscale" {
		t.Errorf("repo name = %v, want tailscale", step.Repos[0].Raw["name"])
	}
}
