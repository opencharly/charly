package main

// check_members.go — cross-deployment probing for `charly check`.
//
// Two seams let ONE deployment act as a test DRIVER probing a SEPARATE
// deployment as the SUBJECT (e.g. a Chrome pod CDP-probing a web-server pod):
//
//  1. The `on:` step modifier (Check.On) dispatches a probe against a named
//     DRIVER deployment instead of the subject under test. Its wiring into
//     `charly check live` lives here (liveTargetResolver); the per-step swap is in
//     checkrun.go runOne; the harness path wires its own resolveScoringChain.
//
//  2. The unified ${HOST:<member>} address variable lets the driven probe TARGET
//     a SIBLING member over the shared `charly` network or the host. The presence
//     of a :port segment selects the resolution:
//       ${HOST:name}        -> the member's container DNS name on the shared
//                              `charly` net (charly-<name>), the pod->pod address.
//                              Inspect-free + it verifies the member is running.
//       ${HOST:name:port}   -> a host-reachable 127.0.0.1:NNNN for that member's
//                              <port>, via the shared resolveCheckEndpoint
//                              (container published port, or ssh -L forward for a
//                              VM/host member). The host-vantage address a
//                              local/host driver uses to reach a pod/VM.
//
// Host vars are pre-resolved per run and folded into RunnerConfig.HostVars, which
// kit.Runner.EffectiveEnv overlays onto WHATEVER env is active (primary, on:-swapped,
// or harness bucket), so a `cdp:` check with `on: chrome` and `url:
// http://${HOST:web}:8080` works the same in `charly check live`, a kind:check bed, and
// an AI-iteration run (R3).

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// resolveHostVarsForChecks scans the given checks for ${HOST:<member>} references, resolves
// each, and returns the resolved address map (folded into RunnerConfig.HostVars) plus the
// teardown funcs for any ssh -L forwards opened for ${HOST} against a VM/host subject. Returns
// (nil, nil) when no host refs are present. The caller closes the returned cleanups at run end
// (via kit.CloseHostCleanups) on the paths that tear down ${HOST} forwards (VM / group); the
// pod / local paths, which historically leaked them, still discard them.
func resolveHostVarsForChecks(checks []spec.Op, instance string) (map[string]string, []func()) {
	refs := kit.CollectHostRefs(checks)
	if len(refs) == 0 {
		return nil, nil
	}
	return resolveHostVars(refs, instance)
}

// resolveHostVarsForSteps is the plan-step counterpart (harness / iterate / feature-run /
// live-plan paths), flattening every step's embedded Op.
func resolveHostVarsForSteps(plan []spec.Step, instance string) (map[string]string, []func()) {
	checks := make([]spec.Op, 0, len(plan))
	for _, st := range plan {
		checks = append(checks, st.Op)
	}
	return resolveHostVarsForChecks(checks, instance)
}

// resolveHostVars resolves each ${HOST:<member>} key to its address. A key that can't
// be resolved (subject not running, bad port) is left OUT of the map; the
// referencing check then FAILS via runOne's unresolved-host-var path
// (filterHostVars) — an unreachable member is a real failure, NEVER a SKIP (a
// skip on an unreachable dependency would be a fake pass). Returns cleanups for
// any ssh -L forwards opened.
func resolveHostVars(refs []string, instance string) (map[string]string, []func()) {
	vars := map[string]string{}
	var cleanups []func()
	for _, key := range refs {
		_, arg, ok := kit.SplitHostKey(key)
		if !ok {
			continue
		}
		// arg is "<member>" (DNS) or "<member>:<port>" (host endpoint). The
		// presence of a :port segment selects the resolution.
		dep, portStr, hasPort := strings.Cut(arg, ":")
		if !hasPort {
			// ${HOST:<member>} → the running container's DNS name on the shared
			// `charly` net (charly-<member>); also verifies it is actually running.
			if _, ctr, err := resolveContainer(arg, instance); err == nil {
				vars[key] = ctr
			} else {
				fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, err)
			}
			continue
		}
		// ${HOST:<member>:<port>} → a host-reachable endpoint for that port.
		port, perr := strconv.Atoi(strings.TrimSpace(portStr))
		if perr != nil || port < 1 || port > 65535 {
			fmt.Fprintf(os.Stderr, "check: ${%s} — invalid port %q\n", key, portStr)
			continue
		}
		venue, verr := resolveCheckVenue(dep, instance)
		if verr != nil {
			fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, verr)
			continue
		}
		ep, eerr := resolveCheckEndpoint(venue, port)
		if eerr != nil {
			fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, eerr)
			continue
		}
		vars[key] = ep.Addr
		cleanups = append(cleanups, ep.Close)
	}
	return vars, cleanups
}

