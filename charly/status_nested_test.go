package main

import (
	"testing"
)

// nestedUnified builds a minimal UnifiedFile projection carrying one declared
// nested topology: a target:pod parent `check-android-emulator-pod` with two
// target:android nested children `device` and `device-net` (the
// check-android-emulator-pod shape), plus an unrelated flat pod deploy the
// overlay must leave alone.
func nestedUnified() *UnifiedFile {
	return &UnifiedFile{
		Bundle: map[string]BundleNode{
			"check-android-emulator-pod": {
				Target: "pod",
				Image:  "android-emulator",
				Children: map[string]*BundleNode{
					"device":     {Target: "android", From: "pixel9a-36", AddCandy: []string{"android-test-apps"}},
					"device-net": {Target: "android", From: "pixel9a-endpoint", AddCandy: []string{"android-apidemos"}},
				},
			},
			"some-flat-pod": {Target: "pod", Image: "redis"},
		},
	}
}

// findNested returns the nested child row with the given Image cell, or nil.
func findNested(children []DeploymentStatus, image string) *DeploymentStatus {
	for i := range children {
		if children[i].Image == image {
			return &children[i]
		}
	}
	return nil
}

// TestNestedOverlay_AttachesDeclaredChildren verifies the default (no --nested)
// path: declared nested children attach to the matching parent flat row, each
// stamped with its declared kind + Source="nested", and an unrelated flat row
// is untouched.
func TestNestedOverlay_AttachesDeclaredChildren(t *testing.T) {
	const parent = "check-android-emulator-pod"
	rows := []DeploymentStatus{
		{Kind: SubstratePod, Image: parent, Status: "running", Container: "charly-" + parent, Source: "podman"},
		{Kind: SubstratePod, Image: "redis", Status: "running", Container: "charly-redis", Source: "podman"},
	}
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet"}

	out := applyNestedOverlay(rows, opts)

	// Parent row must carry both declared children.
	var prow *DeploymentStatus
	for i := range out {
		if out[i].Image == parent {
			prow = &out[i]
		}
	}
	if prow == nil {
		t.Fatalf("parent row %q vanished from overlay output", parent)
	}
	if len(prow.Nested) != 2 {
		t.Fatalf("parent.Nested = %d children, want 2: %+v", len(prow.Nested), prow.Nested)
	}
	for _, key := range []string{"device", "device-net"} {
		c := findNested(prow.Nested, key)
		if c == nil {
			t.Fatalf("nested child %q not attached", key)
		}
		if c.Kind != SubstrateAndroid {
			t.Errorf("child %q Kind = %q, want %q", key, c.Kind, SubstrateAndroid)
		}
		if c.Source != "nested" {
			t.Errorf("child %q Source = %q, want %q", key, c.Source, "nested")
		}
		// Default (no --nested): declared, no live probe, no flat row to borrow.
		if c.Status != "declared" {
			t.Errorf("child %q Status = %q, want %q (cheap default)", key, c.Status, "declared")
		}
	}

	// The unrelated flat pod must NOT have grown children.
	for i := range out {
		if out[i].Image == "redis" && len(out[i].Nested) != 0 {
			t.Errorf("unrelated row redis grew %d nested children, want 0", len(out[i].Nested))
		}
	}
}

// TestNestedOverlay_NoParentRowNoPhantom verifies a declared parent with no
// flat row (not running, not in --all) attaches nothing — the overlay never
// synthesizes a phantom parent.
func TestNestedOverlay_NoParentRowNoPhantom(t *testing.T) {
	// Only the unrelated flat row is present; the declared parent has no row.
	rows := []DeploymentStatus{
		{Kind: SubstratePod, Image: "redis", Status: "running", Container: "charly-redis", Source: "podman"},
	}
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet"}

	out := applyNestedOverlay(rows, opts)

	if len(out) != 1 {
		t.Fatalf("row count = %d, want 1 (no phantom parent synthesized): %+v", len(out), out)
	}
	if out[0].Image != "redis" {
		t.Fatalf("unexpected row %q", out[0].Image)
	}
	if len(out[0].Nested) != 0 {
		t.Errorf("redis grew %d nested children, want 0", len(out[0].Nested))
	}
}

