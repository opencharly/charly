package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestFlattenBundleVenues_StampsAndHoists verifies the loader venue pass:
// member steps get a bare venue, nested-child steps a dotted venue, and all are
// hoisted into the root bundle's flat Plan (member/child Plans cleared).
func TestFlattenBundleVenues_StampsAndHoists(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]spec.BundleNode{
		// A pure-GROUP bed whose agent-provisioned member `os` carries a step.
		"default": {
			Target: "", // group
			Members: map[string]*spec.BundleNode{
				"os": {
					Target:           "pod",
					AgentProvisioned: true,
					Plan: []spec.Step{
						{Check: "marker present", Op: spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/charly-os-marker"}}},
					},
				},
			},
		},
		// A WORKLOAD bed (own container) with a direct step AND a nested child.
		"cross": {
			Target: "pod",
			Image:  "web",
			Plan: []spec.Step{
				{Check: "web serves marker", Op: spec.Op{Plugin: "http", PluginInput: map[string]any{"http": "http://127.0.0.1:8080/"}}},
			},
			Children: map[string]*spec.BundleNode{
				"migrate": {
					Target:           "pod",
					AgentProvisioned: true,
					Plan: []spec.Step{
						{Check: "migration ran", Op: cmdOp("test -f /done")},
					},
				},
			},
		},
	}}

	if err := flattenBundleVenues(uf); err != nil {
		t.Fatalf("flattenBundleVenues: %v", err)
	}

	// default: one step hoisted, venue == bare member name "os".
	def := uf.Bundle["default"]
	if len(def.Plan) != 1 {
		t.Fatalf("default: want 1 hoisted step, got %d", len(def.Plan))
	}
	if def.Plan[0].Venue != "os" {
		t.Errorf("default member step venue = %q, want %q", def.Plan[0].Venue, "os")
	}
	if p := def.Members["os"].Plan; len(p) != 0 {
		t.Errorf("default member os.Plan should be cleared after hoist, got %d steps", len(p))
	}

	// cross: root step venue == "cross"; nested-child step venue == "cross.migrate".
	cross := uf.Bundle["cross"]
	if len(cross.Plan) != 2 {
		t.Fatalf("cross: want 2 steps (root + hoisted child), got %d", len(cross.Plan))
	}
	venues := map[string]bool{}
	for _, s := range cross.Plan {
		venues[s.Venue] = true
	}
	if !venues["cross"] {
		t.Errorf("cross: missing root-venue step (venue %q); got venues %v", "cross", venues)
	}
	if !venues["cross.migrate"] {
		t.Errorf("cross: missing nested-child dotted venue %q; got venues %v", "cross.migrate", venues)
	}
}

// TestFlattenBundleVenues_GroupDirectStepRejected verifies a direct step under a
// pure group bundle (no workload container) is a hard error — a group has no
// venue of its own.
func TestFlattenBundleVenues_GroupDirectStepRejected(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]spec.BundleNode{
		"grp": {
			Target: "", // group, but carries a direct step → illegal
			Plan: []spec.Step{
				{Check: "stray", Op: cmdOp("true")},
			},
		},
	}}
	if err := flattenBundleVenues(uf); err == nil {
		t.Fatalf("expected error for a direct step under a group bundle, got nil")
	}
}

// TestResolveDottedAgentProvisionedVenue (Risk 5b) proves resolveScoringChain /
// ResolveDeployChain reach a 3-level agent-provisioned venue
// (vm → pod → pod) written into a scratch deploy-tree map — without a live
// connection (the chain is built, not dialed). This is the unit-level proof the
// coordinator's R10 live bed round-trip will exercise end-to-end.
func TestResolveDottedAgentProvisionedVenue(t *testing.T) {
	roots := map[string]spec.BundleNode{
		"nested-check-vm": {
			Target:           "vm",
			From:             "nested-check-vm",
			AgentProvisioned: true,
			Children: map[string]*spec.BundleNode{
				"inner-app-pod": {
					Target:           "pod",
					AgentProvisioned: true,
					Children: map[string]*spec.BundleNode{
						"nested-redis-pod": {
							Target:           "pod",
							AgentProvisioned: true,
						},
					},
				},
			},
		},
	}
	const dotted = "nested-check-vm.inner-app-pod.nested-redis-pod"

	leaf, chain, err := ResolveDeployChain(stampTestDescents(roots), dotted, ShellExecutor{})
	if err != nil {
		t.Fatalf("ResolveDeployChain(%q): %v", dotted, err)
	}
	if leaf == nil {
		t.Fatalf("ResolveDeployChain(%q): nil leaf", dotted)
	}
	if classifyTarget(leaf) != "pod" {
		t.Errorf("leaf target = %q, want pod", classifyTarget(leaf))
	}
	if chain == nil {
		t.Fatalf("ResolveDeployChain(%q): nil chain", dotted)
	}

	// resolveScoringChain (the scorer entry point) must route the dotted venue
	// through ResolveDeployChain and return a chain too.
	sc, scErr := resolveScoringChain(stampTestDescents(roots), dotted)
	if scErr != nil {
		t.Fatalf("resolveScoringChain(%q): %v", dotted, scErr)
	}
	if sc == nil {
		t.Fatalf("resolveScoringChain(%q): nil chain", dotted)
	}
}

