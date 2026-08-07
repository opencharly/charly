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
// Env=marshal(spec.CheckEnv)). spec.CheckEnv is CUE-sourced (sdk/schema/checkresult.cue's
// #CheckEnv, K1-unblock W3 Unit B) — the ONE generated shape this file, sdk/checkverb.go's
// out-of-process decode, charly/provider_checkenv.go's host-side CheckEnv, and
// charly/plugin_dispatch_reverse.go's InvokeProvider detached-CheckContext construction all
// share (a hand-mirrored per-consumer struct was this design's first draft — corrected before
// the six-arm fan-out multiplied its consumers, per SDD's standing no-hand-written-wire-types
// rule).

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// classVerb mirrors charly's core-private ClassVerb ProviderClass ("verb") — a plain string
// constant, not a wire type; InvokeProvider's class param is untyped on the wire.
const classVerb = "verb"

// pluginVerbResolver is a kit.VerbResolver backed by Executor.InvokeProvider — the plugin-side
// counterpart of charly's core-private hostVerbResolver. kr is the back-reference to the *kit.Runner
// this resolver was installed on (wired by newPluginCheckRunner, mirroring
// charly/checkrun.go's hvr.kr = kr), so RunVerb reads the runner's CURRENT executor — including
// one SwapVenue retargeted mid-plan — on every call, threading it as the S1 VenueDescriptor so the
// host materializes the SAME live venue for an out-of-process target without this plugin needing
// its own executor-threaded incoming Invoke. A STATIC field set once at construction (the former
// design) went stale the instant SwapVenue retargeted the runner for a cross-deployment or
// GROUP-member step — RCA'd live via a nil cc.Exec() crash on a VM target's `command:` step.
//
// #55 W3 (check-cross-pod-cdp bed RCA): that earlier fix derived the EXECUTOR fresh from r.kr on
// every RunVerb call but left `env` a STATIC field, marshalled UNCHANGED into every dispatch's
// wire envelope — the exact same staleness class, just for the Box/Instance/Mode/etc. sibling
// fields instead of Exec. A GROUP bed's runner starts with Box=<group-root-name> (a pure group
// has no container of its own; flattenFleetVenues hard-errors a direct plan step there), and
// EVERY step immediately SwapVenue-retargets Box to its OWNING MEMBER — but the frozen `env` kept
// reporting the group's own bare name. charly/plugin_dispatch_reverse.go's InvokeProvider host
// handler decodes THIS envelope to construct the detached CheckContext serving the verb's
// reverse-leg calls (cc.ResolveEndpoint/ResolveGraphicsEndpoint/ResolveImageLabel), so an
// out-of-process live-container verb (cdp/vnc/dbus/mcp/spice) always resolved the group's own
// (container-less) name instead of the swapped member — "container
// charly-check-cross-pod-cdp is not running" for a bed whose actual cdp subject is the chrome
// member. RunVerb now builds env FRESH from r.kr on every call (pluginSnapshotCheckEnv, mirroring
// charly/provider_checkenv.go's snapshotCheckEnv field-for-field) instead of reusing the
// construction-time snapshot; the constructor-supplied `env` survives only as the defensive
// fallback for the (never-hit-in-practice) r.kr == nil case, matching the Exec-derivation block's
// own existing nil-guard style.
type pluginVerbResolver struct {
	ex  *sdk.Executor
	ctx context.Context
	env spec.CheckEnv
	kr  *kit.Runner
}

// pluginSnapshotCheckEnv builds a spec.CheckEnv from kr's CURRENT live state — the plugin-side
// mirror of charly/provider_checkenv.go's snapshotCheckEnv (that function's own doc comment
// explains each field's derivation; kept in exact field-for-field lockstep here, R3).
func pluginSnapshotCheckEnv(kr *kit.Runner) spec.CheckEnv {
	ce := spec.CheckEnv{
		Box:           kr.VmTargetName(),
		Instance:      kr.Instance(),
		Distros:       kr.Distros(),
		Mode:          runModeString(kr.Mode()),
		DialTimeoutNs: int64(kr.DialTimeout()),
	}
	if kr.Mode() != kit.ModeBox && kr.Box() != "" && kr.Box() != "." {
		ce.ContainerName = spec.ContainerNameInstance(spec.ResolveBoxName(kr.Box()), kr.Instance())
	}
	if de, ok := kr.Exec().(spec.DeployExecutor); ok {
		ce.Venue = de.Venue()
		ce.VenueKind = de.Kind()
	}
	return ce
}

// runModeString converts kr.Mode() to the wire mode string — the reverse of verify_checks.go's
// verifyChecksMode.
func runModeString(m kit.RunMode) string {
	if m == kit.ModeBox {
		return "box"
	}
	return "live"
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
	env := r.env
	if r.kr != nil {
		env = pluginSnapshotCheckEnv(r.kr)
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return spec.CheckResult{Status: spec.StatusFail, Message: "verb " + word + ": marshal env: " + err.Error()}, true
	}
	opts := sdk.InvokeProviderOpts{}
	if r.kr != nil {
		if de, ok := r.kr.Exec().(spec.DeployExecutor); ok {
			if d := kit.DescriptorFromExecutor(de); d.Kind != "" {
				opts.VenueDescriptor = &d
			}
		}
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
