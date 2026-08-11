package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// compilerTestProjectDir chdirs to the project root (the repo root that owns candy/) and returns
// a cleanup callback. Relocated here from the deleted charly/install_build_test.go (#55
// decoupling, Batch A) — this test is its last remaining consumer (per the ambiguous-item ruling
// 3: TestFleetCompileParity_PluginRoundTrip's "OLD" side and invokeOpCompile's "NEW" side both
// need charly-internal registry/dispatch machinery unreachable from an out-of-module plugin
// package, so this file STAYS in charly rather than moving).
//
// The marker is the `candy/` directory (the repo root owns it; the charly/ package dir does NOT).
// Walking up for the `charly.yml` FILENAME hits the tracked `charly/charly.yml` embedded providers
// manifest FIRST (it shadows the repo-root charly.yml), so ResolveBox("fedora-coder") then fails
// and this test would vacuously SKIP — the root-cause of the prior vacuous-skip runs. `candy/`
// disambiguates: only the repo root has it.
func compilerTestProjectDir(t *testing.T) (string, func()) { //nolint:unparam // test helper returns (dir, cleanup); dir kept for symmetry
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := prev
	for range 6 {
		if info, err := os.Stat(filepath.Join(dir, "candy")); err == nil && info.IsDir() {
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir %s: %v", dir, err)
			}
			return dir, func() { _ = os.Chdir(prev) }
		}
		dir = filepath.Dir(dir)
	}
	t.Skipf("project root (candy/) not found walking up from %s; skipping", prev)
	return "", func() {}
}