// TestResolveBareAgentProvisionedVenue proves a bare agent-provisioned venue
// (the common iterate-bench case, e.g. `os`) resolves via resolveScoringChain's
// bare-name fallback to the `charly-<name>` container the agent deploys —
// without any top-level bundle entry (agent-provisioned members are not folded).
func TestResolveBareAgentProvisionedVenue(t *testing.T) {
	roots := map[string]spec.BundleNode{} // os is NOT a top-level entry (not folded)
	sc, err := resolveScoringChain(stampTestDescents(roots), "os")
	if err != nil {
		t.Fatalf("resolveScoringChain(os): %v", err)
	}
	if sc == nil {
		t.Fatalf("resolveScoringChain(os): nil chain (bare-name fallback failed)")
	}
}

// TestOverlayRoundTrip_NestedChildSurvives (Risk 5a) proves the per-host overlay
// writer round-trips a deployment's NESTED CHILD + derived TARGET even though
// BundleNode.Children/Target are now yaml:"-" (the writer re-emits them via
// marshalBundleNode → node-form children). A lossy
// writer would silently drop the nested child on the next saveDeployState.
func TestOverlayRoundTrip_NestedChildSurvives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	dc := &BundleConfig{Bundle: map[string]spec.BundleNode{
		"myapp": {
			Target:     "pod",
			Image:      "web",
			Disposable: &disposable,
			Children: map[string]*spec.BundleNode{
				"inner": {
					Target: "pod",
					Image:  "db",
				},
			},
		},
	}}
	if err := saveBundleConfigNodeForm(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}

	dc2, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("LoadBundleConfig (round-trip): %v", err)
	}
	got, ok := dc2.Bundle["myapp"]
	if !ok {
		t.Fatalf("round-trip lost the deploy entry myapp; got entries %v", bundleKeysOf(dc2.Bundle))
	}
	if classifyTarget(&got) != "pod" {
		t.Errorf("round-trip target = %q, want pod (re-derived)", classifyTarget(&got))
	}
	if got.Image != "web" {
		t.Errorf("round-trip box = %q, want web", got.Image)
	}
	inner, ok := got.Children["inner"]
	if !ok {
		t.Fatalf("round-trip LOST nested child %q (lossy overlay writer) — got children %v", "inner", childKeysOf(got.Children))
	}
	if classifyTarget(inner) != "pod" {
		t.Errorf("nested child target = %q, want pod", classifyTarget(inner))
	}
	if inner.Image != "db" {
		t.Errorf("nested child box = %q, want db", inner.Image)
	}
}

// TestOverlayRoundTrip_GroupMembersSurvive proves the per-host overlay writer
// round-trips a GROUP bed (Target=="" + sibling Members — the §3 cross-deploy
// shape) without dropping its members. A lossy round-trip would re-emit a
// MEMBERLESS group bed, which validateCheckBeds then rejects on the next
// LoadBundleConfig — exactly the saveDeployState warning seen during the group
// bed's bringUpMembers (persistBedDeployOverrides on a member reloads the
// overlay). The load itself is the assertion: a memberless group bed fails it.
func TestOverlayRoundTrip_GroupMembersSurvive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	dc := &BundleConfig{Bundle: map[string]spec.BundleNode{
		"shop": {
			Target:     "", // GROUP — no workload cross-ref
			Disposable: &disposable,
			Members: map[string]*spec.BundleNode{
				"web":    {Target: "pod", Image: "web"},
				"chrome": {Target: "pod", Image: "chrome-headless"},
			},
		},
	}}
	if err := saveBundleConfigNodeForm(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}
	dc2, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("LoadBundleConfig (round-trip) — a memberless group bed fails validateCheckBeds: %v", err)
	}
	got, ok := dc2.Bundle["shop"]
	if !ok {
		t.Fatalf("round-trip lost the group bundle 'shop'; got %v", bundleKeysOf(dc2.Bundle))
	}
	if len(got.Members) != 2 || got.Members["web"] == nil || got.Members["chrome"] == nil {
		t.Fatalf("round-trip LOST group members: got %v", childKeysOf(got.Members))
	}
}

// TestPersistBedDeployOverrides_GroupBedNotPersisted proves the root-cause fix:
// persisting a GROUP bed root is a no-op, so it never writes a memberless bed to
// the per-host overlay (which validateCheckBeds would reject on the next load,
// poisoning every subsequent saveDeployState). Without the guard, this writes a
// boxless/memberless check bed and LoadBundleConfig then fails.
func TestPersistBedDeployOverrides_GroupBedNotPersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	groupBed := spec.BundleNode{
		Target:     "", // GROUP — no workload cross-ref
		Disposable: &disposable,
		Members:    map[string]*spec.BundleNode{"web": {Target: "pod", Image: "web"}},
	}
	persistBedDeployOverrides("check-cross-pod-cdp", groupBed)

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("overlay poisoned by persisting a group bed root: %v", err)
	}
	if dc != nil {
		if _, present := dc.Bundle["check-cross-pod-cdp"]; present {
			t.Errorf("group bed root was persisted to the overlay — it must be skipped (no root deploy to seed)")
		}
	}
}

func bundleKeysOf(m map[string]spec.BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func childKeysOf(m map[string]*spec.BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
