package main

import (
	"context"
	"testing"

	"github.com/opencharly/spec/checkstep"
	"github.com/opencharly/spec/spec"
)

// fakeCaptureVerb is a host-coupled check verb whose PASS verdict CAPTURES a value — the
// exact shape candy/plugin-command's PASS arm produces (a step's stdout) and that the
// `check-task` bed's task-output-captured step reads back through `charly task --output`.
type fakeCaptureVerb struct{ captured string }

var _ spec.CheckVerbProvider = fakeCaptureVerb{}

func (f fakeCaptureVerb) Reserved() string { return "captureprobe" }

func (f fakeCaptureVerb) RunVerb(_ context.Context, _ spec.CheckContext, _ *spec.Op) spec.CheckVerbResult {
	return spec.CheckVerbResult{Status: spec.StatusPass, Message: "exit=0", CapturedValue: f.captured}
}

// TestKitVerbAdapterCarriesCapturedValue proves the adapter does not drop the verdict's
// CapturedValue while building charly's result. Every dispatch path converts a verb's
// spec.CheckVerbResult into spec.CheckResult, and before that conversion was funnelled
// through checkResultFromVerb each path hand-built the literal with Op/Verb/Status/Message
// only — so a producer's capture, the entire reason the field exists and the only thing
// `charly task --output` prints, reached the ledger as "". This test fails (captured_value
// "") against the old literal.
func TestKitVerbAdapterCarriesCapturedValue(t *testing.T) {
	a := kitVerbAdapter{kv: fakeCaptureVerb{captured: "TASK-SMOKE-OK\n"}}
	op := &spec.Op{Plugin: "command"}

	got := a.RunVerb(context.Background(), nil, op)

	if got.CapturedValue != "TASK-SMOKE-OK\n" {
		t.Fatalf("captured_value = %q, want the verdict's capture carried through", got.CapturedValue)
	}
	if got.Op != op {
		t.Fatalf("Op = %v, want the verb's own op pointer preserved", got.Op)
	}
	if got.Verb != "captureprobe" || got.Status != spec.StatusPass || got.Message != "exit=0" {
		t.Fatalf("got (verb=%q status=%v message=%q), want the verdict's own word/status/message",
			got.Verb, got.Status, got.Message)
	}
}

// TestCheckResultFromVerbKeepsACapturelessVerdictEmpty pins the other half of the contract:
// the conversion states NO capture policy of its own. A verdict that captured nothing (the
// FAIL/SKIP arms in candy/plugin-command leave it empty on purpose — a failed step's output
// was never vouched for) yields exactly the pre-extension result: no placeholder, no
// inherited value from a previous step.
func TestCheckResultFromVerbKeepsACapturelessVerdictEmpty(t *testing.T) {
	op := &spec.Op{Plugin: "command"}
	got := checkResultFromVerb(op, "command", spec.CheckVerbResult{Status: spec.StatusFail, Message: "exit=1"})

	if got.CapturedValue != "" {
		t.Fatalf("captured_value = %q, want empty for a captureless verdict", got.CapturedValue)
	}
	if got.Op != op || got.Verb != "command" || got.Status != spec.StatusFail || got.Message != "exit=1" {
		t.Fatalf("conversion dropped or altered a field: %#v", got)
	}
}

// fakeStepProvider records what the kit adapter delegates to it. The C7 cutover moved the
// typed-step kind mapping + materialization OUT of core into the candy's
// checkstep.StepProvider; kitVerbActStepAdapter must therefore call the provider's
// StepKind() and MaterializeStep() VERBATIM — no per-kind switch, no core-side rebuild.
type fakeStepProvider struct {
	kind spec.StepKind
	step spec.InstallStep

	gotOp     *spec.Op
	gotRunAs  string
	gotCandy  string
	gotFormat string
	gotTags   []string
}

var _ checkstep.StepProvider = (*fakeStepProvider)(nil)

func (f *fakeStepProvider) StepKind() spec.StepKind { return f.kind }

func (f *fakeStepProvider) MaterializeStep(op *spec.Op, runAsUser, candyName, pkgFormat string, distroTags []string) spec.InstallStep {
	f.gotOp, f.gotRunAs, f.gotCandy, f.gotFormat, f.gotTags = op, runAsUser, candyName, pkgFormat, distroTags
	return f.step
}

// TestKitVerbActStepAdapterDelegates proves the core adapter holds NO per-kind switch: it
// returns the provider's StepKind() unchanged and forwards the op + the four host-resolved
// ctx scalars to the provider's MaterializeStep(), returning its step. It fails without the
// C7 delegation (against the old interface the fake does not satisfy StepProvider and the
// adapter mapped the kind via kitStepKindToCharly + rebuilt the step via materializeStep).
func TestKitVerbActStepAdapterDelegates(t *testing.T) {
	want := &spec.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall, Packages: []string{"bash"}}
	f := &fakeStepProvider{kind: spec.StepKindSystemPackages, step: want}
	a := kitVerbActStepAdapter{sp: f}

	if got := a.LowersTo(); got != spec.StepKindSystemPackages {
		t.Fatalf("LowersTo = %v, want the provider's StepKind %v (no core mapping)", got, spec.StepKindSystemPackages)
	}

	op := &spec.Op{Plugin: "package", PluginInput: map[string]any{"package": "bash"}}
	got := a.ConstructStep(op, stepConstructCtx{
		RunAsUser:  "1000",
		CandyName:  "net",
		PkgFormat:  "rpm",
		DistroTags: []string{"fedora:43", "fedora"},
	})
	if got != want {
		t.Fatalf("ConstructStep returned %#v, want the provider's MaterializeStep result", got)
	}
	if f.gotOp != op || f.gotRunAs != "1000" || f.gotCandy != "net" || f.gotFormat != "rpm" ||
		len(f.gotTags) != 2 || f.gotTags[0] != "fedora:43" || f.gotTags[1] != "fedora" {
		t.Fatalf("MaterializeStep got (op=%v runAs=%q candy=%q fmt=%q tags=%v), want the op + the 4 ctx scalars verbatim",
			f.gotOp, f.gotRunAs, f.gotCandy, f.gotFormat, f.gotTags)
	}
}