// invokeOpCompile drives command:fleet's KEPT OpCompile leg over an in-proc reverse channel — the
// SAME shared compilePlansForRequest candy/plugin-fleet's walk.go dispatchOne calls IN-PROC (K4-C
// shape-2). It replaces the deleted host deployAddCmd.compileViaPlugin (fleet_compile_seam.go) as
// this parity test's plugin-compile entry point, byte-for-byte the same Invoke(OpCompile) mechanism.
func invokeOpCompile(t *testing.T, req spec.DeployCompileRequest) ([]*spec.InstallPlan, error) {
	t.Helper()
	prov, ok := providerRegistry.resolve(ClassCommand, "fleet")
	if !ok {
		t.Fatalf("invokeOpCompile: command:fleet provider not loaded (candy/plugin-fleet must be compiled in)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ctx := specexec.ContextWithExecutor(context.Background(),
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ctx, &Operation{Reserved: "fleet", Op: ops.OpCompile, Params: reqJSON})
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.JSON) == 0 {
		t.Fatalf("invokeOpCompile: OpCompile returned no reply")
	}
	var reply spec.DeployCompileReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return nil, err
	}
	var views []spec.InstallPlanView
	if err := json.Unmarshal(reply.PlansJSON, &views); err != nil {
		return nil, err
	}
	plans := make([]*spec.InstallPlan, 0, len(views))
	for _, v := range views {
		p, err := spec.PlanFromView(v)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// isolateProviderRegistry snapshots the global providerRegistry and restores it on cleanup, so the
// external plugin connections + byKey entries THIS test creates (via LoadUnified(rootDir) →
// connectDeclaredKindPlugins + the deploy-substrate connect for the root's kubernetes entities) do not leak
// to later tests.
//
// The leak's mechanism (R1 root cause): a later test (credential_await_unlock_external_test.go) calls
// providerRegistry.Close() in its own t.Cleanup, which closes EVERY plugin connection (closers) but
// does NOT clear byKey. A kind/deploy provider THIS test registered therefore stays in byKey —
// already CLOSED — and the next connectDeclaredKindPlugins SKIPS its re-connect (ResolveKind returns
// true on the stale byKey entry), leaving later tests with a dead "grpc: the client connection is
// closing" connection. The same retention makes ResolveDeploy("kubernetes") stay true, which flips
// isExternalDeploySubstrate("kubernetes") true and silences validateCheckBeds's "unsupported target"
// rejection (TestValidateCheckBeds_TargetEnum). In the clean tree no test alphabetically before
// check_bed_run_test.go calls LoadUnified(rootDir), so the leak is latent — this test ('b') is the
// first to populate the registry from the root project, surfacing it.
//
// Removing the NEW byKey/origins entries + closing the NEW closers restores the registry to its
// pre-test state, so later tests re-connect fresh. The registry has no public Unregister; a
// test-local snapshot+restore in the SAME package is the standard isolation pattern for global
// mutable state with no per-test reset (not an R4 workaround — it is the cleanup, applied at the
// exact mutation site). Compiled-in providers registered at init() are in the snapshot and stay.
//
// The SAME leak hits the additive prescan globals (declaredDeploySubstrate/declaredKind/…): the
// root project's candy/ contains candy/plugin-kube (declaring deploy:kubernetes) + candy/plugin-example-kind
// (declaring kind:examplekind), so LoadUnified(rootDir)'s byte-gated prescan stamps
// declaredDeploySubstrate["kubernetes"]=true, which flips isExternalDeploySubstrate("kubernetes") true and silences
// validateCheckBeds's "unsupported target" rejection (TestValidateCheckBeds_TargetEnum). They are
// process-wide + additive by design, so the snapshot+restore removes the keys THIS test added.
func isolateProviderRegistry(t *testing.T) {
	t.Helper()
	providerRegistry.mu.Lock()
	snapKeys := maps.Clone(providerRegistry.byKey)
	snapOrigins := maps.Clone(providerRegistry.origins)
	snapClosers := len(providerRegistry.closers)
	providerRegistry.mu.Unlock()
	declaredDeployMu.Lock()
	snapDeploySub := maps.Clone(declaredDeploySubstrate)
	snapKind := maps.Clone(declaredKind)
	snapExtVerb := maps.Clone(declaredExternalVerb)
	snapExtStep := maps.Clone(declaredExternalStep)
	snapExtCmd := maps.Clone(declaredExternalCommand)
	declaredDeployMu.Unlock()
	t.Cleanup(func() {
		providerRegistry.mu.Lock()
		for k := range providerRegistry.byKey {
			if _, keep := snapKeys[k]; !keep {
				delete(providerRegistry.byKey, k)
			}
		}
		for k := range providerRegistry.origins {
			if _, keep := snapOrigins[k]; !keep {
				delete(providerRegistry.origins, k)
			}
		}
		newClosers := providerRegistry.closers[snapClosers:]
		providerRegistry.closers = providerRegistry.closers[:snapClosers:cap(providerRegistry.closers)]
		providerRegistry.mu.Unlock()
		for _, c := range newClosers {
			_ = c.Close()
		}
		declaredDeployMu.Lock()
		for k := range declaredDeploySubstrate {
			if _, keep := snapDeploySub[k]; !keep {
				delete(declaredDeploySubstrate, k)
			}
		}
		for k := range declaredKind {
			if _, keep := snapKind[k]; !keep {
				delete(declaredKind, k)
			}
		}
		for k := range declaredExternalVerb {
			if _, keep := snapExtVerb[k]; !keep {
				delete(declaredExternalVerb, k)
			}
		}
		for k := range declaredExternalStep {
			if _, keep := snapExtStep[k]; !keep {
				delete(declaredExternalStep, k)
			}
		}
		for k := range declaredExternalCommand {
			if _, keep := snapExtCmd[k]; !keep {
				delete(declaredExternalCommand, k)
			}
		}
		declaredDeployMu.Unlock()
	})
}

// fleet_compile_parity_test.go — the K4-B compile-parity golden. Proves the deploy COMPILE slice
// moved out of charly/ core into candy/plugin-fleet (the command:fleet plugin's OpCompile leg)
// is byte-faithful to the former in-proc host compile, OVER the FULL plugin seam: the host computes
// the per-node selection (a hand-built ResolvedBoxView + the candy order + HostContext), Invokes
// the fleet plugin's OpCompile, the plugin re-hydrates the resolved-project envelope via
// InvokeProvider("build","project") + loops deploykit.BuildDeployPlan + projects []InstallPlanView,
// and the host re-materializes []*InstallPlan via spec.PlanFromView.
//
// THE GOLDEN (#55 K3 cone 1 redesign): the OLD side used to call deploykit.BuildDeployPlan
// directly, in-process — an sdk mechanism-kit dependency the import-purity gate forbids in
// charly/ (this file was its last holder; the value-type leg had already dropped its
// buildkit import). BuildDeployPlan is a pure function (candy/plugin-fleet's own
// TestBuildDeployPlan_BuilderPurity_NoPluginRPC proves it never dials a plugin itself) computed
// from data that is ITSELF deterministic and reproducible offline: the fedora/rpm distro
// vocabulary is a documented PURE field-copy of the checked-in charly/charly.yml (candy/
// plugin-distro's ops.OpResolve, see its resolve.go), and the pixi builder's deploy-time context/
// reverse ops are thin dispatches to the PUBLIC, pure sdk/kit.BuilderCollectContext/BuilderReverse
// (candy/plugin-builder-pixi/plugin.go). tools/golden-compile (its own standalone module, mirroring
// the tools/gomod-canonical precedent) computes this OLD-side ground truth offline and writes it to
// the checked-in charly/testdata/fleet_compile_parity_golden.json — this file now loads that golden
// via plain encoding/json instead of computing it live, so it needs no sdk import at all. The NEW
// side (invokeOpCompile) is UNCHANGED: it was always charly-internal registry/dispatch machinery,
// never an sdk import.
//
// Regenerate the golden with `go run ./tools/golden-compile` (from the repo root) whenever the
// compiler (sdk/deploykit's BuildDeployPlan or its sub-compilers) or one of the three fixture
// candies (candy/ripgrep, candy/dev-tools, candy/pre-commit) changes in a way that alters its
// compiled InstallPlan — a stale golden fails this test loudly (a wire-form diff), never silently.
//
// For each fixture candy (across 3 classes — pkg/op/builder) the golden asserts BOTH:
//  (1) WireView parity: the golden's frozen OLD wire view byte-equals spec.WireView(newPlan) — the
//      plugin-compiled + re-materialized plan projects to the SAME wire form as the former live
//      host-compile (the spike's byte-identity check).
//  (2) PlanFromView fidelity: spec.PlanFromView(spec.WireView(newPlan)) DeepEqual newPlan —
//      the WireView→PlanFromView round-trip is the identity on a re-materialized plan, proving the
//      re-materialization step the host now performs loses nothing.
//
// The can-fail RIDER (subtest) perturbs the box Home and asserts the plans DIFFER — so the parity
// comparison is not vacuously passing on a constant (a silently-empty or perturbation-insensitive
// fixture would FAIL the rider). Non-vacuity is also guarded directly: ≥3 candies AND ≥2 step
// classes (pkg/op/builder) must appear in the plans.

func TestFleetCompileParity_PluginRoundTrip(t *testing.T) {
	isolateProviderRegistry(t)
	dir, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		t.Fatalf("ScanAllCandyWithConfig: %v", err)
	}

	golden := loadCompileParityGolden(t, dir)

	// The SAME hand-built fedora ResolvedBoxView the OLD-side golden generator
	// (tools/golden-compile) constructs its ResolvedBox from — kept in field lockstep with it
	// (mirrors the K4-B RDD spike).
	boxView := spec.ResolvedBoxView{
		Name: "k4b-parity", EffectiveVersion: "2026.001.0001", Base: "quay.io/fedora/fedora:43",
		IsExternalBase: true, UID: 1000, GID: 1000, User: "user", Home: "/home/user",
		UserAdopted: true, Distro: []string{"fedora:43", "fedora"}, BuildFormats: []string{"rpm"}, Pkg: "rpm",
	}

	candidates := []string{"ripgrep", "dev-tools", "pre-commit"}
	var exercised []string
	classes := map[string]bool{}
	oldJSON := map[string]string{}
	for _, name := range candidates {
		layer, ok := layers[name]
		if !ok {
			t.Logf("fixture %q not present in layers; skipping", name)
			continue
		}
		oldView, ok := golden[name]
		if !ok {
			t.Fatalf("golden fixture missing candy %q — regenerate with `go run ./tools/golden-compile`", name)
		}

		// NEW: the SHARED in-proc compiler (compilePlansForRequest), reached via the KEPT OpCompile
		// leg (invokeOpCompile) — the EXACT SAME function candy/plugin-fleet's walk.go dispatchOne
		// calls IN-PROC (K4-C shape-2). Empty HostContextJSON matches production reality post-Unit-8:
		// the host no longer pre-populates BuilderContext at all — command:fleet's
		// compileDeployPlans always recomputes it itself over its own exec.InvokeProvider pre-pass,
		// regardless of what (if anything) rides the wire.
		emptyHostCtxJSON, err := json.Marshal(spec.HostContext{})
		if err != nil {
			t.Fatalf("marshal empty host context: %v", err)
		}
		plans, err := invokeOpCompile(t, spec.DeployCompileRequest{
			Dir:             dir,
			BoxView:         boxView,
			Order:           []string{name}, // single-candy compile, matching the golden's single-candy BuildDeployPlan
			HostContextJSON: emptyHostCtxJSON,
		})
		if err != nil {
			t.Fatalf("NEW invokeOpCompile(%s): %v", name, err)
		}
		if len(plans) != 1 {
			t.Fatalf("NEW %s: expected 1 plan, got %d (%v)", name, len(plans), planCandyNames(plans))
		}
		newPlan := plans[0]

		// (1) WireView parity — the plugin-compiled plan projects to the SAME wire form the frozen
		// golden captured. The wire form (JSON) is what crosses the plugin boundary, so
		// byte-identity against the golden is the honest parity test.
		newView := spec.WireView(newPlan)
		ob, _ := json.Marshal(oldView)
		nb, _ := json.Marshal(newView)
		if string(ob) != string(nb) {
			t.Fatalf("PARITY BREAK on %q (WireView wire form differs from the golden — regenerate with `go run ./tools/golden-compile` if this is an intentional compiler change):\n--- GOLDEN ---\n%s\n--- NEW ---\n%s", name, ob, nb)
		}

		// (2) PlanFromView fidelity — WireView→PlanFromView is the identity on the re-materialized plan.
		reread, err := spec.PlanFromView(newView)
		if err != nil {
			t.Fatalf("PlanFromView(%s): %v", name, err)
		}
		if !reflect.DeepEqual(reread, newPlan) {
			t.Fatalf("PlanFromView fidelity break on %q: re-materialized plan differs from plugin plan", name)
		}

		exercised = append(exercised, name)
		oldJSON[name] = string(mustMarshalJSON(t, newView))
		// Class tracking (mirror the spike's detection).
		if layer.HasFile("pixi.toml") || layer.GetHasPackageJson() || layer.GetHasCargoToml() {
			classes["builder"] = true
		} else if len(layer.TopPackages()) > 0 || layer.HasFormatPackages() {
			classes["pkg"] = true
		}
		if layer.HasTasks() {
			classes["op"] = true
		}
	}

	// Non-vacuity guards.
	if len(exercised) < 3 {
		t.Fatalf("low-fixture-diversity: only %d candies exercised (%v) — need ≥3 of %v", len(exercised), exercised, candidates)
	}
	if len(classes) < 2 {
		t.Fatalf("low-fixture-diversity: only %d step classes (%v) — need ≥2 of pkg/op/builder", len(classes), classes)
	}
	t.Logf("PARITY OK: %d candies, %d classes (%v) — plugin OpCompile round-trip byte-faithful for the deploy compile", len(exercised), len(classes), classes)

	// can-fail RIDER: a perturbed envelope (a different Home) MUST produce a different plan for any
	// home-anchored candy — so the parity comparison is not vacuously passing on a constant. The
	// pixi builder step (pre-commit) is home-anchored (cargo/pixi install into $HOME).
	t.Run("can_fail", func(t *testing.T) {
		perturbed := boxView // value copy — spec.ResolvedBoxView is a plain struct
		perturbed.Home = "/home/OTHER"
		emptyHostCtxJSON, err := json.Marshal(spec.HostContext{})
		if err != nil {
			t.Fatalf("marshal empty host context: %v", err)
		}
		var broke bool
		for _, name := range exercised {
			plans, err := invokeOpCompile(t, spec.DeployCompileRequest{
				Dir:             dir,
				BoxView:         perturbed,
				Order:           []string{name},
				HostContextJSON: emptyHostCtxJSON,
			})
			if err != nil {
				t.Fatalf("perturbed invokeOpCompile(%s): %v", name, err)
			}
			if len(plans) != 1 {
				t.Fatalf("perturbed %s: expected 1 plan, got %d", name, len(plans))
			}
			nv := spec.WireView(plans[0])
			nb := string(mustMarshalJSON(t, nv))
			if nb != oldJSON[name] {
				broke = true
			}
		}
		if !broke {
			t.Fatal("can-fail RIDER: a perturbed Home produced byte-identical plans for ALL candies — the parity comparison is vacuous / not sensitive to the envelope")
		}
		t.Logf("can-fail RIDER OK: perturbed Home changed ≥1 plan — parity comparison is sensitive to the envelope")
	})
}

func planCandyNames(plans []*spec.InstallPlan) []string {
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.Candy)
	}
	return out
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// loadCompileParityGolden reads the checked-in OLD-side ground truth
// (charly/testdata/fleet_compile_parity_golden.json, keyed by candy name) that
// tools/golden-compile computes offline — see this file's top doc comment. dir is the repo root
// compilerTestProjectDir resolved (the marker candy/ directory's parent).
func loadCompileParityGolden(t *testing.T, dir string) map[string]spec.InstallPlanView {
	t.Helper()
	path := filepath.Join(dir, "charly", "testdata", "fleet_compile_parity_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v (regenerate with `go run ./tools/golden-compile`)", path, err)
	}
	// The generator normalizes repo-root-absolute paths (candy_dir/ctx_path) to a ${REPO_ROOT}
	// token so the golden is worktree-independent; substitute THIS tree's resolved root back in
	// (the paired replace in tools/golden-compile's main).
	data = bytes.ReplaceAll(data, []byte("${REPO_ROOT}"), []byte(dir))
	var golden map[string]spec.InstallPlanView
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("decode golden fixture %s: %v", path, err)
	}
	return golden
}
