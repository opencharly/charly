package main

import (
	"context"
	"testing"
)

// fakePreflightProvider is a minimal Provider stub for runPreflightPhaseWith — no existing
// bootstrap_phase_test.go precedent to mirror (runBootstrapPhaseWith's os.Exit-free data-transform
// shape was never unit-tested either; only its sub-helpers were, all of which moved to
// candy/plugin-doctor's freshness_test.go/freshness_stamped_test.go).
type fakePreflightProvider struct {
	reserved string
	invoked  bool
	reply    string // raw JSON reply; empty -> no reply (Invoke returns nil Result)
	err      error
}

func (f *fakePreflightProvider) Reserved() string     { return f.reserved }
func (f *fakePreflightProvider) Class() ProviderClass { return ClassVerb }
func (f *fakePreflightProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	f.invoked = true
	if op.Op != "preflight" {
		panic("fakePreflightProvider: unexpected op " + op.Op)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.reply == "" {
		return nil, nil
	}
	return &Result{JSON: []byte(f.reply)}, nil
}

// TestRunPreflightPhaseWith_NonRefusingDoesNotExit proves a provider that replies without
// Refuse=true lets the run continue (the process is still alive after the call — this test itself
// completing is the proof; a genuine refuse would os.Exit(1) and fail the whole test binary).
func TestRunPreflightPhaseWith_NonRefusingDoesNotExit(t *testing.T) {
	p := &fakePreflightProvider{reserved: "freshness-guard", reply: `{}`}
	runPreflightPhaseWith("box build foo", []Provider{p})
	if !p.invoked {
		t.Error("provider was not invoked")
	}
}

// TestRunPreflightPhaseWith_ErroringProviderSkipped proves a provider returning an error (or no
// reply) is skipped rather than treated as a refusal — a broken preflight plugin must never itself
// become the reason `charly version` stops working.
func TestRunPreflightPhaseWith_ErroringProviderSkipped(t *testing.T) {
	erroring := &fakePreflightProvider{reserved: "a", err: context.DeadlineExceeded}
	noReply := &fakePreflightProvider{reserved: "b"}
	ok := &fakePreflightProvider{reserved: "c", reply: `{"refuse":false}`}
	runPreflightPhaseWith("version", []Provider{erroring, noReply, ok})
	if !erroring.invoked || !noReply.invoked || !ok.invoked {
		t.Error("every provider in the phase must be invoked")
	}
}
