package check

// verb_resolver.go — K1-unblock W3 Unit B spike: a plugin-side kit.VerbResolver backed by
// Executor.InvokeProvider, proving the design hypothesis that let the whole check-gather family
// move without inventing a new "check-run-execute" HostBuild leaf.
//
// RDD finding (code-level, traced through the actual production dispatch before writing this):
// charly/plugin_dispatch_reverse.go's InvokeProvider (the host's reverse-leg handler) ALREADY
// dispatches uniformly to BOTH placements a verb word can have — resolving (class, word) in the
// SAME core-private providerRegistry hostVerbResolver.RunVerb uses today, then either (a) an
// OUT-OF-PROCESS target (cdp/vnc/kube/…): threaded a venue executor over a nested reverse-channel
// broker, exactly like charly/provider_checkenv.go's invokeVerbProvider already does; or (b) an
// IN-PROC/compiled-in target (builtin verbs like command=): a direct Invoke, no broker. The
// caller (this plugin) does not need to know or care which placement a word has — it is EXACTLY
// the same host-side branch hostVerbResolver.RunVerb already takes, just reached from a plugin
// instead of from core. No new wire Op, no new HostBuild leaf.
//
// The ONE new mechanism this design needed — letting THIS plugin's own locally-constructed check
// venue (Unit A's resolveCheckVenue, most commonly a deploykit.ContainerChain single-hop
// NestedExecutor) ride along as InvokeProviderOpts.VenueDescriptor — already landed as its own sdk
// leg (kit.DescriptorFromExecutor's new "container" kind, sdk PR pending its first real caller:
// this file).
//
// Wire shapes mirrored EXACTLY from the two existing production callers of this same dispatch
// (not invented): the op/params/env marshal matches charly/provider_checkenv.go's
// invokeVerbProvider (Reserved=word, Op=sdk.OpRun, Params=marshal(*spec.Op),
// Env=marshal(checkEnvWire)); the env struct shape matches sdk/checkverb.go's private
// checkEnvWire (Box/Instance/Mode/Distros/VenueKind/DialTimeoutNs) — mirrored here rather than
// exported because promoting it to a shared/CUE-sourced type is a follow-up polish item once the
// full six-arm wiring lands, not blocking for this spike.

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// classVerb mirrors charly's core-private ClassVerb ProviderClass ("verb") — a plain string
// constant, not a wire type; InvokeProvider's class param is untyped on the wire.
const classVerb = "verb"

// verbEnvWire mirrors sdk/checkverb.go's private checkEnvWire byte-for-byte (same JSON tags) —
// the established wire shape the host's out-of-process verb-serve path already decodes.
type verbEnvWire struct {
	Box           string   `json:"box"`
	Instance      string   `json:"instance"`
	Mode          string   `json:"mode"`
	Distros       []string `json:"distros"`
	VenueKind     string   `json:"venue_kind"`
	DialTimeoutNs int64    `json:"dial_timeout_ns"`
}

// pluginVerbResolver is a kit.VerbResolver backed by Executor.InvokeProvider — the plugin-side
// counterpart of charly's core-private hostVerbResolver. venueDesc is this plugin's own
// self-resolved check venue (Unit A's resolveCheckVenue), threaded as the S1 VenueDescriptor so
// the host materializes the SAME venue for an out-of-process target without this plugin needing
// its own executor-threaded incoming Invoke.
type pluginVerbResolver struct {
	ex        *sdk.Executor
	ctx       context.Context
	env       verbEnvWire
	venueDesc *spec.VenueDescriptor
}

var _ kit.VerbResolver = (*pluginVerbResolver)(nil)

// RunVerb resolves op's verb word via InvokeProvider and runs it — mirrors
// charly/planrun_adapter.go's hostVerbResolver.RunVerb exactly, just dispatched over the wire
// instead of the in-process registry.
func (r *pluginVerbResolver) RunVerb(ctx context.Context, op *spec.Op) (spec.CheckResult, bool) {
	word, err := op.Kind()
	if err != nil {
		return spec.CheckResult{}, false
	}
	params, err := json.Marshal(op)
	if err != nil {
		return spec.CheckResult{Status: spec.StatusFail, Message: "verb " + word + ": marshal op: " + err.Error()}, true
	}
	envJSON, err := json.Marshal(r.env)
	if err != nil {
		return spec.CheckResult{Status: spec.StatusFail, Message: "verb " + word + ": marshal env: " + err.Error()}, true
	}
	opts := sdk.InvokeProviderOpts{}
	if r.venueDesc != nil {
		opts.VenueDescriptor = r.venueDesc
	}
	resultJSON, err := r.ex.InvokeProvider(ctx, classVerb, word, sdk.OpRun, params, envJSON, opts)
	if err != nil {
		return spec.CheckResult{Status: spec.StatusFail, Message: "verb " + word + ": " + err.Error()}, true
	}
	var res spec.CheckResult
	if len(resultJSON) > 0 {
		if uerr := json.Unmarshal(resultJSON, &res); uerr != nil {
			return spec.CheckResult{Status: spec.StatusFail, Message: "verb " + word + ": decode result: " + uerr.Error()}, true
		}
	}
	return res, true
}

// RunProvisionAct is NOT part of this spike's proven surface — every builtin do:act verb this
// family exercises today runs as a check: step (RunVerb), not a provisioning act. Returning
// (_, false) matches the walk's own documented fallback (falls through to the assert dispatch),
// so this is a safe, honest default rather than a fabricated implementation.
func (r *pluginVerbResolver) RunProvisionAct(ctx context.Context, op *spec.Op, verb string) (spec.CheckResult, bool) {
	return spec.CheckResult{}, false
}
