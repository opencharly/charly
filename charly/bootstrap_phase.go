package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/charly/charly/plugin/sdk"
)

// runBootstrapPhase invokes every PhaseBootstrap provider's OpBootstrap on the raw project config
// bytes (F9), BEFORE config validation/migration, threading each provider's returned (possibly
// transformed) bytes to the next: a bootstrap-phase plugin runs before the schema gate accepts the
// config and MAY rewrite the raw root bytes (the kernel never needs a validated config to run a
// bootstrap transform). Today only the no-op candy/plugin-example-bootstrap registers in this phase
// (it demonstrates the hook). The migrate chain is NOT a bootstrap plugin: it is verb:migrate over
// OpRun, invoked explicitly by `charly migrate` + remote-cache auto-migration, and the load gate
// keeps the `Run: charly migrate` reject for a stale config — the chain is whole-project file-based
// + host-coupled (it needs host-prelifted inputs from a completed LoadUnified) and so cannot run on
// the root bytes alone inside this phase. LoadUnified seeds the returned bytes into
// loadUnifiedInto via the `fileOverrides` map (keyed on the root's abs path), so the rewrite reaches
// the actual PARSE + the post-merge gate — not just the early version gate. Bootstrap providers are
// COMPILED-IN (in-proc — no validated config exists yet to discover an out-of-process source), so
// this NEVER re-enters the validated-config load (the F4 connect-cycle hazard avoided by
// construction). A no-op bootstrap plugin returns the bytes unchanged. Returns the bytes after all
// bootstrap transforms (the input unchanged when no bootstrap plugin is registered).
func runBootstrapPhase(data []byte) []byte {
	return runBootstrapPhaseWith(data, providerRegistry.providersInPhase(sdk.PhaseBootstrap))
}

// runBootstrapPhaseWith is the injectable core of runBootstrapPhase — it invokes each given
// bootstrap provider's OpBootstrap on data, threading transforms. Split out so a test can drive a
// fixed provider list WITHOUT registering into the global registry (which would make the hot-path
// runBootstrapPhase transform every other test's config).
func runBootstrapPhaseWith(data []byte, providers []Provider) []byte {
	for _, p := range providers {
		params, err := marshalJSON(map[string]string{"config": string(data)})
		if err != nil {
			continue
		}
		res, err := p.Invoke(context.Background(), &Operation{Reserved: p.Reserved(), Op: sdk.OpBootstrap, Params: params})
		if err != nil || res == nil || len(res.JSON) == 0 {
			continue
		}
		var reply struct {
			Config string `json:"config"`
		}
		if json.Unmarshal(res.JSON, &reply) == nil && reply.Config != "" {
			data = []byte(reply.Config)
		}
	}
	return data
}
