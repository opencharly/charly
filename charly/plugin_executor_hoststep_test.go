package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// TestRunHostStep_Dispatch proves the host-engine reverse handler routes each step view to
// the RIGHT in-core fn — the dispatch decision is the NEW seam (the machinery itself is
// proven by the deploy beds). The reverse server holds a fake executor, so no real build /
// package install runs; we assert the DISPATCH decision per kind. All five host-engine kinds
// route to a kind-specific arm; every other (plugin-renderable) kind falls to the loud
// default ("not a host-engine step") — the plugin executes those itself via RunSystem/PutFile.
func TestRunHostStep_Dispatch(t *testing.T) {
	srv := &executorReverseServer{exec: &recordingExec{homeReturn: "/home/test"}}
	ctx := context.Background()

	call := func(t *testing.T, step spec.InstallStep, opts deploykit.EmitOpts) *pb.HostStepReply {
		t.Helper()
		stepJSON, err := json.Marshal(deploykit.StepToView(step))
		if err != nil {
			t.Fatalf("marshal step view: %v", err)
		}
		optsJSON, _ := json.Marshal(opts)
		rep, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON, OptsJson: optsJSON})
		if err != nil {
			t.Fatalf("RunHostStep returned a transport error (should ride reply.Error): %v", err)
		}
		return rep
	}

	t.Run("builder arm", func(t *testing.T) {
		// A builder with no LocalPkg + nil BuilderDef has no host build cell; under
		// --skip-incompatible runVenueBuilderStep returns nil — proving the Builder arm
		// was taken (the default arm would have errored "not a host-engine step").
		rep := call(t, &deploykit.BuilderStep{Builder: "npm", CandyName: "x"}, deploykit.EmitOpts{SkipIncompatible: true})
		if rep.GetError() != "" {
			t.Fatalf("builder arm: unexpected error: %s", rep.GetError())
		}
		var ops []spec.ReverseOp
		if err := json.Unmarshal(rep.GetReverseOpsJson(), &ops); err != nil {
			t.Fatalf("builder arm: reverse ops not decodable: %v", err)
		}
	})

	t.Run("localpkg arm", func(t *testing.T) {
		// A LocalPkgInstallStep with nil LocalPkg is a clean skip in deploykit.ExecLocalPkgInstall —
		// proving the LocalPkgInstall arm was taken (no error, vs the default arm's error).
		rep := call(t, &deploykit.LocalPkgInstallStep{CandyName: "charly", PkgbuildRef: "pkg/arch", Format: "pac"}, deploykit.EmitOpts{})
		if rep.GetError() != "" {
			t.Fatalf("localpkg arm: unexpected error: %s", rep.GetError())
		}
	})

	t.Run("system-packages arm", func(t *testing.T) {
		// A PhaseInstall SystemPackagesStep with packages routes to renderHostPackageCommand;
		// the test server's zero buildEngineContext has no DistroCfg, so the render errors
		// "no distro config for format" — proving the SystemPackages arm was taken (and tried
		// the DistroCfg render), NOT the default "not a host-engine step" arm.
		rep := call(t, &deploykit.SystemPackagesStep{Format: "pac", Phase: spec.PhaseInstall, Packages: []string{"ripgrep"}}, deploykit.EmitOpts{})
		if !strings.Contains(rep.GetError(), "no distro config") {
			t.Fatalf("system-packages arm: want a 'no distro config' render error, got %q", rep.GetError())
		}
	})

	t.Run("act-op arm rejects a plugin-renderable Op", func(t *testing.T) {
		// An OpStep whose verb is NOT a ProvisionActor (here a literal command, which is
		// plugin-renderable) routes to the OpStep arm, which loudly rejects it: a
		// plugin-renderable OpStep must be executed by the plugin via RunSystem/RunUser, not
		// routed to RunHostStep. Proves the OpStep arm was taken (vs the default arm).
		rep := call(t, &deploykit.OpStep{CandyName: "x", Op: &spec.Op{Command: "true"}}, deploykit.EmitOpts{})
		if !strings.Contains(rep.GetError(), "not act-capable") {
			t.Fatalf("act-op arm: want a 'not act-capable' rejection, got %q", rep.GetError())
		}
	})

	t.Run("external-plugin arm", func(t *testing.T) {
		// An ExternalPluginStep whose verb is not connected (no plugin loaded in the unit
		// test) routes to executeExternalPluginStep, which errors "verb is not connected at
		// deploy time" — proving the ExternalPlugin arm was taken (vs the default arm).
		rep := call(t, &deploykit.ExternalPluginStep{CandyName: "x", Op: &spec.Op{Plugin: "examplestep", PluginInput: map[string]any{"marker": "m"}}}, deploykit.EmitOpts{})
		if !strings.Contains(rep.GetError(), "not connected") {
			t.Fatalf("external-plugin arm: want a 'not connected' error, got %q", rep.GetError())
		}
	})

	t.Run("plugin-renderable kind rejected by default arm", func(t *testing.T) {
		// A FileStep IS plugin-renderable — the plugin must execute it itself via PutFile.
		// RunHostStep rejects it loudly via the default arm.
		rep := call(t, &deploykit.FileStep{Source: "/tmp/src", Dest: "/etc/x", CandyName: "x"}, deploykit.EmitOpts{})
		if !strings.Contains(rep.GetError(), "not a host-engine step") {
			t.Fatalf("plugin-renderable kind: want a 'not a host-engine step' error, got %q", rep.GetError())
		}
	})

	t.Run("malformed step view", func(t *testing.T) {
		rep, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: []byte("{not json")})
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if rep.GetError() == "" {
			t.Fatal("malformed step view: want a decode error in reply.Error")
		}
	})
}
