package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/exec"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// TestExternalStepKind_EndToEnd proves the FULL F3 external-step-kind path END-TO-END on real
// code: the reference class:step plugin (candy/plugin-example-stepkind) is host-built + served
// OUT-OF-PROCESS, its DECLARED StepContract is decoded from Describe (buildUnit), an external
// step ("external:examplestepkind") round-trips through the opaque step view, and the host's
// OPEN DEFAULT ARM in RunHostStep dispatches it (invokeExternalStep → InvokeProvider → ops.OpExecute over the E3b
// reverse channel) so the plugin writes a marker on the real shell venue and returns a dynamic
// teardown ReverseOp — NO compiled-in case for the word anywhere. Builds + execs a real binary,
// gated behind -short like the other reverse-channel e2es.
//
//nolint:gocyclo // sequential end-to-end scenario; extraction would fragment the narrative
func TestExternalStepKind_EndToEnd(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir := pluginModuleDir(t, "example-stepkind")
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("example step-kind plugin module not found at %s: %v", srcDir, err)
	}

	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-stepkind-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassStep || unit.Providers[0].Reserved() != "examplestepkind" {
		t.Fatalf("providers = %+v, want exactly one step:examplestepkind", unit.Providers)
	}
	// The DECLARED StepContract round-trips from the plugin's Describe through buildUnit.
	carrier, ok := unit.Providers[0].(spec.StepContractCarrier)
	if !ok {
		t.Fatalf("provider %T does not carry a step contract", unit.Providers[0])
	}
	sc, ok := carrier.DeclaredStepContract()
	if !ok || sc.Scope != spec.ScopeUser || sc.Venue != spec.VenueHostNative || sc.Gate != spec.GateNone {
		t.Fatalf("declared contract = %+v ok=%v, want {ScopeUser, VenueHostNative, GateNone}", sc, ok)
	}
	// F-STEP-EMIT: the plugin DECLARES it produces a build-context fragment (Emits=true), so the
	// pod-overlay deploykit.OCITarget open external-step arm bakes it (proven below).
	if !sc.Emits {
		t.Fatalf("declared contract Emits = false, want true (F-STEP-EMIT build leg)")
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "f3-stepkind-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}

	// K5-A item 2 spike proof: DescribeProvider against a REAL out-of-process gRPC-connected
	// provider (not the hand-constructed fake in plugin_dispatch_reverse_describe_test.go) — proves
	// capMeta populated via the ACTUAL Describe/buildUnit pipeline (not a direct struct literal)
	// round-trips through the RPC identically to the plugin's own advertised contract above.
	{
		s := &executorReverseServer{}
		reply, err := s.DescribeProvider(context.Background(), &pb.DescribeProviderRequest{Class: string(ClassStep), Word: "examplestepkind"})
		if err != nil {
			t.Fatalf("DescribeProvider (real out-of-process provider): %v", err)
		}
		if !reply.GetFound() {
			t.Fatal("DescribeProvider: Found = false for a just-registered real provider")
		}
		got := reply.GetStepContract()
		if got == nil {
			t.Fatal("DescribeProvider: StepContract = nil, want the plugin's declared contract")
		}
		if got.GetScope() != sc.Scope.String() || got.GetVenue() != int32(sc.Venue) || got.GetGate() != string(sc.Gate) || got.GetEmits() != sc.Emits {
			t.Fatalf("DescribeProvider StepContract = %+v, want {Scope:%q Venue:%d Gate:%q Emits:%v} (the plugin's OWN declared contract, re-derived via the real connect pipeline)",
				got, sc.Scope.String(), sc.Venue, sc.Gate, sc.Emits)
		}
	}

	// The hostBuildConstructStep ROUTING seam (the "construct-step" HostBuild handler, K5-A
	// item 1): a `run: plugin: examplestepkind` op whose provider is a class:step grpcProvider
	// declaring a StepContract lowers to an externalStep carrying the DECLARED contract + the
	// opaque payload (NOT an OpStep / ExternalPluginStep). Using the compiled step (not a
	// hand-built one) proves the FULL authoring → compile → wire path.
	op := &spec.Op{Plugin: "examplestepkind", PluginInput: map[string]any{"marker": "EXTERNAL-STEPKIND-E2E"}}
	layer := testCandy("plugin-example-stepkind", spec.CandyModel{}, spec.CandyView{})
	img := &spec.BuildResolvedBox{ResolvedBox: spec.ResolvedBox{Tags: []string{"fedora"}}}
	// op.RunAs is unset for this fixture, so the real sdk/deploykit.ResolveUserSpec (already
	// directly unit-tested in sdk/deploykit/tasks_emit_test.go::TestResolveUserSpec) collapses to
	// "0" — this class:step (non-verb) op never reads ResolvedUser anyway (hostBuildConstructStep's
	// class:step branch builds the ExternalStep from CandyName alone), so testResolveRunAsUser's
	// minimal root/0/empty stand-in (candy_test_helpers_test.go) is exact here, not an approximation.
	userDir := testResolveRunAsUser(op.RunAs)
	constructReply, err := hostBuildConstructStep(context.Background(), spec.ConstructStepRequest{
		Op: *op, CandyName: layer.GetName(), CandySourceDir: layer.GetSourceDir(),
		ResolvedUser: userDir, PkgFormat: img.Pkg, DistroTags: img.Tags,
	}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildConstructStep: %v", err)
	}
	if constructReply.Step == nil {
		t.Fatalf("hostBuildConstructStep returned no step — want an externalStep")
	}
	routed, err := spec.StepFromView(*constructReply.Step)
	if err != nil {
		t.Fatalf("StepFromView: %v", err)
	}
	step, ok := routed.(*spec.ExternalStep)
	if !ok {
		t.Fatalf("hostBuildConstructStep routed a class:step plugin to %T, want *externalStep", routed)
	}
	if step.Word != "examplestepkind" || step.ScopeV != spec.ScopeUser || step.VenueV != spec.VenueHostNative || step.GateV != spec.GateNone {
		t.Fatalf("externalStep contract = {%q, %v, %v, %v}, want {examplestepkind, ScopeUser, VenueHostNative, GateNone}", step.Word, step.ScopeV, step.VenueV, step.GateV)
	}

	// F-STEP-EMIT BUILD leg: ociEmitStep's open external-step arm resolves the class:step provider
	// by the trimmed word, sees Emits=true, Invokes its ops.OpEmit over the wire, and splices the
	// returned Containerfile fragment — baking the persistent build marker with the opaque
	// payload's value (proving the Payload round-trips through ops.OpEmit too, and that a step kind
	// with an EmitOCI fragment can be EXTERNALIZED — the one addition C1 needs).
	frag, err := ociEmitStep(step, &spec.InstallPlan{Box: "check-stepkind"}, []string{"fedora"}, buildEngineContext{Box: &spec.ResolvedBox{Name: "check-stepkind", Tags: []string{"fedora"}}})
	if err != nil {
		t.Fatalf("ociEmitStep(external:examplestepkind): %v", err)
	}
	if !strings.Contains(frag, "/etc/examplestepkind-build-baked") || !strings.Contains(frag, "EXTERNAL-STEPKIND-E2E") {
		t.Fatalf("baked fragment = %q, want a RUN baking /etc/examplestepkind-build-baked with the payload marker EXTERNAL-STEPKIND-E2E", frag)
	}

	// Project it to the OPAQUE view (Kind "external:<word>" + Payload).
	view := spec.StepToView(step)
	if view.Kind != "external:examplestepkind" {
		t.Fatalf("view.Kind = %q, want external:examplestepkind", view.Kind)
	}
	stepJSON, err := marshalJSON(view)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll("/tmp/charly-examplestepkind") })

	// Walk it via the host's RunHostStep OPEN DEFAULT ARM (stepFromView rebuilds the externalStep
	// from the carried contract + Payload; invokeExternalStep dispatches ops.OpExecute to the plugin).
	srv := &executorReverseServer{exec: exec.ShellExecutor{}}
	reply, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON})
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("RunHostStep reply error: %s", reply.GetError())
	}

	// The external step executed on the real venue — the marker is present, carrying the
	// opaque payload's value (proving the Payload round-tripped to the plugin's ops.OpExecute).
	got, err := os.ReadFile("/tmp/charly-examplestepkind/marker")
	if err != nil {
		t.Fatalf("external step did not write the venue marker: %v", err)
	}
	if string(got) != "EXTERNAL-STEPKIND-E2E\n" {
		t.Fatalf("marker = %q, want the opaque payload value round-tripped", got)
	}

	// The dynamic teardown op rode the reply (record-and-replay — externalStep.Reverse() is
	// populated from the plugin's ops.OpExecute DeployReply, never compiled-in).
	var ops []spec.ReverseOp
	if err := json.Unmarshal(reply.GetReverseOpsJson(), &ops); err != nil {
		t.Fatalf("decode reverse ops: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("external step recorded no teardown op (record-and-replay broken)")
	}
}

