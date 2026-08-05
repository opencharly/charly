package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/phase"
)

// preflight_phase.go — the PREFLIGHT-PHASE pre-pass (K5 seam-death of charly/main_freshness.go),
// invoked from main.go right after Kong parses the command line and BEFORE dispatching to ANY
// command. Mirrors bootstrap_phase.go's runBootstrapPhase shape exactly (enumerate
// providersInPhase, Invoke each with the phase's dedicated Op), except this is a HARD GATE (a
// refusing reply prints its message and exits 1) rather than a data transform. candy/plugin-doctor's
// verb:freshness-guard capability is the sole PhasePreflight provider today (freshness.go).

// preflightGuardInput / preflightGuardReply mirror candy/plugin-doctor/freshness.go's
// freshnessGuardInput/freshnessGuardReply — a process-boundary wire-shape copy, not a new pattern
// (see sdk/deploykit/credential_executor.go's header for the established convention).
type preflightGuardInput struct {
	VerbPath string `json:"verb_path"`
	Version  string `json:"version"`
}

type preflightGuardReply struct {
	Refuse  bool   `json:"refuse,omitempty"`
	Message string `json:"message,omitempty"`
}

// runPreflightPhase invokes every PhasePreflight provider's OpPreflight with the parsed verb path
// + the binary's stamped version — the two facts a compiled-in preflight plugin cannot compute
// itself (everything else it can read directly, being in-process: os.Args, os.Getwd,
// os.Executable, the filesystem). The FIRST refusing reply prints its message to stderr and exits
// 1; a provider that errors or returns no reply is skipped (best-effort, mirroring
// runBootstrapPhaseWith — a broken preflight plugin must never itself become the reason `charly
// version` stops working).
func runPreflightPhase(verbPath string) {
	runPreflightPhaseWith(verbPath, providerRegistry.providersInPhase(phase.PhasePreflight))
}

// runPreflightPhaseWith is the injectable core of runPreflightPhase — split out so a test can
// drive a fixed provider list without registering into the global registry.
func runPreflightPhaseWith(verbPath string, providers []Provider) {
	params, err := marshalJSON(preflightGuardInput{VerbPath: verbPath, Version: CharlyVersion()})
	if err != nil {
		return
	}
	for _, p := range providers {
		res, err := p.Invoke(context.Background(), &Operation{Reserved: p.Reserved(), Op: ops.OpPreflight, Params: params})
		if err != nil || res == nil || len(res.JSON) == 0 {
			continue
		}
		var reply preflightGuardReply
		if json.Unmarshal(res.JSON, &reply) == nil && reply.Refuse {
			fmt.Fprint(os.Stderr, reply.Message)
			os.Exit(1)
		}
	}
}
