package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// TestExternalStepKind_EndToEnd proves the FULL F3 external-step-kind path END-TO-END on real
// code: the reference class:step plugin (candy/plugin-example-stepkind) is host-built + served
// OUT-OF-PROCESS, its DECLARED StepContract is decoded from Describe (buildUnit), an external
// step ("external:examplestepkind") round-trips through the opaque step view, and the host's
// OPEN DEFAULT ARM in RunHostStep dispatches it (executeExternalStep → OpExecute over the E3b
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

	srcDir, err := filepath.Abs("../candy/plugin-example-stepkind")
	if err != nil {
		t.Fatal(err)
	}
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
	carrier, ok := unit.Providers[0].(stepContractCarrier)
	if !ok {
		t.Fatalf("provider %T does not carry a step contract", unit.Providers[0])
	}
	sc, ok := carrier.declaredStepContract()
	if !ok || sc.Scope != ScopeUser || sc.Venue != VenueHostNative || sc.Gate != GateNone {
		t.Fatalf("declared contract = %+v ok=%v, want {ScopeUser, VenueHostNative, GateNone}", sc, ok)
	}
	// F-STEP-EMIT: the plugin DECLARES it produces a build-context fragment (Emits=true), so the
	// pod-overlay OCITarget open external-step arm bakes it (proven below).
	if !sc.Emits {
		t.Fatalf("declared contract Emits = false, want true (F-STEP-EMIT build leg)")
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "f3-stepkind-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}

	// The compileActOp ROUTING seam: a `run: plugin: examplestepkind` op whose provider is a
	// class:step grpcProvider declaring a StepContract lowers to an externalStep carrying the
	// DECLARED contract + the opaque payload (NOT an OpStep / ExternalPluginStep). Using the
	// compiled step (not a hand-built one) proves the FULL authoring → compile → wire path.
	op := &Op{Plugin: "examplestepkind", PluginInput: map[string]any{"marker": "EXTERNAL-STEPKIND-E2E"}}
	routed := compileActOp(op, &Candy{Name: "plugin-example-stepkind"}, &ResolvedBox{Tags: []string{"fedora"}})
	step, ok := routed.(*externalStep)
	if !ok {
		t.Fatalf("compileActOp routed a class:step plugin to %T, want *externalStep", routed)
	}
	if step.Word != "examplestepkind" || step.ScopeV != ScopeUser || step.VenueV != VenueHostNative || step.GateV != GateNone {
		t.Fatalf("externalStep contract = {%q, %v, %v, %v}, want {examplestepkind, ScopeUser, VenueHostNative, GateNone}", step.Word, step.ScopeV, step.VenueV, step.GateV)
	}

	// F-STEP-EMIT BUILD leg: the pod-overlay OCITarget open external-step arm resolves the
	// class:step provider by the trimmed word, sees Emits=true, Invokes its OpEmit over the wire,
	// and splices the returned Containerfile fragment — baking the persistent build marker with
	// the opaque payload's value (proving the Payload round-trips through OpEmit too, and that a
	// step kind with an EmitOCI fragment can be EXTERNALIZED — the one addition C1 needs).
	tgt := &OCITarget{Box: &ResolvedBox{Name: "check-stepkind", Tags: []string{"fedora"}}}
	if err := tgt.emitStep(step, &InstallPlan{Box: "check-stepkind"}); err != nil {
		t.Fatalf("OCITarget.emitStep(external:examplestepkind): %v", err)
	}
	frag := tgt.String()
	if !strings.Contains(frag, "/etc/examplestepkind-build-baked") || !strings.Contains(frag, "EXTERNAL-STEPKIND-E2E") {
		t.Fatalf("baked fragment = %q, want a RUN baking /etc/examplestepkind-build-baked with the payload marker EXTERNAL-STEPKIND-E2E", frag)
	}

	// Project it to the OPAQUE view (Kind "external:<word>" + Payload).
	view := stepToView(step)
	if view.Kind != "external:examplestepkind" {
		t.Fatalf("view.Kind = %q, want external:examplestepkind", view.Kind)
	}
	stepJSON, err := marshalJSON(view)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll("/tmp/charly-examplestepkind") })

	// Walk it via the host's RunHostStep OPEN DEFAULT ARM (stepFromView rebuilds the externalStep
	// from the carried contract + Payload; executeExternalStep dispatches OpExecute to the plugin).
	srv := &executorReverseServer{exec: ShellExecutor{}}
	reply, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON})
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("RunHostStep reply error: %s", reply.GetError())
	}

	// The external step executed on the real venue — the marker is present, carrying the
	// opaque payload's value (proving the Payload round-tripped to the plugin's OpExecute).
	got, err := os.ReadFile("/tmp/charly-examplestepkind/marker")
	if err != nil {
		t.Fatalf("external step did not write the venue marker: %v", err)
	}
	if string(got) != "EXTERNAL-STEPKIND-E2E\n" {
		t.Fatalf("marker = %q, want the opaque payload value round-tripped", got)
	}

	// The dynamic teardown op rode the reply (record-and-replay — externalStep.Reverse() is
	// populated from the plugin's OpExecute DeployReply, never compiled-in).
	var ops []spec.ReverseOp
	if err := json.Unmarshal(reply.GetReverseOpsJson(), &ops); err != nil {
		t.Fatalf("decode reverse ops: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("external step recorded no teardown op (record-and-replay broken)")
	}
}