// filterHostVars returns the subset of unresolved variable keys that are
// cross-member ${HOST:…} vars. runOne FAILS a check that references any of these
// filterHostVars (the ${HOST:…} unresolved-var filter) moved to sdk/kit (planspec.go) with the
// plan walk that consumes it; package main references it directly as kit.FilterHostVars.
// closeHostCleanups / collectHostRefs / splitHostKey (P12a follow-up) moved to
// sdk/kit (hostrefs.go) alongside it — all pure over spec.Op / kit.HostVar, with
// zero core state; this file's callers (resolveHostVarsForChecks, resolveHostVars,
// check_cmd.go, check_runner_live.go) stay core (they drive live venue resolution)
// and call kit.CloseHostCleanups / kit.CollectHostRefs / kit.SplitHostKey.

// liveTargetResolver builds the `on:` DRIVER venue resolver used by `charly check live`
// (and kind:check beds, which drive `charly check live`); venueResolver (planrun_adapter.go)
// adapts it to the kit.VenueResolver seam the runner's per-step SwapVenue drives. For a named
// DRIVER deployment it resolves the execution venue (resolveCheckVenue — container / VM /
// local, the same classifier the interactive verbs use) plus a best-effort runtime var
// resolver (the driver's own ${HOST_PORT}/${CONTAINER_IP}). The per-step swap (kit.Runner.
// SwapVenue) also sets the runner's box to <driver> so the host-side cdp/vnc/mcp verb dispatch
// connects to the driver's endpoint (via their out-of-process plugins). ${HOST:<member>}
// addressing of the SUBJECT rides in via RunnerConfig.HostVars (the kit.Runner.EffectiveEnv
// overlay), independent of which venue is active.
func liveTargetResolver(instance string) func(string) (*kit.CheckVarResolver, deploykit.DeployExecutor, error) {
	return func(target string) (*kit.CheckVarResolver, deploykit.DeployExecutor, error) {
		venue, err := resolveCheckVenue(target, instance)
		if err != nil {
			return nil, nil, err
		}
		res := liveDeployVarResolver(target, instance, venue)
		return res, venue.Exec, nil
	}
}

// liveDeployVarResolver builds a runtime var resolver for a named pod
// deployment (container venue). Best-effort: a non-container venue or an
// unreadable image label yields an empty resolver (the driven probe then relies
// on ${HOST:<member>} + literals, which is the common cross-deployment case). Shares
// the ResolveCheckVarsRuntime primitive with the primary target (R3).
func liveDeployVarResolver(name, instance string, venue *CheckVenue) *kit.CheckVarResolver {
	if venue == nil || !venue.IsContainer() {
		return &kit.CheckVarResolver{}
	}
	dir, _ := os.Getwd()
	var projectCfg *Config
	var deployOverlay *spec.BundleNode
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check live on:"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(name, instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[name]; ok {
			deployOverlay = &entry
		}
	}
	imageRef := resolveDeployBoxName(name, instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return &kit.CheckVarResolver{}
	}
	meta, err := ExtractMetadata(venue.Engine, resolvedRef)
	if err != nil || meta == nil {
		return &kit.CheckVarResolver{}
	}
	res, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, venue.Engine, name, venue.Name, instance)
	return stampCharlyBin(res)
}
