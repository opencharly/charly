package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// isolateProviderRegistry snapshots the global providerRegistry and restores it on cleanup, so the
// external plugin connections + byKey entries THIS test creates (via LoadUnified(rootDir) →
// connectDeclaredKindPlugins + the deploy-substrate connect for the root's k8s entities) do not leak
// to later tests.
//
// The leak's mechanism (R1 root cause): a later test (credential_await_unlock_external_test.go) calls
// providerRegistry.Close() in its own t.Cleanup, which closes EVERY plugin connection (closers) but
// does NOT clear byKey. A kind/deploy provider THIS test registered therefore stays in byKey —
// already CLOSED — and the next connectDeclaredKindPlugins SKIPS its re-connect (ResolveKind returns
// true on the stale byKey entry), leaving later tests with a dead "grpc: the client connection is
// closing" connection. The same retention makes ResolveDeploy("k8s") stay true, which flips
// isExternalDeploySubstrate("k8s") true and silences validateCheckBeds's "unsupported target"
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
// root project's candy/ contains candy/plugin-kube (declaring deploy:k8s) + candy/plugin-example-kind
// (declaring kind:examplekind), so LoadUnified(rootDir)'s byte-gated prescan stamps
// declaredDeploySubstrate["k8s"]=true, which flips isExternalDeploySubstrate("k8s") true and silences
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

// bundle_compile_parity_test.go — the K4-B compile-parity golden. Proves the deploy COMPILE slice
// moved out of charly/ core into candy/plugin-bundle (the command:bundle plugin's OpCompile leg)
// is byte-faithful to the former in-proc host compile, OVER the FULL plugin seam: the host computes
// the per-node selection (projectResolvedBox + the candy order + HostContext), Invokes the bundle
// plugin's OpCompile, the plugin re-hydrates the resolved-project envelope via HostBuild("resolved-project")
// + loops deploykit.BuildDeployPlan + projects []InstallPlanView, and the host re-materializes
// []*InstallPlan via deploykit.PlanFromView.
//
// For each fixture candy (across 3 classes — pkg/op/builder) the golden asserts BOTH:
//  (1) WireView parity: deploykit.WireView(oldPlan) DeepEqual deploykit.WireView(newPlan) — the
//      plugin-compiled + re-materialized plan projects to the SAME wire form as the former live
//      host-compile (the spike's byte-identity check, strengthened to a DeepEqual).
//  (2) PlanFromView fidelity: deploykit.PlanFromView(deploykit.WireView(newPlan)) DeepEqual newPlan —
//      the WireView→PlanFromView round-trip is the identity on a re-materialized plan, proving the
//      re-materialization step the host now performs loses nothing.
//
// The can-fail RIDER (subtest) perturbs the box Home and asserts the plans DIFFER — so the parity
// comparison is not vacuously passing on a constant (a silently-empty or perturbation-insensitive
// fixture would FAIL the rider). Non-vacuity is also guarded directly: ≥3 candies AND ≥2 step
// classes (pkg/op/builder) must appear in the plans.

