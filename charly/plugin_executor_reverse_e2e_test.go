package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencharly/spec/spec"
)

// testLedgerPaths / testComputeDeployID / testReadDeployRecord / testReadCandyRecord are thin
// local ports of sdk/kit's ledger-path shape + install_ledger.go's tiny stdlib-only readers
// (json.Unmarshal off a deterministic path — no engine behavior, plain file I/O) + sdk/deploykit's
// ComputeDeployID (a pure sha256 hash, stdlib only) — this test's ASSERTION TAIL reads back what
// the real reverse-channel deploy plugin wrote to a temp ledger dir; the ledger record TYPES
// (spec.DeployRecord/spec.CandyRecord) are already spec-native.
type testLedgerPaths struct {
	Root    string
	Deploys string
	Candies string
}

func testComputeDeployID(box string, layers, addCandies []string) string {
	h := sha256.New()
	h.Write([]byte(box))
	h.Write([]byte{0})
	for _, l := range layers {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	h.Write([]byte{1})
	for _, l := range addCandies {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func testReadDeployRecord(paths *testLedgerPaths, id string) (*spec.DeployRecord, error) {
	data, err := os.ReadFile(filepath.Join(paths.Deploys, id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec spec.DeployRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func testReadCandyRecord(paths *testLedgerPaths, layer string) (*spec.CandyRecord, error) {
	data, err := os.ReadFile(filepath.Join(paths.Candies, layer+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec spec.CandyRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// TestExternalDeployPlugin_ReverseChannelEndToEnd proves the FULL external deploy
// LIFECYCLE END-TO-END on real code over the E3b reverse channel: the reference
// external DEPLOY plugin (candy/plugin-example-deploy, its own Go module) is
// host-built and served OUT-OF-PROCESS over go-plugin gRPC (LocalTransport, which
// carries the GRPCBroker), routed through ResolveTarget → pluginDeployTarget (S3b —
// the thin data-only proxy dispatching to candy/plugin-fleet's Invoke(OpDeployDispatch),
// which in turn reaches the substrate provider via its own sdk.Executor.InvokeProvider),
// and driven through Add → Update → Del:
//
//   - Add Invokes the provider (ops.OpExecute) with the host's ExecutorService on the
//     broker; the plugin dials back through the SDK (ExecutorFromInvoke) and writes
//     TWO markers on the host venue, then RETURNS a DeployReply whose plugin-script
//     reverse op + record candy/plugin-fleet persists in the (temp) install ledger;
//   - Update re-Invokes idempotently (markers stay, reverse op not duplicated);
//   - Del replays the RECORDED plugin-script reverse op (markers gone, records deleted).
//
// A Test step (a host-side `file` check against the probe marker, no plugin call) used to sit
// between Add and Update — UnifiedDeployTarget.Test is DELETED (#55 W3 B3 remainder, zero real
// callers anywhere in the tree); it never exercised the reverse channel this test proves anyway
// (its own former comment said so — "no plugin call"), so removing it costs this test nothing.
//
// Builds + execs a real binary, so it is gated behind -short exactly like
// TestExternalPluginEndToEnd.
func TestExternalDeployPlugin_ReverseChannelEndToEnd(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir := pluginModuleDir(t, "example-deploy")
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("external deploy plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-deploy-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	// 2. Connect OUT-OF-PROCESS via LocalTransport — the connection carries the broker.
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassDeployTarget || unit.Providers[0].Reserved() != "exampledeploy" {
		t.Fatalf("providers = %+v, want exactly one deploy:exampledeploy", unit.Providers)
	}
	if _, ok := unit.Providers[0].(*grpcProvider); !ok {
		t.Fatalf("provider is %T, want *grpcProvider (the broker-carrying out-of-proc peer)", unit.Providers[0])
	}

	// E3-deploy consumer: register the external provider and confirm ResolveTarget routes
	// target=exampledeploy to the pluginDeployTarget adapter (S3b).
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "e3deploy-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	routed, err := ResolveTarget(&spec.FleetNode{Target: "exampledeploy"}, "e3deploy")
	if err != nil {
		t.Fatalf("ResolveTarget(external deploy): %v", err)
	}
	tgt, ok := routed.(*pluginDeployTarget)
	if !ok {
		t.Fatalf("ResolveTarget routed external deploy to %T, want *pluginDeployTarget", routed)
	}

	// 3. A real lifecycle target: a unique deploy name (so the /tmp scratch dir is private to
	//    this run) and a TEMP ledger (never the operator's). K-wave W3a A9 deleted
	//    pluginDeployTarget's ledgerRoot field + its dispatch()-time req.LedgerRoot threading (it
	//    had zero production callers — test-only scaffolding). candy/plugin-fleet's
	//    ledgerPathsFor falls back to kit.DefaultLedgerPaths() — os.UserHomeDir()-anchored — for
	//    ANY req.LedgerRoot=="" call, exactly what Add/Update/Del's own internal dispatch()
	//    requests always carry (no method threads a ledger override), so redirecting HOME for the
	//    test's duration achieves the SAME temp-ledger isolation through the plugin's EXISTING
	//    default-resolution path, with no wire field needed.
	name := fmt.Sprintf("e3deploy-%d", time.Now().UnixNano())
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".config", "opencharly", "installed")
	paths := &testLedgerPaths{
		Root:    root,
		Deploys: filepath.Join(root, "deploys"),
		Candies: filepath.Join(root, "layers"),
	}
	tgt.name = name

	dir := filepath.Join("/tmp", "charly-exampledeploy", name)
	applied := filepath.Join(dir, "applied")
	probe := filepath.Join(dir, "probe")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// --- Add: reverse channel applies both markers; candy/plugin-fleet records the ledger. ---
	if err := tgt.Add(ctx, nil, nil, spec.EmitOpts{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	mustExist(t, applied, "Add did not write the applied marker over the reverse channel")
	mustExist(t, probe, "Add did not write the probe marker over the reverse channel")
	deployID := testComputeDeployID(name, nil, nil)
	rec, err := testReadDeployRecord(paths, deployID)
	if err != nil || rec == nil {
		t.Fatalf("Add did not write the deploy record: rec=%v err=%v", rec, err)
	}
	if rec.Target != "exampledeploy" {
		t.Fatalf("deploy record target = %q, want %q (must NOT be \"host\" — would collide with the local deploy target.Del's scan)", rec.Target, "exampledeploy")
	}
	crec, err := testReadCandyRecord(paths, "plugin-example-deploy")
	if err != nil || crec == nil {
		t.Fatalf("Add did not write the candy record: crec=%v err=%v", crec, err)
	}
	if len(crec.ReverseOps) != 1 || crec.ReverseOps[0].Kind != spec.ReverseOpPluginScript {
		t.Fatalf("candy record reverse ops = %+v, want exactly one plugin-script op", crec.ReverseOps)
	}

	// --- Update: idempotent re-apply — markers stay, reverse op NOT duplicated. ---
	if err := tgt.Update(ctx, nil, spec.UpdateOpts{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mustExist(t, probe, "Update lost the probe marker")
	crec2, err := testReadCandyRecord(paths, "plugin-example-deploy")
	if err != nil || crec2 == nil || len(crec2.ReverseOps) != 1 {
		t.Fatalf("Update must keep exactly ONE reverse op (idempotent), got %+v err=%v", crec2, err)
	}

	// --- Del: replays the recorded plugin-script reverse op — markers gone, records deleted. ---
	if err := tgt.Del(ctx, spec.DeployTargetDelOpts{}); err != nil {
		t.Fatalf("Del: %v", err)
	}
	mustNotExist(t, probe, "Del did not remove the probe marker (reverse op not replayed)")
	mustNotExist(t, applied, "Del did not remove the applied marker (reverse op not replayed)")
	if rec, _ := testReadDeployRecord(paths, deployID); rec != nil {
		t.Fatal("Del did not delete the deploy record")
	}
	if crec, _ := testReadCandyRecord(paths, "plugin-example-deploy"); crec != nil {
		t.Fatal("Del did not delete the candy record")
	}
}

func mustExist(t *testing.T, path, msg string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: stat %s: %v", msg, path, err)
	}
}

func mustNotExist(t *testing.T, path, msg string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: %s still present (err=%v)", msg, path, err)
	}
}