// TestStepEmitHostBuilder proves the F-STEP-EMIT "step-emit" HostBuild seam — the generic
// host-builder a HOST-COUPLED step kind's OpEmit calls back to for a fragment the host build ENGINE
// renders in-core (the seam C1.2 registered the system-packages per-word emitter into). The test
// registers a fixture emitter under a test-only word to exercise the generic by-word dispatch in
// isolation, drives hostBuildStepEmit by word, and asserts an unregistered word + the registry-level
// "step-emit" host-builder registration.
func TestStepEmitHostBuilder(t *testing.T) {
	// The "step-emit" host-builder is registered on the F10 hostBuilders seam at init. Drive it
	// through the registered wire-level hostBuilder so the test exercises the typedHostBuilder
	// adapter (JSON decode → typed fn → marshal) alongside the by-word dispatch.
	stepEmit, ok := hostBuilderFor("step-emit")
	if !ok {
		t.Fatal("hostBuilderFor(\"step-emit\") = false, want the step-emit host-builder registered")
	}

	const word = "test-stepemit-fixture"
	// Register a fixture in-core emitter under a test-only word (the real registry also holds the
	// C1.2 system-packages emitter; this fixture exercises the generic by-word dispatch in isolation).
	stepEmitters[word] = func(req spec.StepEmitRequest, _ buildEngineContext) (string, error) {
		return "RUN echo host-coupled-" + string(req.Payload) + " > /etc/step-emit-fixture\n", nil
	}
	t.Cleanup(func() { delete(stepEmitters, word) })

	// Dispatch by word through the host-builder: the fragment comes back in an EmitReply.
	reqJSON, err := marshalJSON(spec.StepEmitRequest{Word: word, Payload: []byte(`{"marker":"m"}`), Distros: []string{"fedora"}})
	if err != nil {
		t.Fatal(err)
	}
	resJSON, err := stepEmit(context.Background(), reqJSON, buildEngineContext{})
	if err != nil {
		t.Fatalf("step-emit host-build: %v", err)
	}
	var reply spec.EmitReply
	if err := json.Unmarshal(resJSON, &reply); err != nil {
		t.Fatalf("decode EmitReply: %v", err)
	}
	if !strings.Contains(reply.Fragment, "/etc/step-emit-fixture") {
		t.Fatalf("fragment = %q, want the fixture emitter's rendered RUN", reply.Fragment)
	}

	// An UNREGISTERED step word is a LOUD error (never a silent empty bake, R4).
	bad, err := marshalJSON(spec.StepEmitRequest{Word: "no-such-step-emitter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stepEmit(context.Background(), bad, buildEngineContext{}); err == nil {
		t.Fatal("step-emit host-build for an unregistered word = nil error, want a loud failure")
	}
}
