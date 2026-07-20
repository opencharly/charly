package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestExternalPluginStep_Derivations proves the IR derivations of the new step kind
// (no plugin process needed): Kind/Venue are fixed; Scope follows the resolved user
// (the OpStep rule); Gate is None (operator-authorized); Reverse() is static-nil
// because the teardown ops are recorded DYNAMICALLY from the OpExecute reply.
func TestExternalPluginStep_Derivations(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	rootStep := &deploykit.ExternalPluginStep{Op: &spec.Op{Plugin: "examplestep"}, ResolvedUser: "root"}
	if rootStep.Kind() != spec.StepKindExternalPlugin {
		t.Fatalf("Kind = %q, want %q", rootStep.Kind(), spec.StepKindExternalPlugin)
	}
	if rootStep.Venue() != spec.VenueHostNative {
		t.Fatalf("Venue = %v, want VenueHostNative", rootStep.Venue())
	}
	if rootStep.RequiresGate() != spec.GateNone {
		t.Fatalf("RequiresGate = %v, want GateNone", rootStep.RequiresGate())
	}
	if rootStep.Reverse() != nil {
		t.Fatalf("Reverse = %v, want nil (teardown ops are recorded from the OpExecute reply)", rootStep.Reverse())
	}
	if rootStep.Scope() != spec.ScopeSystem {
		t.Fatalf("Scope(root) = %v, want ScopeSystem", rootStep.Scope())
	}
	userStep := &deploykit.ExternalPluginStep{Op: &spec.Op{Plugin: "examplestep"}, ResolvedUser: "1000:1000"}
	if userStep.Scope() != spec.ScopeUser {
		t.Fatalf("Scope(1000:1000) = %v, want ScopeUser", userStep.Scope())
	}

	// The step kind has a registered StepProvider (the dedicated-builtin bijection).
	if _, ok := stepProviderFor(spec.StepKindExternalPlugin); !ok {
		t.Fatal("StepKindExternalPlugin has no registered StepProvider (registerDedicatedBuiltin not wired)")
	}
}

// TestExternalPluginStep_ReverseChannelEndToEnd proves the STEP DEPLOY-EXECUTE leg
// END-TO-END on real code over the E3b reverse channel: the reference external plugin
// (candy/plugin-example-step, its own Go module) is host-built and served
// OUT-OF-PROCESS over go-plugin gRPC (LocalTransport, which carries the GRPCBroker),
// registered as a verb provider, and exercised as a `run: plugin: examplestep`
// install-timeline step:
//
//   - compileActOp lowers the run-step to an ExternalPluginStep (the routing seam:
//     an external grpcProvider satisfies executorInvoker);
//   - executeExternalPluginStep Invokes the plugin's OpExecute WITH the host's
//     ExecutorService on the broker; the plugin dials back through the SDK
//     (ExecutorFromInvoke) and writes the marker on the host venue, then RETURNS a
//     DeployReply whose plugin-script reverse op the host would record in the ledger;
//   - runReverseOps replays that recorded op (the marker is gone).
//
// Builds + execs a real binary, so it is gated behind -short exactly like
// TestExternalDeployPlugin_ReverseChannelEndToEnd.
func TestExternalPluginStep_ReverseChannelEndToEnd(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-step")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("external step plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-step-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	// 2. Connect OUT-OF-PROCESS via LocalTransport — the connection carries the broker.
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassVerb || unit.Providers[0].Reserved() != "examplestep" {
		t.Fatalf("providers = %+v, want exactly one verb:examplestep", unit.Providers)
	}
	if _, ok := unit.Providers[0].(*grpcProvider); !ok {
		t.Fatalf("provider is %T, want *grpcProvider (the broker-carrying out-of-proc peer)", unit.Providers[0])
	}

	// Register the external verb provider into the GLOBAL registry so both the
	// compileActOp routing seam and executeExternalPluginStep resolve it (the same
	// path loadDeployPlugins sets up at deploy).
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "examplestep-step-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}

	marker := fmt.Sprintf("teststep-%d", time.Now().UnixNano())
	op := &spec.Op{Plugin: "examplestep", PluginInput: map[string]any{"marker": marker}}

	// 3. The routing seam: compileActOp lowers a `run: plugin: examplestep` op whose
	//    provider is an external grpcProvider to an ExternalPluginStep (not an OpStep).
	layer := &Candy{Name: "examplestep-deploy-consumer"}
	img := &buildkit.ResolvedBox{Tags: []string{"fedora"}}
	step := compileActOp(op, layer, img)
	eps, ok := step.(*deploykit.ExternalPluginStep)
	if !ok {
		t.Fatalf("compileActOp routed external plugin verb to %T, want *ExternalPluginStep", step)
	}

	dir := filepath.Join("/tmp", "charly-examplestep", marker)
	applied := filepath.Join(dir, "applied")
	probe := filepath.Join(dir, "probe")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// 4. Execute the step over the E3b reverse channel via the local ShellExecutor
	//    (RunUser → bash -lc, no sudo) so the plugin's marker write runs for real.
	plan := &deploykit.InstallPlan{Candy: layer.Name}
	reply, err := executeExternalPluginStep(ctx, eps, plan, kit.ShellExecutor{}, buildEngineContext{})
	if err != nil {
		t.Fatalf("executeExternalPluginStep: %v", err)
	}
	mustExist(t, applied, "OpExecute did not write the applied marker over the reverse channel")
	mustExist(t, probe, "OpExecute did not write the probe marker over the reverse channel")
	if len(reply.ReverseOps) != 1 || reply.ReverseOps[0].Kind != spec.ReverseOpPluginScript {
		t.Fatalf("reply reverse ops = %+v, want exactly one plugin-script op (recorded for teardown)", reply.ReverseOps)
	}
	if reply.Record.Candy != "plugin-example-step" {
		t.Fatalf("reply record candy = %q, want %q", reply.Record.Candy, "plugin-example-step")
	}

	// 5. Teardown: replaying the recorded plugin-script reverse op removes the markers
	//    (record-and-replay — the `charly bundle del` contract). Local runner (nil
	//    Runner → local bash, user scope, no sudo).
	kit.RunReverseOps(reply.ReverseOps, &hostReverseExec{})
	mustNotExist(t, probe, "reverse op replay did not remove the probe marker")
	mustNotExist(t, applied, "reverse op replay did not remove the applied marker")
}