// TestNestedOverlay_MovesFlatPodRow verifies the vm->pod dedup: a nested pod
// that ALSO exists as a top-level flat row (its own charly-<flat-path> container
// was collected) is MOVED under its parent — inheriting the flat row's real
// data and provenance — AND REMOVED from the top level so it appears once.
func TestNestedOverlay_MovesFlatPodRow(t *testing.T) {
	const parent = "stack-vm"
	uf := &UnifiedFile{
		Bundle: map[string]BundleNode{
			parent: {
				Target: "vm",
				From:   "stack-vm",
				Children: map[string]*BundleNode{
					"web": {Target: "pod", Image: "nginx"},
				},
			},
		},
	}
	// NestedContainerName("stack-vm.web") == "stack-vm_web"; that flat key is
	// the move coordinate.
	flatKey := NestedContainerName(parent + ".web")
	rows := []DeploymentStatus{
		{Kind: SubstrateVM, Image: parent, Status: "running", Container: "stack-vm", Source: "libvirt"},
		{Kind: SubstratePod, Image: flatKey, Status: "running", Uptime: "Up 3 minutes", Container: "charly-" + flatKey, Source: "podman"},
	}
	opts := CollectOpts{Unified: uf, RunMode: "quadlet"}

	out := applyNestedOverlay(rows, opts)

	// Dedup: the flat pod row must be GONE from the top level (moved under the
	// parent), leaving only the parent row at the top.
	if len(out) != 1 {
		t.Fatalf("top-level row count = %d, want 1 (flat nested-pod row must be moved, not duplicated): %+v", len(out), out)
	}
	for i := range out {
		if out[i].Image == flatKey {
			t.Fatalf("flat nested-pod row %q still present at top level — not deduplicated", flatKey)
		}
	}

	prow := &out[0]
	if prow.Image != parent {
		t.Fatalf("sole top-level row = %q, want parent %q", prow.Image, parent)
	}
	if len(prow.Nested) != 1 {
		t.Fatalf("parent %q must carry exactly 1 nested child, got %+v", parent, prow.Nested)
	}
	web := prow.Nested[0]
	if web.Status != "running" {
		t.Errorf("nested web Status = %q, want %q (moved from flat row)", web.Status, "running")
	}
	if web.Uptime != "Up 3 minutes" {
		t.Errorf("nested web Uptime = %q, want moved %q", web.Uptime, "Up 3 minutes")
	}
	if web.Container != "charly-"+flatKey {
		t.Errorf("nested web Container = %q, want moved %q", web.Container, "charly-"+flatKey)
	}
	if web.Source != "podman" {
		t.Errorf("nested web Source = %q, want preserved flat provenance %q", web.Source, "podman")
	}
	if web.Kind != SubstratePod {
		t.Errorf("nested web Kind = %q, want %q", web.Kind, SubstratePod)
	}
}

