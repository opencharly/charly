package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// CheckEnv is the SERIALIZABLE subset of a *Runner that crosses the wire to an
// out-of-process verb provider. A *Runner cannot be marshalled (it holds a live
// DeployExecutor + closures), so a verb provider receives this snapshot as the
// Operation.Env. It carries only what a marshallable verb needs; verbs that reach
// host-side Runner internals (http/port-reachable/kill) stay in-process and are
// never routed to an out-of-proc provider.
//
// CUE-sourced (K1-unblock W3 Unit B, sdk/schema/checkresult.cue's #CheckEnv) — the SAME
// generated shape sdk/checkverb.go's out-of-process decode, candy/plugin-check's
// InvokeProvider-backed VerbResolver marshal, and this file's own InvokeProvider detached-
// CheckContext construction (plugin_dispatch_reverse.go) all share, replacing what were three
// independently hand-maintained mirrors of the same wire contract with one CUE def + one
// generated struct (R3/SDD).
type CheckEnv = spec.CheckEnv

func runModeName(m RunMode) string {
	switch m {
	case RunModeBox:
		return "box"
	default:
		return "live"
	}
}

// snapshotCheckEnv captures the serializable invocation context for a verb
// provider call.
func snapshotCheckEnv(kr *kit.Runner, _ *spec.Op) *CheckEnv {
	// Box is the verb's TARGET name across the wire. For a VM deployment it must be the per-deploy
	// DOMAIN IDENTITY (VmTargetName) — the out-of-process vm/spice/libvirt plugins prefix charly-
	// onto it to address the live domain and cannot LoadUnified to compute it themselves (the
	// go-libvirt shed dropped that in-core remap). A pod/k8s/android deployment leaves VmName empty,
	// so VmTargetName() == Box (unchanged).
	ce := &CheckEnv{Box: kr.VmTargetName(), Instance: kr.Instance(), Distros: kr.Distros(), Mode: runModeName(kr.Mode()), DialTimeoutNs: int64(kr.DialTimeout())}
	// The container name is meaningful only for a live (non-box) run with a real box —
	// the same condition under which a live-container verb runs at all.
	if kr.Mode() != RunModeBox && kr.Box() != "" && kr.Box() != "." {
		ce.ContainerName = kit.ContainerNameInstance(kit.ResolveBoxName(kr.Box()), kr.Instance())
	}
	if de := deployExecOf(kr); de != nil {
		ce.Venue = de.Venue()
		ce.VenueKind = de.Kind()
	}
	return ce
}

// pluginCheckResult is the wire form a verb provider returns (Operation.Result
// JSON for the `verb` class). Kept minimal — status + message — so the result
// round-trips cleanly without serializing the host-only *Op/timing fields of a
// full CheckResult.
type pluginCheckResult struct {
	Status  string `json:"status"` // "pass" | "fail" | "skip"
	Message string `json:"message"`
}

// runPluginVerb dispatches the generic `plugin:` verb to its registered Provider
// (built-in OR out-of-tree, transport-invisible). This is the permanent plugin
// fall-through the foundation cutover (C0) adds; the built-in verb switch above is
// migrated into the registry in C1.
func (h *hostVerbResolver) runPluginVerb(ctx context.Context, c *spec.Op) spec.CheckResult {
	word := c.Plugin
	res := spec.CheckResult{Verb: "plugin"}
	// connectBakedPlugin (not a bare ResolveVerb) so a BAKED verb plugin resolves
	// project-lessly inside a deployed container / on a host where it is installed alongside
	// charly — additive: a registry hit returns immediately, and with no baked binary it is a
	// plain ResolveVerb miss.
	prov, ok := connectBakedPlugin(ClassVerb, word)
	if !ok {
		// An unresolved plugin verb is a FAILURE, not a skip — a bed asserting a
		// plugin verb that never registered must go red, not fake-green (mirrors
		// the unresolvable-${HOST:...} rule).
		res.Status = spec.StatusFail
		res.Message = fmt.Sprintf("no provider registered for plugin verb %q", word)
		return res
	}
	// Validate the authored plugin_input against the plugin's SERVED CUE schema
	// (base ++ plugin) BEFORE dispatch — a typo / missing / empty marker is a hard
	// failure here, never a silent runtime surprise. Transport-invisible: the def
	// comes from the process-wide schema set the load gate filled, identically for a
	// builtin and an external plugin.
	inputJSON := []byte("{}")
	if c.PluginInput != nil {
		j, err := marshalJSON(c.PluginInput)
		if err != nil {
			res.Status = spec.StatusFail
			res.Message = "plugin verb: marshal plugin_input: " + err.Error()
			return res
		}
		inputJSON = j
	}
	if err := validateAuthoredPluginInput(ClassVerb, word, inputJSON); err != nil {
		res.Status = spec.StatusFail
		res.Message = err.Error()
		return res
	}
	// A CheckVerbProvider plugin unit is IN-PROCESS and keeps the live check context: an
	// EXECUTION-NEEDING verb (one that reaches the executor / the host CheckContext)
	// dispatches via RunVerb, threaded the host verb resolver that cannot cross the wire.
	// Only an OUT-OF-PROCESS provider falls through to invokeVerbProvider, which marshals
	// the Op into the Invoke envelope (necessarily dropping the live host context).
	if cv, ok := prov.(CheckVerbProvider); ok {
		res = cv.RunVerb(ctx, h, c)
		res.Verb = "plugin"
		return res
	}
	res = h.invokeVerbProvider(ctx, prov, word, c)
	res.Verb = "plugin"
	return res
}

