package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencharly/sdk/kit"
)

// CheckEnv is the SERIALIZABLE subset of a *Runner that crosses the wire to an
// out-of-process verb provider. A *Runner cannot be marshalled (it holds a live
// DeployExecutor + closures), so a verb provider receives this snapshot as the
// Operation.Env. It carries only what a marshallable verb needs; verbs that reach
// host-side Runner internals (http/port-reachable/kill) stay in-process and are
// never routed to an out-of-proc provider.
type CheckEnv struct {
	Box      string `json:"box"`
	Instance string `json:"instance"`
	Mode     string `json:"mode"` // "live" | "box"
	// ContainerName is the HOST-AUTHORITATIVE container name for the deployment under
	// test (charly-<box>[_<instance>], with registry-ref stripping) — computed by the
	// host so an out-of-process verb (e.g. the external appium plugin) reaches the
	// running container's engine inspect / cp without re-deriving charly's naming
	// convention. Empty for an image-context (box-mode) run or a venue with no box.
	ContainerName string   `json:"container_name"`
	Distros       []string `json:"distros"`
	Venue         string   `json:"venue"`      // r.Exec.Venue()
	VenueKind     string   `json:"venue_kind"` // r.Exec.Kind()
	// DialTimeoutNs is the engine's per-dial ceiling (r.DialTimeout) in nanoseconds — the
	// kit.CheckContext.DialTimeout() leg for an out-of-process host-coupled check verb (a
	// plain scalar, so it rides the snapshot rather than the CheckContextService channel).
	DialTimeoutNs int64 `json:"dial_timeout_ns,omitempty"`
	// Substrate carries the host-resolved, OPAQUE preresolution payload for a verb
	// whose plugin needs a host-computed input it cannot derive across the process
	// boundary — a cdp/vnc/mcp/spice dialable endpoint (resolved from podman / venue /
	// go-libvirt machinery the out-of-process plugin owns none of). It is filled by the
	// verb's registered preresolver (verb_preresolve.go) keyed on the verb word, and
	// decoded by the matching plugin into its own endpoint type. nil for any verb with no
	// registered preresolver (wl/dbus/record/adb/appium/…) and for an op whose preresolver
	// resolved nothing. This is the check-verb analogue of DeployVenue.Substrate — ONE
	// generic, opaque, per-plugin channel, never a per-verb typed field (the Uniform API
	// Invariant): adding a host-resolved verb adds a preresolver, not a CheckEnv field.
	Substrate json.RawMessage `json:"substrate,omitempty"`
}

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
func snapshotCheckEnv(r *Runner, _ *Op) *CheckEnv {
	// Box is the verb's TARGET name across the wire. For a VM deployment it must be the
	// resolved vm: ENTITY name (vmTargetName) — the out-of-process vm/spice/libvirt plugins
	// prefix charly- onto it to address the live domain and cannot LoadUnified to remap a
	// deploy/bed name themselves (the go-libvirt shed dropped that in-core remap). A
	// pod/k8s/android deployment leaves VmName empty, so vmTargetName() == Box (unchanged).
	ce := &CheckEnv{Box: r.vmTargetName(), Instance: r.Instance, Distros: r.Distros, Mode: runModeName(r.Mode), DialTimeoutNs: int64(r.DialTimeout)}
	// The container name is meaningful only for a live (non-box) run with a real box —
	// the same condition under which a live-container verb runs at all.
	if r.Mode != RunModeBox && r.Box != "" && r.Box != "." {
		ce.ContainerName = containerNameInstance(resolveBoxName(r.Box), r.Instance)
	}
	if r.Exec != nil {
		ce.Venue = r.Exec.Venue()
		ce.VenueKind = r.Exec.Kind()
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
func (r *Runner) runPluginVerb(ctx context.Context, c *Op) CheckResult {
	word := c.Plugin
	res := CheckResult{Verb: "plugin"}
	// connectBakedPlugin (not a bare ResolveVerb) so a BAKED verb plugin resolves
	// project-lessly inside a deployed container / on a host where it is installed alongside
	// charly — additive: a registry hit returns immediately, and with no baked binary it is a
	// plain ResolveVerb miss.
	prov, ok := connectBakedPlugin(ClassVerb, word)
	if !ok {
		// An unresolved plugin verb is a FAILURE, not a skip — a bed asserting a
		// plugin verb that never registered must go red, not fake-green (mirrors
		// the unresolvable-${HOST:...} rule).
		res.Status = TestFail
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
			res.Status = TestFail
			res.Message = "plugin verb: marshal plugin_input: " + err.Error()
			return res
		}
		inputJSON = j
	}
	if err := validateAuthoredPluginInput(ClassVerb, word, inputJSON); err != nil {
		res.Status = TestFail
		res.Message = err.Error()
		return res
	}
	// A CheckVerbProvider plugin unit is IN-PROCESS and keeps the live executor: an
	// EXECUTION-NEEDING verb (one that reaches r.Exec / the *Runner) dispatches via
	// RunVerb, which carries the *Runner that cannot cross the wire. Only an
	// OUT-OF-PROCESS provider falls through to invokeVerbProvider, which marshals the
	// Op into the Invoke envelope (necessarily dropping the *Runner).
	if cv, ok := prov.(CheckVerbProvider); ok {
		res = cv.RunVerb(ctx, r, c)
		res.Verb = "plugin"
		return res
	}
	res = r.invokeVerbProvider(ctx, prov, word, c)
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
func (r *Runner) invokeVerbProvider(ctx context.Context, prov Provider, word string, c *Op) CheckResult {
	res := CheckResult{}
	// Resolve a relative committed-APK path (appium: install-app, `apk: ./tests/data/…`)
	// against the ORIGINATING candy's source tree HOST-side, BEFORE marshaling — an
	// out-of-process verb has no Runner.CandyDirs, so it cannot anchor the fixture itself.
	// Same candy-anchored walk-up the host APK resolver uses (R3); the plugin then sees
	// an absolute, candy-anchored path.
	if apk := kit.InputStr(c, "apk"); apk != "" {
		resolved, err := r.resolveCheckApk(apk, c.Origin)
		if err != nil {
			res.Status = TestFail
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
	// Host-side per-verb preresolution via the GENERIC registry — no per-verb
	// special-casing in this dispatch (the Uniform API Invariant). The preresolver
	// registered under this verb word runs: cdp/vnc/mcp/spice fill an opaque endpoint
	// Substrate (→ CheckEnv.Substrate, decoded by the matching plugin); kube rewrites the
	// op's KubeContext (carried in params). A verb with no registered preresolver
	// (wl/dbus/record/adb/appium/…) is a no-op. The preresolver may short-circuit (early
	// SKIP/FAIL) or open a tunnel/forward whose cleanup defers across Invoke (it carries
	// the live connection).
	// The generic host-endpoint reverse-leg (cc.ResolveEndpoint) opens ssh -L forwards DURING
	// the Invoke; drain them (LIFO) after it returns — the forward must outlive the plugin's
	// dial. Reset per-Invoke so a leftover from a prior op never leaks in.
	r.endpointCleanups = nil
	defer r.runEndpointCleanups()
	var substrate json.RawMessage
	if pre, ok := verbPreresolverFor(word); ok {
		sub, opOut, cleanup, early := pre(r, c)
		if cleanup != nil {
			defer cleanup()
		}
		if early != nil {
			return *early
		}
		if opOut != nil {
			c = opOut
		}
		substrate = sub
	}
	params, err := marshalJSON(c)
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: marshal op: %v", word, err)
		return res
	}
	ce := snapshotCheckEnv(r, c)
	ce.Substrate = substrate
	env, err := marshalJSON(ce)
	if err != nil {
		res.Status = TestFail
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
	if ei, ok := prov.(executorInvoker); ok && r.Exec != nil {
		// A check verb never drives the RunHostStep host-engine channel, so the host-engine
		// context is the zero value (no project Config needed for RunCapture/GetFile) and the
		// venue is never rebootable (a check verb never reboots the target). Alongside the
		// ExecutorService (the venue), serve the CheckContextService (F2) so a HOST-COUPLED
		// out-of-process kit verb reaches the host-vantage HTTPDo + AddBackground legs.
		var addBg func(int)
		if r.Scenario != nil {
			addBg = r.Scenario.AddBackground
		}
		cc := &checkContextReverseServer{httpBase: r.HTTPClient, addBg: addBg, resolveEp: r.resolveVerbEndpoint, resolveGfx: r.resolveVerbGraphics}
		out, err = ei.InvokeWithExecutor(ctx, op, r.Exec, buildEngineContext{}, false, cc)
	} else {
		out, err = prov.Invoke(ctx, op)
	}
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: %v", word, err)
		return res
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: decode result: %v", word, err)
		return res
	}
	switch pr.Status {
	case "pass":
		res.Status = TestPass
	case "skip":
		res.Status = TestSkip
	default:
		res.Status = TestFail
	}
	res.Message = pr.Message
	return res
}
