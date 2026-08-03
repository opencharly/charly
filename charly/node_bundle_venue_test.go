package main

import (
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// TestFlattenBundleVenues_StampsAndHoists / TestFlattenBundleVenues_GroupDirectStepRejected
// relocated to candy/plugin-loader (#55 decoupling; Batch A executed this move on Batch C's
// behalf per the cross-batch file-ownership matrix) — they asserted
// loaderkit.FlattenBundleVenues directly, zero charly dep.

// TestResolveDottedAgentProvisionedVenue relocated to candy/plugin-bundle (#55 decoupling,
// Batch A) — it asserted deploykit.ResolveDeployChain/ClassifyTarget directly, zero charly dep.

// TestOverlayRoundTrip_NestedChildSurvives (Risk 5a) proves the per-host overlay
// writer round-trips a deployment's NESTED CHILD + derived TARGET even though
// BundleNode.Children/Target are now yaml:"-" (the writer re-emits them via
// deploykit.MarshalBundleNode → node-form children). A lossy
// writer would silently drop the nested child on the next saveDeployState.
func TestOverlayRoundTrip_NestedChildSurvives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
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
	if err := testSaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}

	dc2, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("LoadBundleConfig (round-trip): %v", err)
	}
	got, ok := dc2.Bundle["myapp"]
	if !ok {
		t.Fatalf("round-trip lost the deploy entry myapp; got entries %v", bundleKeysOf(dc2.Bundle))
	}
	if deploykit.ClassifyTarget(&got) != "pod" {
		t.Errorf("round-trip target = %q, want pod (re-derived)", deploykit.ClassifyTarget(&got))
	}
	if got.Image != "web" {
		t.Errorf("round-trip box = %q, want web", got.Image)
	}
	inner, ok := got.Children["inner"]
	if !ok {
		t.Fatalf("round-trip LOST nested child %q (lossy overlay writer) — got children %v", "inner", childKeysOf(got.Children))
	}
	if deploykit.ClassifyTarget(inner) != "pod" {
		t.Errorf("nested child target = %q, want pod", deploykit.ClassifyTarget(inner))
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
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"shop": {
			Target:     "", // GROUP — no workload cross-ref
			Disposable: &disposable,
			Members: map[string]*spec.BundleNode{
				"web":    {Target: "pod", Image: "web"},
				"chrome": {Target: "pod", Image: "chrome-headless"},
			},
		},
	}}
	if err := testSaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}
	dc2, err := testLoadBundleConfig()
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
	deploykit.PersistBedDeployOverrides("check-cross-pod-cdp", groupBed, bedExternalInPlace(groupBed.Target), testBedMarshalNode, testLoadBundleConfig)

	dc, err := testLoadBundleConfig()
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
