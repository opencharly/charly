package main

import (
	"context"
	"sort"
	"testing"
)

// androidBedUnified builds a synthetic UnifiedFile mirroring the
// check-android-emulator-pod bed: a target:pod root with two nested
// target:android children (an in-pod image device + a remote adb endpoint
// device), plus the matching kind:android specs. Used to drive the pure
// enumeration paths hermetically (no live adb / podman).
func androidBedUnified() *UnifiedFile {
	return &UnifiedFile{
		Android: map[string]*AndroidSpec{
			"pixel9a-36":       {Box: "android-emulator"},
			"pixel9a-endpoint": {Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:1"}, Serial: "emulator-5554"},
		},
		Bundle: map[string]BundleNode{
			"check-android-emulator-pod": {
				Target: "pod",
				Image:  "android-emulator",
				Children: map[string]*BundleNode{
					"device": {
						Target:   "android",
						From:     "pixel9a-36",
						AddCandy: []string{"android-test-apps"},
					},
					"device-net": {
						Target:   "android",
						From:     "pixel9a-endpoint",
						AddCandy: []string{"android-apidemos"},
					},
				},
			},
			// A plain pod deploy with no android children — must not contribute.
			"some-pod": {Target: "pod", Image: "whatever"},
		},
	}
}

func TestAndroidCollector_Kind(t *testing.T) {
	a := &AndroidCollector{}
	if a.Kind() != SubstrateAndroid {
		t.Errorf("Kind() = %q, want %q", a.Kind(), SubstrateAndroid)
	}
}

func TestAndroidCollector_AvailableFalseWhenNoAndroidDeploy(t *testing.T) {
	a := &AndroidCollector{}
	// Empty opts → no unified, no deploy → no android nodes.
	if a.Available(CollectOpts{}) {
		t.Error("Available() = true, want false with no declared android deploy")
	}
	// A unified with only a plain pod deploy is still unavailable.
	uf := &UnifiedFile{Bundle: map[string]BundleNode{"x": {Target: "pod", Image: "y"}}}
	if a.Available(CollectOpts{Unified: uf}) {
		t.Error("Available() = true, want false when no target:android node exists")
	}
}

func TestAndroidCollector_AvailableTrueWhenAndroidDeployDeclared(t *testing.T) {
	a := &AndroidCollector{}
	if !a.Available(CollectOpts{Unified: androidBedUnified()}) {
		t.Error("Available() = false, want true when nested target:android nodes exist")
	}
}

// collectAndroidDeployNodes must walk top-level AND nested nodes, addressing
// each by its full dotted deploy path, and ignore non-android nodes.
func TestCollectAndroidDeployNodes_EnumeratesNestedByDottedPath(t *testing.T) {
	nodes := collectAndroidDeployNodes(CollectOpts{Unified: androidBedUnified()})
	if len(nodes) != 2 {
		t.Fatalf("collectAndroidDeployNodes() = %d nodes, want 2", len(nodes))
	}
	paths := []string{nodes[0].path, nodes[1].path}
	sort.Strings(paths)
	want := []string{"check-android-emulator-pod.device", "check-android-emulator-pod.device-net"}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// A top-level (non-nested) target:android deploy must also be enumerated, with
// the bare deploy key as its path.
func TestCollectAndroidDeployNodes_TopLevel(t *testing.T) {
	uf := &UnifiedFile{
		Android: map[string]*AndroidSpec{"dev": {Adb: &AndroidAdbEndpoint{Host: "h:1"}}},
		Bundle: map[string]BundleNode{
			"phone": {Target: "android", From: "dev"},
		},
	}
	nodes := collectAndroidDeployNodes(CollectOpts{Unified: uf})
	if len(nodes) != 1 || nodes[0].path != "phone" {
		t.Fatalf("collectAndroidDeployNodes() = %#v, want one node path=phone", nodes)
	}
}

// Local deploy.yml overrides the unified projection per key (mirrors
// resolveTreeRoot's MergeDeployConfigs(projectDC, localDC) precedence).
func TestCollectAndroidDeployNodes_DeployYamlWinsPerKey(t *testing.T) {
	uf := &UnifiedFile{
		Android: map[string]*AndroidSpec{"dev": {Adb: &AndroidAdbEndpoint{Host: "h:1"}}},
		Bundle:  map[string]BundleNode{"phone": {Target: "android", From: "dev"}},
	}
	// deploy.yml flips "phone" to a pod target — the android node must disappear.
	local := &BundleConfig{Bundle: map[string]BundleNode{
		"phone": {Target: "pod", Image: "x"},
	}}
	nodes := collectAndroidDeployNodes(CollectOpts{Unified: uf, Deploy: local})
	if len(nodes) != 0 {
		t.Fatalf("collectAndroidDeployNodes() = %d nodes, want 0 (deploy.yml overrode phone to pod)", len(nodes))
	}
}

// collectOne against a (resolvable) endpoint device reports "declared": with the
// goadb device-state probe gone from core (the adb → external-plugin dep-shed), an
// endpoint's live state is only assertable via the `adb:` check verb, so the status
// collector enumerates it as declared rather than probing it. Row fields (path, serial,
// venue note, run mode) are still populated. Runs fully hermetically — no live adb.
func TestAndroidCollector_CollectOneEndpointDeclared(t *testing.T) {
	a := &AndroidCollector{}
	dn := androidDeployNode{
		path: "phone",
		node: BundleNode{Target: "android", From: "dev"},
	}
	opts := CollectOpts{
		RunMode: "quadlet",
		Unified: &UnifiedFile{
			Android: map[string]*AndroidSpec{
				"dev": {Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:1"}, Serial: "emulator-5554"},
			},
		},
	}
	row := a.collectOne(opts, dn)
	if row.Kind != SubstrateAndroid {
		t.Errorf("Kind = %q, want %q", row.Kind, SubstrateAndroid)
	}
	if row.Source != "adb" {
		t.Errorf("Source = %q, want adb", row.Source)
	}
	if row.Image != "phone" {
		t.Errorf("Image (path) = %q, want phone", row.Image)
	}
	if row.Status != "declared" {
		t.Errorf("Status = %q, want declared (endpoint, no in-core probe)", row.Status)
	}
	if row.Container != "emulator-5554" {
		t.Errorf("Container (serial) = %q, want emulator-5554", row.Container)
	}
	if row.Network != "endpoint 127.0.0.1:1" {
		t.Errorf("Network = %q, want endpoint 127.0.0.1:1", row.Network)
	}
	if row.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want quadlet (from opts)", row.RunMode)
	}
}

// A node referencing an undeclared kind:android device yields an absent row
// naming the missing reference, not a panic.
func TestAndroidCollector_CollectOneUndeclaredDevice(t *testing.T) {
	a := &AndroidCollector{}
	dn := androidDeployNode{path: "phone", node: BundleNode{Target: "android", From: "ghost"}}
	row := a.collectOne(CollectOpts{Unified: &UnifiedFile{}}, dn)
	if row.Status != "absent" {
		t.Errorf("Status = %q, want absent for undeclared device", row.Status)
	}
	if row.Container != "ghost" {
		t.Errorf("Container = %q, want ghost (the undeclared ref)", row.Container)
	}
}

// Collect over the bed unified produces one row per nested device, status derived
// host-side WITHOUT goadb: the in-pod device is "absent" (its emulator pod isn't running
// in the test environment, so resolveAndroidDevice degrades), and the endpoint device is
// "declared" (an endpoint's live state is only assertable via the `adb:` check verb).
func TestAndroidCollector_CollectBed(t *testing.T) {
	a := &AndroidCollector{}
	rows, err := a.Collect(context.Background(), CollectOpts{Unified: androidBedUnified()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Collect() = %d rows, want 2", len(rows))
	}
	wantStatus := map[string]string{
		"check-android-emulator-pod.device":     "absent",   // in-pod, pod not running
		"check-android-emulator-pod.device-net": "declared", // endpoint, no in-core probe
	}
	for _, r := range rows {
		if r.Kind != SubstrateAndroid || r.Source != "adb" {
			t.Errorf("row kind/source = %q/%q, want android/adb", r.Kind, r.Source)
		}
		want, ok := wantStatus[r.Image]
		if !ok {
			t.Errorf("unexpected row path %q", r.Image)
			continue
		}
		if r.Status != want {
			t.Errorf("row %q Status = %q, want %q", r.Image, r.Status, want)
		}
	}
}
