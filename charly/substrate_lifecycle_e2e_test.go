package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubstrateLifecycle_PrepareVenueDescriptorRoundTrip proves the F6 HIGHEST-risk assumption on a
// HOST-LOCAL venue (fast, no VM): a deploy-substrate plugin (candy/plugin-example-lifecycle, NOT
// compiled in) serves OpPrepareVenue over Provider.Invoke, returns a self-contained VenueDescriptor,
// and the host's grpcSubstrateLifecycle re-materializes it into a REAL DeployExecutor — the live
// executor never crossing the wire. This is the descriptor round-trip that lets a substrate lifecycle
// run out-of-process (the channel M4 reuses to externalize pod/vm). Builds the real plugin OOP, so
// -short-gated. The slow live-SSH-descriptor + auto-boot path is the check-k3s-vm bed (M4-time).
func TestSubstrateLifecycle_PrepareVenueDescriptorRoundTrip(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds the external lifecycle plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("example lifecycle plugin module not found at %s: %v", srcDir, err)
	}

	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-lifecycle-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassDeployTarget || unit.Providers[0].Reserved() != "examplelifecycle" {
		t.Fatalf("providers = %+v, want exactly one deploy:examplelifecycle", unit.Providers)
	}
	gp, ok := unit.Providers[0].(*grpcProvider)
	if !ok {
		t.Fatalf("provider is %T, want *grpcProvider", unit.Providers[0])
	}
	if !gp.lifecycle {
		t.Fatal("deploy provider did not carry lifecycle=true from its Describe capability")
	}

	// The registration path: RegisterPluginProviders wires a wire-backed substrateLifecycle for the
	// substrate word, so externalDeployTarget drives the plugin's lifecycle transparently.
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "f6-lifecycle-test", nil); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	reg, ok := substrateLifecycleFor("examplelifecycle")
	if !ok {
		t.Fatal("RegisterPluginProviders did not register a wire-backed lifecycle for examplelifecycle")
	}
	if _, isWire := reg.(grpcSubstrateLifecycle); !isWire {
		t.Fatalf("registered lifecycle is %T, want grpcSubstrateLifecycle", reg)
	}

	// The preresolver path (F6's other half): the substrate declares a host-side preresolve step.
	if !gp.preresolve {
		t.Fatal("deploy provider did not carry preresolve=true from its Describe capability")
	}
	pre, ok := deployPreresolverFor("examplelifecycle")
	if !ok {
		t.Fatal("RegisterPluginProviders did not register a wire-backed preresolver for examplelifecycle")
	}
	sub, err := pre("my-lifecycle", "", nil, nil)
	if err != nil {
		t.Fatalf("wire preresolver: %v", err)
	}
	if !strings.Contains(string(sub), "examplelifecycle_preresolved") {
		t.Fatalf("preresolve payload = %s, missing the round-tripped marker", sub)
	}

	// The host adapter: it Invokes the plugin's OpPrepareVenue and re-materializes the descriptor.
	lc := grpcSubstrateLifecycle{prov: gp}
	exec, err := lc.PrepareVenue(ctx, "my-lifecycle", "", nil, nil, EmitOpts{})
	if err != nil {
		t.Fatalf("PrepareVenue: %v", err)
	}
	if _, isShell := exec.(ShellExecutor); !isShell {
		t.Fatalf("re-materialized executor is %T, want ShellExecutor (from the shell VenueDescriptor)", exec)
	}
	if exec.Venue() != "local" {
		t.Fatalf("re-materialized executor venue = %q, want local", exec.Venue())
	}

	// TeardownExecutor returns an empty descriptor → the host keeps its own executor (nil).
	tex, err := lc.TeardownExecutor("my-lifecycle", nil)
	if err != nil {
		t.Fatalf("TeardownExecutor: %v", err)
	}
	if tex != nil {
		t.Fatalf("TeardownExecutor = %T, want nil (empty descriptor → caller keeps its executor)", tex)
	}

	// A lifecycle leg (Start) round-trips host→plugin without error.
	if err := lc.Start(ctx, "my-lifecycle", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
}