func TestBundleCompileParity_PluginRoundTrip(t *testing.T) {
	isolateProviderRegistry(t)
	dir, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		t.Fatalf("LoadDefaultBuildConfig: %v", err)
	}
	RegisterBuildVocabulary(distroCfg)

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		t.Fatalf("ScanAllCandyWithConfig: %v", err)
	}

	// Hand-built fedora ResolvedBox (the compile target — a real builder config + fedora distro so
	// the pixi builder step resolves for pre-commit; mirrors the K4-B RDD spike).
	imgOld := &buildkit.ResolvedBox{
		Name:             "k4b-parity",
		EffectiveVersion: "2026.001.0001",
		Base:             "quay.io/fedora/fedora:43",
		IsExternalBase:   true,
		UID:              1000,
		GID:              1000,
		User:             "user",
		Home:             "/home/user",
		UserAdopted:      true,
		Distro:           []string{"fedora:43", "fedora"},
		BuildFormats:     []string{"rpm"},
		Pkg:              "rpm",
		DistroConfig:     distroCfg,
		BuilderConfig:    builderCfg,
	}
	imgOld.DistroDef = distroCfg.ResolveDistro(imgOld.Distro)

	boxView := projectResolvedBox(imgOld)

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
		// FLOOR-SLIM-proper Unit-8: candy/plugin-bundle's own compileDeployPlans now ALWAYS runs
		// the builder deploy-time pre-pass itself (over exec.InvokeProvider) before its BuildDeployPlan
		// loop — the host no longer pre-populates hostCtx.BuilderContext at all (builder_preresolve.go
		// keeps only the CONNECT step). So OLD must ALSO see a REAL, populated BuilderContext (computed
		// here via the host's own STILL-live providerRegistry — this test process has the pixi/npm/
		// cargo/aur plugins connected from ScanAllCandyWithConfig above) for the comparison to be
		// apples-to-apples; an artificially-empty hostCtx on both sides would silently stop exercising
		// the pixi-builder reverse_ops the "pre-commit" fixture is specifically chosen to cover.
		hostCtx := deploykit.HostContext{BuilderContext: testPreresolveBuilderContext(t, cfg, dir, name, layer, imgOld)}
		hostCtxJSON, err := json.Marshal(hostCtx)
		if err != nil {
			t.Fatalf("marshal host context (%s): %v", name, err)
		}
		// OLD: the former live host-compile (BuildDeployPlan over the runtime *Candy graph).
		oldPlan, err := deploykit.BuildDeployPlan(layer, imgOld, hostCtx)
		if err != nil {
			t.Fatalf("OLD BuildDeployPlan(%s): %v", name, err)
		}
		// NEW: the K4-B plugin compile — host computes the selection, plugin re-hydrates the envelope,
		// runs its OWN builder pre-pass (ignoring/recomputing hostCtxJSON.BuilderContext — production
		// never sends one populated any more), loops BuildDeployPlan + projects views, host
		// re-materializes via PlanFromView.
		plans, err := (&deployAddCmd{}).compileViaPlugin(spec.DeployCompileRequest{
			Dir:             dir,
			BoxView:         boxView,
			Order:           []string{name}, // single-candy compile, matching the OLD single-candy BuildDeployPlan
			HostContextJSON: hostCtxJSON,
		})
		if err != nil {
			t.Fatalf("NEW compileViaPlugin(%s): %v", name, err)
		}
		if len(plans) != 1 {
			t.Fatalf("NEW %s: expected 1 plan, got %d (%v)", name, len(plans), planCandyNames(plans))
		}
		newPlan := plans[0]

		// (1) WireView parity — the plugin-compiled plan projects to the SAME wire form. The wire
		// form (JSON) is what crosses the plugin boundary, so byte-identity of the marshaled WireView
		// is the honest parity test. (A struct-level reflect.DeepEqual would false-negative on a
		// benign Go-type narrowing in the OPAQUE RawInstallContext carry-through: the live candy's
		// YAML-canonicalized `[]string` vs the JSON-round-tripped re-hydrated `[]interface{}` both
		// serialize to the same bytes and both feed buildSystemPackagesStep's []string conversion
		// identically — the wire form, not the in-memory Go type, is the contract.)
		oldView := deploykit.WireView(oldPlan)
		newView := deploykit.WireView(newPlan)
		ob, _ := json.Marshal(oldView)
		nb, _ := json.Marshal(newView)
		if string(ob) != string(nb) {
			t.Fatalf("PARITY BREAK on %q (WireView wire form differs):\n--- OLD ---\n%s\n--- NEW ---\n%s", name, ob, nb)
		}

		// (2) PlanFromView fidelity — WireView→PlanFromView is the identity on the re-materialized plan.
		reread, err := deploykit.PlanFromView(newView)
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
		perturbed := projectResolvedBox(imgOld)
		perturbed.Home = "/home/OTHER"
		// An empty HostContextJSON matches production reality post-Unit-8: the host no longer
		// pre-populates BuilderContext at all — command:bundle's compileDeployPlans always
		// recomputes it itself, regardless of what (if anything) rides the wire.
		emptyHostCtxJSON, err := json.Marshal(deploykit.HostContext{})
		if err != nil {
			t.Fatalf("marshal empty host context: %v", err)
		}
		var broke bool
		for _, name := range exercised {
			plans, err := (&deployAddCmd{}).compileViaPlugin(spec.DeployCompileRequest{
				Dir:             dir,
				BoxView:         perturbed,
				Order:           []string{name},
				HostContextJSON: emptyHostCtxJSON,
			})
			if err != nil {
				t.Fatalf("perturbed compileViaPlugin(%s): %v", name, err)
			}
			if len(plans) != 1 {
				t.Fatalf("perturbed %s: expected 1 plan, got %d", name, len(plans))
			}
			nv := deploykit.WireView(plans[0])
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

func planCandyNames(plans []*deploykit.InstallPlan) []string {
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

// silence the os import if compilerTestProjectDir's cleanup is the only consumer in some build configs.
var _ = os.Chdir

// testPreresolveBuilderContext mirrors candy/plugin-bundle/builder_preresolve.go's
// preresolveBuilderContexts for a SINGLE candy, but dispatches via the host's OWN
// providerRegistry/Invoke (available in-process here, unlike a real plugin, which reaches the same
// builder plugins via exec.InvokeProvider) — the FLOOR-SLIM-proper Unit-8 test-side twin that lets
// this parity test's "OLD" comparison see the SAME real builder pre-resolution production now runs
// exclusively plugin-side. ensureBuildersConnected is charly-core's own (unmoved) connect step —
// same one preresolveBuildersInto calls in production.
func testPreresolveBuilderContext(t *testing.T, cfg *Config, dir, name string, layer spec.CandyReader, img *buildkit.ResolvedBox) map[string]deploykit.BuilderPreresolved {
	t.Helper()
	needed := deploykit.DetectExternalizedBuilders([]string{name}, map[string]spec.CandyReader{name: layer}, externalizedBuilders, img)
	if len(needed) == 0 {
		return nil
	}
	if err := ensureBuildersConnected(context.Background(), cfg, dir, needed); err != nil {
		// Connect can legitimately no-op-succeed here (every needed builder is already connected
		// from ScanAllCandyWithConfig's project load above) — only fail loudly on a REAL gap.
		for _, w := range needed {
			if _, ok := providerRegistry.ResolveBuilder(w); !ok {
				t.Fatalf("testPreresolveBuilderContext(%s): builder %q not connected: %v", name, w, err)
			}
		}
	}
	var out map[string]deploykit.BuilderPreresolved
	for _, bName := range needed {
		if img.BuilderConfig == nil {
			continue
		}
		bDef := img.BuilderConfig.Builder[bName]
		if bDef == nil || !deploykit.CandyNeedsBuilderStep(layer, bDef) {
			continue
		}
		prov, ok := providerRegistry.ResolveBuilder(bName)
		if !ok {
			t.Fatalf("testPreresolveBuilderContext(%s): builder %q is externalized but not connected", name, bName)
		}
		in := spec.BuilderCollectInput{Candy: layer.GetName(), Builder: bName, Home: img.Home}
		if bDef.DetectConfig != "" {
			if sec := layer.FormatSection(bDef.DetectConfig); sec != nil {
				in.Packages = append([]string(nil), sec.Packages...)
				if raw, ok := sec.Raw["replaces"]; ok {
					if list, ok := deploykit.StringSliceFromYAML(raw); ok {
						in.Replaces = list
					}
				}
			}
		}
		params, err := marshalJSON(in)
		if err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): marshal collect-context input: %v", name, err)
		}
		res, err := prov.Invoke(context.Background(), &Operation{Reserved: bName, Op: OpCollectContext, Params: params})
		if err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): builder %q collect-context: %v", name, bName, err)
		}
		var collectReply spec.BuilderCollectReply
		if err := json.Unmarshal(res.JSON, &collectReply); err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): decode collect-context reply: %v", name, err)
		}
		revParams, err := marshalJSON(spec.BuilderReverseInput{Candy: layer.GetName(), Builder: bName, Context: collectReply.Context})
		if err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): marshal reverse input: %v", name, err)
		}
		revRes, err := prov.Invoke(context.Background(), &Operation{Reserved: bName, Op: OpReverse, Params: revParams})
		if err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): builder %q reverse: %v", name, bName, err)
		}
		var reverseReply spec.BuilderReverseReply
		if err := json.Unmarshal(revRes.JSON, &reverseReply); err != nil {
			t.Fatalf("testPreresolveBuilderContext(%s): decode reverse reply: %v", name, err)
		}
		if out == nil {
			out = map[string]deploykit.BuilderPreresolved{}
		}
		out[deploykit.BuilderCtxKey(layer.GetName(), bName)] = deploykit.BuilderPreresolved{Context: collectReply.Context, Reverse: reverseReply.ReverseOps}
	}
	return out
}
