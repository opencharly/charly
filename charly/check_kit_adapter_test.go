package main

import (
	"testing"

	"github.com/opencharly/spec/checkstep"
	"github.com/opencharly/spec/spec"
)

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
