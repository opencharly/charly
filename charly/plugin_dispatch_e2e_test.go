package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestPluginDispatch_InvokeProviderAndHostBuild proves the F10 reverse legs END-TO-END on real
// code: the candy/plugin-example-dispatch plugin (A) is host-built + served OUT-OF-PROCESS and
// driven via InvokeWithExecutor (so its broker serves ExecutorService incl. the F10 RPCs); during
// its Invoke A calls BACK to the host to (1) InvokeProvider the COMPILED-IN externalprobe verb
// (plugin↔plugin — the nested-broker round-trip: A's Invoke is in-flight while the host dispatches
// the peer) and (2) HostBuild a candy's plugin binary (host-build). Builds real binaries, so
// -short-gated.
func TestPluginDispatch_InvokeProviderAndHostBuild(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds plugin binaries OOP (slow)")
	}
	ctx := context.Background()

	buildDir, err := filepath.Abs("../candy/plugin-example-kind")
	if err != nil {
		t.Fatal(err)
	}
	srcA, err := filepath.Abs("../candy/plugin-example-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcA, "go.mod")); err != nil {
		t.Fatalf("dispatch plugin module not found at %s: %v", srcA, err)
	}

	binA, err := buildPluginBinary(ctx, srcA, "plugin-example-dispatch-test")
	if err != nil {
		t.Fatalf("buildPluginBinary A: %v", err)
	}
	unitA, closerA, err := (&LocalTransport{BinPath: binA}).Connect(ctx)
	if err != nil {
		t.Fatalf("connect A: %v", err)
	}
	defer func() { _ = closerA.Close() }()
	// Register A's providers so the OUT-OF-PROCESS peer (exampledispatchpeer) is resolvable when A
	// dispatches it via InvokeProvider — exercising the OOP nested-broker branch (the HIGH-risk path).
	if err := providerRegistry.RegisterPluginProviders(unitA.Providers, "f10-dispatch-test", nil); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	var gpA *grpcProvider
	for _, p := range unitA.Providers {
		if p.Reserved() == "exampledispatch" {
			gpA = p.(*grpcProvider)
		}
	}
	if gpA == nil {
		t.Fatalf("exampledispatch provider not found in %+v", unitA.Providers)
	}

	params, err := marshalJSON(map[string]string{
		"target_word":     "exampledispatchpeer", // an OUT-OF-PROCESS peer → nested-broker dispatch
		"build_candy_dir": buildDir,
		"build_name":      "plugin-example-kind",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drive A WITH a reverse channel (ShellExecutor) so the broker serves the F10 RPCs.
	res, err := gpA.InvokeWithExecutor(ctx, &Operation{Reserved: "exampledispatch", Op: OpRun, Params: params}, kit.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		t.Fatalf("InvokeWithExecutor A: %v", err)
	}
	var out struct {
		ProviderResult json.RawMessage `json:"provider_result"`
		BuildResult    json.RawMessage `json:"build_result"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("decode A reply: %v (raw %s)", err, res.JSON)
	}

	// (1) plugin↔plugin: A reached the OUT-OF-PROCESS peer via InvokeProvider over the nested broker.
	if !strings.Contains(string(out.ProviderResult), "peer-reached") {
		t.Fatalf("InvokeProvider did not reach the OOP peer over the nested broker: %s", out.ProviderResult)
	}

	// (2) host-build: A got a built binary path back, and it exists on the host.
	var br struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out.BuildResult, &br); err != nil || br.Path == "" {
		t.Fatalf("HostBuild returned no path: %s", out.BuildResult)
	}
	if _, err := os.Stat(br.Path); err != nil {
		t.Fatalf("host-built binary missing at %s: %v", br.Path, err)
	}
}