// TestStepEmitHostBuilder proves the F-STEP-EMIT "step-emit" HostBuild seam (K5 seam-death, W4:
// the former by-word `stepEmitters` micro-registry — a word-keyed indirection over exactly ONE
// registered entry — is gone; hostBuildStepEmit now matches "oci-emit-step" directly). Drives
// hostBuildStepEmit by word through the registered wire-level hostBuilder (exercising the
// typedHostBuilder JSON-decode/marshal adapter too) and asserts: the ONE known word ("oci-emit-step")
// reaches stepEmitOCIEmitStep -> dispatchOCIStep -> the compiled-in "oci-dispatch" class:step
// provider (candy/plugin-installstep) — proven by the DISTINCT downstream error dispatchOCIStep's
// peer raises when reconstructing an intentionally-empty step view ("unknown step kind"), a
// different failure than the loud "no in-core emitter registered" any OTHER word still gets.
func TestStepEmitHostBuilder(t *testing.T) {
	// The "step-emit" host-builder is registered on the F10 hostBuilders seam at init.
	stepEmit, ok := hostBuilderFor("step-emit")
	if !ok {
		t.Fatal("hostBuilderFor(\"step-emit\") = false, want the step-emit host-builder registered")
	}

	// "oci-emit-step" is the one word this seam still serves: it must reach stepEmitOCIEmitStep ->
	// dispatchOCIStep -> the "oci-dispatch" provider, proven by the downstream reconstruct-step
	// error (an intentionally-empty StepView has no Kind), never the "no in-core emitter
	// registered" word-mismatch error.
	reqJSON, err := marshalJSON(spec.StepEmitRequest{Word: "oci-emit-step", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stepEmit(context.Background(), reqJSON, buildEngineContext{})
	if err == nil || !strings.Contains(err.Error(), "oci-dispatch") {
		t.Fatalf("step-emit host-build(oci-emit-step) = %v, want an oci-dispatch reconstruct-step error (proves the word dispatched, not a word mismatch)", err)
	}

	// Any OTHER step word is a LOUD error (never a silent empty bake, R4).
	bad, err := marshalJSON(spec.StepEmitRequest{Word: "no-such-step-emitter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stepEmit(context.Background(), bad, buildEngineContext{}); err == nil {
		t.Fatal("step-emit host-build for an unregistered word = nil error, want a loud failure")
	}
}