// invokeVerbProvider marshals the Op + the check env, Invokes the provider's OpRun, and
// decodes the pluginCheckResult into a CheckResult. It is the transport-invisible verb
// dispatch shared by the `plugin:` verb (runPluginVerb, after plugin_input validation)
// AND the external-charly-verb path (a live verb word — cdp/kube/… — whose provider is
// OUT-OF-PROCESS, not a CheckVerbProvider): an external verb reads the FULL Op it is
// handed here (params_json), so a verb's params stay authored in #Op with NO migration
// when its implementation moves out-of-tree. The caller sets res.Verb.
func (h *hostVerbResolver) invokeVerbProvider(ctx context.Context, prov Provider, word string, c *spec.Op) spec.CheckResult {
	res := spec.CheckResult{}
	// Resolve a relative committed-APK path (appium: install-app, `apk: ./tests/data/…`)
	// against the ORIGINATING candy's source tree HOST-side, BEFORE marshaling — an
	// out-of-process verb has no CandyDirs, so it cannot anchor the fixture itself.
	// Same candy-anchored walk-up the host APK resolver uses (R3); the plugin then sees
	// an absolute, candy-anchored path.
	if apk := kit.InputStr(c, "apk"); apk != "" {
		resolved, err := h.resolveCheckApk(apk, c.Origin)
		if err != nil {
			res.Status = spec.StatusFail
			res.Message = fmt.Sprintf("verb %q: %v", word, err)
			return res
		}
		if resolved != apk {
			cc := *c
			cc.PluginInput = make(map[string]any, len(c.PluginInput))
			for k, v := range c.PluginInput {
				cc.PluginInput[k] = v
			}
			cc.PluginInput["apk"] = resolved
			c = &cc
		}
	}
	// A host-coupled verb resolves its own inputs through the GENERIC CheckContextService
	// reverse-legs (cc.ResolveEndpoint / ResolveGraphicsEndpoint / ResolveClusterContext /
	// ResolveImageLabel) — this dispatch stays verb-agnostic (the Uniform API Invariant). Those
	// reverse-legs open ssh -L forwards / socket bridges DURING the Invoke; drain them (LIFO)
	// after it returns — the forward must outlive the plugin's dial. Reset per-Invoke so a
	// leftover from a prior op never leaks in.
	h.endpointCleanups = nil
	defer h.runEndpointCleanups()
	params, err := marshalJSON(c)
	if err != nil {
		res.Status = spec.StatusFail
		res.Message = fmt.Sprintf("verb %q: marshal op: %v", word, err)
		return res
	}
	ce := snapshotCheckEnv(h.kr, c)
	env, err := marshalJSON(ce)
	if err != nil {
		res.Status = spec.StatusFail
		res.Message = fmt.Sprintf("verb %q: marshal env: %v", word, err)
		return res
	}
	// Attach the host's live executor over the E3b reverse channel when the provider is
	// out-of-process (executorInvoker — the grpcProvider) and a live venue executor exists,
	// so an EXEC-based external check verb (record/dbus/wl) can call BACK RunCapture/GetFile
	// against the running container. A port-based external verb (cdp/vnc/mcp/spice/kube)
	// never dials the broker; a builtin verb never reaches here (a CheckVerbProvider
	// dispatches in-proc via RunVerb in runPluginVerb).
	op := &Operation{Reserved: word, Op: OpRun, Params: params, Env: env}
	var out *Result
	de := deployExecOf(h.kr)
	if ei, ok := prov.(executorInvoker); ok && de != nil {
		// A check verb never drives the RunHostStep host-engine channel, so the host-engine
		// context is the zero value (no project Config needed for RunCapture/GetFile) and the
		// venue is never rebootable (a check verb never reboots the target). Alongside the
		// ExecutorService (the venue), serve the CheckContextService (F2) so a HOST-COUPLED
		// out-of-process kit verb reaches the host-vantage HTTPDo + AddBackground legs.
		var addBg func(int)
		if h.kr.Scenario() != nil {
			addBg = h.kr.Scenario().AddBackground
		}
		cc := &checkContextReverseServer{httpBase: h.kr.HTTPClient(), addBg: addBg, resolveEp: h.resolveVerbEndpoint, resolveGfx: h.resolveVerbGraphics, resolveClusterCtx: h.resolveClusterContext, resolveImgLabel: h.resolveImageLabel}
		out, err = ei.InvokeWithExecutor(ctx, op, de, buildEngineContext{}, false, cc)
	} else {
		out, err = prov.Invoke(ctx, op)
	}
	if err != nil {
		res.Status = spec.StatusFail
		res.Message = fmt.Sprintf("verb %q: %v", word, err)
		return res
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		res.Status = spec.StatusFail
		res.Message = fmt.Sprintf("verb %q: decode result: %v", word, err)
		return res
	}
	switch pr.Status {
	case "pass":
		res.Status = spec.StatusPass
	case "skip":
		res.Status = spec.StatusSkip
	default:
		res.Status = spec.StatusFail
	}
	res.Message = pr.Message
	return res
}
