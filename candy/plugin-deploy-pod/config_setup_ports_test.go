package deploypod

import (
	"context"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestResolveDeployPorts_NilOverlaySiblingsAutoAllocateDistinctPorts is the task-#19 regression
// test: it FAILS on the pre-fix shape (the `if dc != nil && dc.Bundle != nil` guard in
// config_setup.go, before it was replaced by this test's target, resolveDeployPorts) and PASSES
// once a nil dc/dc.Bundle self-heals into an empty overlay instead of skipping resolution.
//
// Reproduces the exact real-world collision (see /tmp/terminus-rca.md, task #19): a disposable
// check bed's first-ever `charly config` runs in a fresh XDG-isolated environment with NO
// per-host overlay yet — dc is nil going in, exactly like loadDeploy's documented contract for an
// absent overlay. The pre-fix guard skipped port resolution entirely in that state, leaving
// meta.Port as the image label's bare container port ("18794"), which quadlet rendering then
// treats as a literal host==container 1:1 publish — so every deploy sharing that container port
// (all nine check-pod-derived beds) collided on the identical literal host port 18794.
//
// This test drives resolveDeployPorts twice against the SAME dc (starting nil, exactly like two
// sibling beds' first configs against the same real ~/.config/charly/charly.yml) with two
// DIFFERENT deploy keys sharing container port 18794 — mirroring check-tunnel-pod and
// check-stepkind-emit-pod. It asserts: (1) a nil dc/dc.Bundle no longer skips resolution — each
// call must persist a genuine resolved H:C pin, never the bare unresolved container port; and
// (2) OccupiedHostPorts correctly differentiates the two sibling keys once resolved into the
// same overlay, so they land on DISTINCT host ports — never colliding.
func TestResolveDeployPorts_NilOverlaySiblingsAutoAllocateDistinctPorts(t *testing.T) {
	saveStub := &saveBundleStub{}
	saveStub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	meta := &spec.BoxMetadata{Port: []string{"18794"}}

	var dc *deploykit.BundleConfig // nil: no per-host overlay yet — a fresh bed's first config

	key1 := spec.DeployKey("check-tunnel-pod", "")
	if err := resolveDeployPorts(context.Background(), ex, &dc, key1, meta); err != nil {
		t.Fatalf("resolveDeployPorts() [first call, nil overlay] error = %v", err)
	}
	if dc == nil || dc.Bundle == nil {
		t.Fatal("resolveDeployPorts() left dc/dc.Bundle nil after a nil-overlay first config — the task #19 bug: port resolution silently skipped")
	}
	entry1, ok := dc.Bundle[key1]
	if !ok || len(entry1.ResolvedPort) != 1 {
		t.Fatalf("dc.Bundle[%q] = %+v, want exactly one resolved port pin", key1, entry1)
	}
	pm1, ok := kit.ParsePortMapping(entry1.ResolvedPort[0])
	if !ok || pm1.Container != 18794 {
		t.Fatalf("resolved port %q, want a resolved H:18794 pin, not the bare unresolved label", entry1.ResolvedPort[0])
	}

	key2 := spec.DeployKey("check-stepkind-emit-pod", "")
	if err := resolveDeployPorts(context.Background(), ex, &dc, key2, meta); err != nil {
		t.Fatalf("resolveDeployPorts() [second call, sibling key, same dc] error = %v", err)
	}
	entry2, ok := dc.Bundle[key2]
	if !ok || len(entry2.ResolvedPort) != 1 {
		t.Fatalf("dc.Bundle[%q] = %+v, want exactly one resolved port pin", key2, entry2)
	}
	pm2, ok := kit.ParsePortMapping(entry2.ResolvedPort[0])
	if !ok || pm2.Container != 18794 {
		t.Fatalf("resolved port %q, want a resolved H:18794 pin, not the bare unresolved label", entry2.ResolvedPort[0])
	}

	if pm2.Host == pm1.Host {
		t.Fatalf("both sibling deploys (sharing container port 18794) resolved to the SAME host port %d — the exact collision task #19 fixes", pm1.Host)
	}
	if !saveStub.called {
		t.Error("resolveDeployPorts() did not persist via saveBundle")
	}
}

// TestResolveDeployPorts_NilDcPointerSelfHeals covers the more degenerate nil case: dc is a nil
// *deploykit.BundleConfig (not merely a non-nil BundleConfig with a nil Bundle) — the shape
// loadDeploy returns when the plugin-side load itself fails (a load error, not merely "absent").
// Mirrors persistResourceCaps' own *dc == nil branch (config_setup_helpers.go), which
// resolveDeployPorts shares.
func TestResolveDeployPorts_NilDcPointerSelfHeals(t *testing.T) {
	saveStub := &saveBundleStub{}
	saveStub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	meta := &spec.BoxMetadata{Port: []string{"18794"}}

	var dc *deploykit.BundleConfig
	key := spec.DeployKey("check-preempt-arbiter-pod", "")
	if err := resolveDeployPorts(context.Background(), ex, &dc, key, meta); err != nil {
		t.Fatalf("resolveDeployPorts() error = %v", err)
	}
	if dc == nil || dc.Bundle == nil {
		t.Fatal("resolveDeployPorts() left dc nil — a nil dc pointer must self-heal into an empty BundleConfig, exactly like persistResourceCaps")
	}
	if _, ok := dc.Bundle[key]; !ok {
		t.Fatalf("dc.Bundle[%q] missing after resolution", key)
	}
}

// TestResolveDeployPorts_NoPortsIsNoop asserts the fix does not regress the zero-port case: a box
// with no container ports and no pin must resolve to nothing and never call saveBundle.
func TestResolveDeployPorts_NoPortsIsNoop(t *testing.T) {
	saveStub := &saveBundleStub{}
	saveStub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	meta := &spec.BoxMetadata{}

	var dc *deploykit.BundleConfig
	key := spec.DeployKey("check-addcandy-pod", "")
	if err := resolveDeployPorts(context.Background(), ex, &dc, key, meta); err != nil {
		t.Fatalf("resolveDeployPorts() error = %v", err)
	}
	if saveStub.called {
		t.Error("resolveDeployPorts() called saveBundle for a portless deploy — want a no-op")
	}
}