// TestNestedOverlay_MovesFlatAndroidRowOnly verifies the canonical
// check-android-emulator-pod dedup: a nested android device that ALSO surfaced
// as a flat AndroidCollector row (keyed on its DOTTED path) is moved under its
// parent and removed from the top level, while a sibling declared-only child
// with no flat row is still SYNTHESIZED in place (Source "nested"). This proves
// the move and the synthesize paths coexist in one parent.
func TestNestedOverlay_MovesFlatAndroidRowOnly(t *testing.T) {
	const parent = "check-android-emulator-pod"
	// Flat rows: the parent pod, PLUS one flat AndroidCollector row for the
	// "device" child keyed on its dotted path (Source "adb"). The "device-net"
	// child has NO flat row.
	devicePath := parent + ".device"
	rows := []DeploymentStatus{
		{Kind: SubstratePod, Image: parent, Status: "running", Container: "charly-" + parent, Source: "podman"},
		{Kind: SubstrateAndroid, Image: devicePath, Status: "online", Container: "emulator-5554", Network: "in-pod (charly-" + parent + ")", Source: "adb"},
	}
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet"}

	out := applyNestedOverlay(rows, opts)

	// Dedup: the flat android row must be GONE from the top level.
	if len(out) != 1 {
		t.Fatalf("top-level row count = %d, want 1 (flat android row must be moved): %+v", len(out), out)
	}
	for i := range out {
		if out[i].Image == devicePath {
			t.Fatalf("flat android row %q still present at top level — not deduplicated", devicePath)
		}
	}

	prow := &out[0]
	if prow.Image != parent || len(prow.Nested) != 2 {
		t.Fatalf("parent %q must carry 2 nested children, got %+v", parent, prow)
	}

	// The "device" child inherited the flat android row's real data + provenance.
	dev := findNested(prow.Nested, "device")
	if dev == nil {
		t.Fatalf("nested child %q not attached", "device")
	}
	if dev.Status != "online" {
		t.Errorf("moved child device Status = %q, want %q (from flat android row)", dev.Status, "online")
	}
	if dev.Container != "emulator-5554" {
		t.Errorf("moved child device Container = %q, want %q", dev.Container, "emulator-5554")
	}
	if dev.Source != "adb" {
		t.Errorf("moved child device Source = %q, want preserved %q (NOT restamped nested)", dev.Source, "adb")
	}

	// The "device-net" child had NO flat row → synthesized declared, Source nested.
	devNet := findNested(prow.Nested, "device-net")
	if devNet == nil {
		t.Fatalf("nested child %q not attached", "device-net")
	}
	if devNet.Status != "declared" {
		t.Errorf("synthesized child device-net Status = %q, want %q", devNet.Status, "declared")
	}
	if devNet.Source != "nested" {
		t.Errorf("synthesized child device-net Source = %q, want %q", devNet.Source, "nested")
	}
}

// TestNestedOverlay_LiveProbeUnreachable verifies the --nested path under no
// live backend: a declared nested child whose multi-hop venue can't be reached
// renders Status="unreachable" (deadline-bounded, never blocks the table).
func TestNestedOverlay_LiveProbeUnreachable(t *testing.T) {
	const parent = "check-android-emulator-pod"
	rows := []DeploymentStatus{
		{Kind: SubstratePod, Image: parent, Status: "running", Container: "charly-" + parent, Source: "podman"},
	}
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet", Nested: true}

	out := applyNestedOverlay(rows, opts)

	var prow *DeploymentStatus
	for i := range out {
		if out[i].Image == parent {
			prow = &out[i]
		}
	}
	if prow == nil || len(prow.Nested) != 2 {
		t.Fatalf("parent %q must carry 2 nested children under --nested, got %+v", parent, prow)
	}
	// android nested children reached over a non-existent emulator port probe
	// to unreachable. The assertion is the load-bearing one: --nested NEVER
	// leaves a child at "declared" — it always resolves to a live verdict.
	for _, c := range prow.Nested {
		if c.Status != "unreachable" {
			t.Errorf("child %q Status = %q under --nested with no backend, want %q",
				c.Image, c.Status, "unreachable")
		}
	}
}

// TestNestedOverlay_NilConfigNoOp verifies the overlay is a clean no-op when no
// deploy config is available (opts.Unified == nil, opts.Deploy == nil): rows
// pass through unchanged so `charly status` never regresses on a config-less host.
func TestNestedOverlay_NilConfigNoOp(t *testing.T) {
	rows := []DeploymentStatus{
		{Kind: SubstratePod, Image: "redis", Status: "running", Container: "charly-redis", Source: "podman"},
	}
	out := applyNestedOverlay(rows, CollectOpts{RunMode: "quadlet"})
	if len(out) != 1 || out[0].Image != "redis" || len(out[0].Nested) != 0 {
		t.Fatalf("nil-config overlay must be a no-op, got %+v", out)
	}
}
