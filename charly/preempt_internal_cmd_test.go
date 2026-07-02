package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// TestRenderLeaseTable proves the hidden `charly __preempt-status` FORMATTER (renderLeaseTable)
// surfaces the arbiter's lease ledger — the core-side rendering the externalized
// candy/plugin-preempt shells back to via the in-core proxy. The proxy's Status() DISPATCH to
// verb:arbiter is exercised by the R10 bed (check-preempt-arbiter-pod); here we drive the pure
// formatter with a hand-built ledger, no live plugin needed.
func TestRenderLeaseTable(t *testing.T) {
	// Empty ledger → the "no leases" message.
	var empty bytes.Buffer
	if err := renderLeaseTable(&spec.PreemptLedger{}, nil, &empty); err != nil {
		t.Fatalf("renderLeaseTable (empty): %v", err)
	}
	if got := empty.String(); !strings.Contains(got, "No active preemption leases.") {
		t.Fatalf("empty-ledger render = %q, want the no-leases message", got)
	}

	// One ACTIVE lease → the table renders its claimant + token + preempted holder + state.
	led := &spec.PreemptLedger{Leases: []spec.PreemptLease{{
		Claimant:  "check-gpu-bed",
		Tokens:    []string{"nvidia-gpu"},
		Transient: true,
		Created:   "2026-01-01T00:00:00Z",
		Preempted: []spec.PreemptedHolder{{Addr: spec.HolderAddr{Name: "gpu-workstation"}}},
	}}}
	var buf bytes.Buffer
	if err := renderLeaseTable(led, nil, &buf); err != nil {
		t.Fatalf("renderLeaseTable (seeded): %v", err)
	}
	out := buf.String()
	for _, want := range []string{"check-gpu-bed", "nvidia-gpu", "gpu-workstation", "active"} {
		if !strings.Contains(out, want) {
			t.Fatalf("seeded render missing %q:\n%s", want, out)
		}
	}

	// A STRANDED lease renders the recovery hint.
	var stranded bytes.Buffer
	if err := renderLeaseTable(led, []string{"check-gpu-bed"}, &stranded); err != nil {
		t.Fatalf("renderLeaseTable (stranded): %v", err)
	}
	if !strings.Contains(stranded.String(), "STRANDED") {
		t.Fatalf("stranded lease must render the STRANDED state:\n%s", stranded.String())
	}
}
