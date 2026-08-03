package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	specexec "github.com/opencharly/spec/exec"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// testRunReverseOpPluginScript is a thin local port of sdk/kit.RunReverseOps'
// ReverseOpPluginScript case (reversePluginScript + runScriptReverse/runUserShellReverse,
// stdlib-only local exec.Command with no ReverseRunner/DryRun — the "long-standing
// host-teardown path" the real functions document as their own no-runner fallback). This test's
// assertion is the record-and-replay CONTRACT for the ONE reverse-op kind an external plugin
// step ever records (ReverseOpPluginScript) — the other 15 kinds RunReverseOps dispatches are
// substantial per-format teardown logic covered by sdk/kit's own test suite, not duplicated here.
// A kind other than ReverseOpPluginScript is a loud failure (never a silent no-op), so a future
// fixture needing broader coverage is forced to extend this, not silently pass vacuously.
func testRunReverseOpPluginScript(t *testing.T, ops []spec.ReverseOp) {
	t.Helper()
	for _, op := range ops {
		if op.Kind != spec.ReverseOpPluginScript {
			t.Fatalf("testRunReverseOpPluginScript: unsupported reverse-op kind %q (only plugin-script is ported here)", op.Kind)
		}
		script := strings.TrimSpace(op.Extra[spec.ReverseOpPluginScriptKey])
		if script == "" {
			continue
		}
		var cmd *exec.Cmd
		if op.Scope == spec.ScopeUser {
			cmd = exec.Command("bash", "-lc", script)
		} else {
			cmd = exec.Command("sudo", "bash", "-lc", script)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "reverse: %s failed: %v\n", op.Kind, err)
		}
	}
}

// TestExternalPluginStep_Derivations proves the IR derivations of the new step kind
// (no plugin process needed): Kind/Venue are fixed; Scope follows the resolved user
// (the OpStep rule); Gate is None (operator-authorized); Reverse() is static-nil
// because the teardown ops are recorded DYNAMICALLY from the OpExecute reply.
func TestExternalPluginStep_Derivations(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	rootStep := &spec.ExternalPluginStep{Op: &spec.Op{Plugin: "examplestep"}, ResolvedUser: "root"}
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
	userStep := &spec.ExternalPluginStep{Op: &spec.Op{Plugin: "examplestep"}, ResolvedUser: "1000:1000"}
	if userStep.Scope() != spec.ScopeUser {
		t.Fatalf("Scope(1000:1000) = %v, want ScopeUser", userStep.Scope())
	}
	// StepKindExternalPlugin's pod-overlay build-emit is an unconditional Go-level type-switch arm
	// in candy/plugin-installstep's "oci-dispatch" word (K5-A item 2) — it has no ClassStep registry
	// entry to check anymore (the former externalPluginStepProvider/EmitOCI is deleted, R1: zero live
	// callers after the relocation). checkStepProviderBijection (provider_step.go) is the compile-time
	// bijection proof for this kind now; see TestDedicatedProviders_BulkStepResolveAndAbsent.
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
//   - RunHostStep's ExternalPluginStep arm (S4: dispatches via invokeExternalStep →
//     the SAME PLUGIN↔PLUGIN InvokeProvider leg every peer-invoke uses) Invokes the
//     plugin's OpExecute WITH the host's ExecutorService on the broker; the plugin
//     dials back through the SDK (ExecutorFromInvoke) and writes the marker on the
//     host venue, then RETURNS a DeployReply whose plugin-script reverse op rides the
//     RunHostStep reply;
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
	// compileActOp routing seam and RunHostStep's invokeExternalStep (via
	// InvokeProvider) resolve it (the same path loadDeployPlugins sets up at deploy).
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "examplestep-step-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}

	marker := fmt.Sprintf("teststep-%d", time.Now().UnixNano())
	op := &spec.Op{Plugin: "examplestep", PluginInput: map[string]any{"marker": marker}}

	// 3. The routing seam: hostBuildConstructStep (the "construct-step" HostBuild handler,
	//    K5-A item 1) lowers a `run: plugin: examplestep` op whose provider is an external
	//    grpcProvider to an ExternalPluginStep (not an OpStep).
	layer := testCandy("examplestep-deploy-consumer", spec.CandyModel{}, spec.CandyView{})
	img := &spec.BuildResolvedBox{ResolvedBox: spec.ResolvedBox{Tags: []string{"fedora"}}}
	userDir := testResolveRunAsUser(op.RunAs)
	reply, err := hostBuildConstructStep(ctx, spec.ConstructStepRequest{
		Op: *op, CandyName: layer.GetName(), CandySourceDir: layer.GetSourceDir(),
		ResolvedUser: userDir, PkgFormat: img.Pkg, DistroTags: img.Tags,
	}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildConstructStep: %v", err)
	}
	if reply.Step == nil {
		t.Fatalf("hostBuildConstructStep returned no step — want an ExternalPluginStep")
	}
	step, err := spec.StepFromView(*reply.Step)
	if err != nil {
		t.Fatalf("StepFromView: %v", err)
	}
	eps, ok := step.(*spec.ExternalPluginStep)
	if !ok {
		t.Fatalf("hostBuildConstructStep routed external plugin verb to %T, want *ExternalPluginStep", step)
	}

	dir := filepath.Join("/tmp", "charly-examplestep", marker)
	applied := filepath.Join(dir, "applied")
	probe := filepath.Join(dir, "probe")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// 4. Execute the step over the E3b reverse channel via RunHostStep (the real production
	//    entry point a deploy plugin's kit.WalkPlans dials back into), backed by the local
	//    ShellExecutor (RunUser → bash -lc, no sudo) so the plugin's marker write runs for
	//    real. stepJSON round-trips the step through the SAME opaque view the wire carries.
	stepJSON, err := marshalJSON(spec.StepToView(eps))
	if err != nil {
		t.Fatal(err)
	}
	srv := &executorReverseServer{exec: specexec.ShellExecutor{}}
	rep, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON})
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if rep.GetError() != "" {
		t.Fatalf("RunHostStep reply error: %s", rep.GetError())
	}
	mustExist(t, applied, "OpExecute did not write the applied marker over the reverse channel")
	mustExist(t, probe, "OpExecute did not write the probe marker over the reverse channel")
	var reverseOps []spec.ReverseOp
	if err := json.Unmarshal(rep.GetReverseOpsJson(), &reverseOps); err != nil {
		t.Fatalf("decode reverse ops: %v", err)
	}
	if len(reverseOps) != 1 || reverseOps[0].Kind != spec.ReverseOpPluginScript {
		t.Fatalf("reply reverse ops = %+v, want exactly one plugin-script op (recorded for teardown)", reverseOps)
	}

	// 5. Teardown: replaying the recorded plugin-script reverse op removes the markers
	//    (record-and-replay — the `charly bundle del` contract). Local runner (nil
	//    Runner → local bash, user scope, no sudo).
	testRunReverseOpPluginScript(t, reverseOps)
	mustNotExist(t, probe, "reverse op replay did not remove the probe marker")
	mustNotExist(t, applied, "reverse op replay did not remove the applied marker")
}
