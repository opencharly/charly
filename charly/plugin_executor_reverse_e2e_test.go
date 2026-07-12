package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExternalDeployPlugin_ReverseChannelEndToEnd proves the FULL external deploy
// LIFECYCLE END-TO-END on real code over the E3b reverse channel: the reference
// external DEPLOY plugin (candy/plugin-example-deploy, its own Go module) is
// host-built and served OUT-OF-PROCESS over go-plugin gRPC (LocalTransport, which
// carries the GRPCBroker), routed through ResolveTarget to the externalDeployTarget
// adapter, and driven through Add → Test → Update → Del:
//
//   - Add Invokes the provider (OpExecute) with the host's ExecutorService on the
//     broker; the plugin dials back through the SDK (ExecutorFromInvoke) and writes
//     TWO markers on the host venue, then RETURNS a DeployReply whose plugin-script
//     reverse op + record the host persists in the (temp) install ledger;
//   - Test runs a host-side `file` check against the probe marker (no plugin call);
//   - Update re-Invokes idempotently (markers stay, reverse op not duplicated);
//   - Del replays the RECORDED plugin-script reverse op (markers gone, records deleted).
//
// Builds + execs a real binary, so it is gated behind -short exactly like
// TestExternalPluginEndToEnd.
func TestExternalDeployPlugin_ReverseChannelEndToEnd(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-deploy")
	if err != nil {
		t.Fatal(err)
	}
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
	gp, ok := unit.Providers[0].(*grpcProvider)
	if !ok {
		t.Fatalf("provider is %T, want *grpcProvider (the broker-carrying out-of-proc peer)", unit.Providers[0])
	}

	// E3-deploy consumer: register the external provider and confirm ResolveTarget
	// routes target=exampledeploy to the externalDeployTarget adapter.
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "e3deploy-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	routed, err := ResolveTarget(&BundleNode{Target: "exampledeploy"}, "e3deploy")
	if err != nil {
		t.Fatalf("ResolveTarget(external deploy): %v", err)
	}
	if _, ok := routed.(*externalDeployTarget); !ok {
		t.Fatalf("ResolveTarget routed external deploy to %T, want *externalDeployTarget", routed)
	}

	// 3. A real lifecycle target: a unique deploy name (so the /tmp scratch dir is
	//    private to this run), a TEMP ledger (never the operator's), and the local
	//    ShellExecutor (RunUser → bash -lc, no sudo) so the plugin's marker write +
	//    the recorded teardown run for real without a sudo prompt.
	name := fmt.Sprintf("e3deploy-%d", time.Now().UnixNano())
	root := t.TempDir()
	paths := &LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
	tgt := &externalDeployTarget{name: name, prov: gp, exec: ShellExecutor{}, paths: paths}

	dir := filepath.Join("/tmp", "charly-exampledeploy", name)
	applied := filepath.Join(dir, "applied")
	probe := filepath.Join(dir, "probe")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// --- Add: reverse channel applies both markers; host records the ledger. ---
	if err := tgt.Add(ctx, nil, nil, EmitOpts{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	mustExist(t, applied, "Add did not write the applied marker over the reverse channel")
	mustExist(t, probe, "Add did not write the probe marker over the reverse channel")
	rec, err := ReadDeployRecord(paths, tgt.deployID())
	if err != nil || rec == nil {
		t.Fatalf("Add did not write the deploy record: rec=%v err=%v", rec, err)
	}
	if rec.Target != "exampledeploy" {
		t.Fatalf("deploy record target = %q, want %q (must NOT be \"host\" — would collide with the local deploy target.Del's scan)", rec.Target, "exampledeploy")
	}
	crec, err := ReadCandyRecord(paths, "plugin-example-deploy")
	if err != nil || crec == nil {
		t.Fatalf("Add did not write the candy record: crec=%v err=%v", crec, err)
	}
	if len(crec.ReverseOps) != 1 || crec.ReverseOps[0].Kind != ReverseOpPluginScript {
		t.Fatalf("candy record reverse ops = %+v, want exactly one plugin-script op", crec.ReverseOps)
	}

	// --- Test: host-side file check on the probe marker passes (no plugin call). ---
	if err := tgt.Test(ctx, []Op{{ID: "probe", Plugin: "file", PluginInput: map[string]any{"file": probe, "exists": true}}}, TestOpts{}); err != nil {
		t.Fatalf("Test (probe marker present): %v", err)
	}

	// --- Update: idempotent re-apply — markers stay, reverse op NOT duplicated. ---
	if err := tgt.Update(ctx, nil, UpdateOpts{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mustExist(t, probe, "Update lost the probe marker")
	crec2, err := ReadCandyRecord(paths, "plugin-example-deploy")
	if err != nil || crec2 == nil || len(crec2.ReverseOps) != 1 {
		t.Fatalf("Update must keep exactly ONE reverse op (idempotent), got %+v err=%v", crec2, err)
	}

	// --- Del: replays the recorded plugin-script reverse op — markers gone, records deleted. ---
	if err := tgt.Del(ctx, DelOpts{}); err != nil {
		t.Fatalf("Del: %v", err)
	}
	mustNotExist(t, probe, "Del did not remove the probe marker (reverse op not replayed)")
	mustNotExist(t, applied, "Del did not remove the applied marker (reverse op not replayed)")
	if rec, _ := ReadDeployRecord(paths, tgt.deployID()); rec != nil {
		t.Fatal("Del did not delete the deploy record")
	}
	if crec, _ := ReadCandyRecord(paths, "plugin-example-deploy"); crec != nil {
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
