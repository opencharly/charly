package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/opencharly/sdk/spec"
)

// host_build_check_config.go — the transitional "check-config" host-builder (P12 Wave-2). A
// DEDICATED check-family seam (sibling of "check-bed"): the compiled-in command:check AI-harness
// owns the `charly check run` CLI + orchestration, but RESOLVING the check-project config it drives
// from (the bed-vs-iterate classification, the iterate: block, the sandbox class, the pod-target
// disposability, the include-expanded scored plan, the kind:agent catalog) is a composite of core
// loader Mechanisms (LoadUnified / CheckBeds / ResolveIterateSandbox / ScanCandy / ExpandPlanIncludes)
// a plugin — a separate module importing only sdk — cannot perform. So the host resolves the
// projection ONCE and ships it back, mirroring the reads the former in-core CheckRunCmd.Run dispatcher
// + runIterateEntity + run-local made. Class-generic action noun "check-config" (F11 — never a
// substrate word). Kept SEPARATE from the generic "config-resolve" seam (which stays vm/deploy-only)
// so a check concern never bloats the generic reply.
//
// Retention (Defaults.KeepCheckRuns) is deliberately NOT resolved here — the harness's per-run prune
// rides the EXISTING HostBuild("retention") seam (R3, the landed pruneCheckRuns engine).
//
// TRANSITIONAL — dies at K1: post-loaderkit the harness plugin loads the project itself and computes
// this projection directly (no host round-trip).
const checkConfigBuilderKind = "check-config"

func hostBuildCheckConfig(_ context.Context, req spec.CheckConfigRequest, _ buildEngineContext) (spec.CheckConfigReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return spec.CheckConfigReply{}, err
	}
	if !ok || uf == nil {
		return spec.CheckConfigReply{}, nil // graceful-degrade: no project → empty projection
	}

	reply := spec.CheckConfigReply{
		// The opaque kind:agent catalog the harness decodes (resolveAgentViaPlugin) to pick the AI CLI.
		// map[string]json.RawMessage IS map[string]RawBody — no re-marshal.
		AgentBodies: uf.PluginKinds["agent"],
	}

	// Bed-vs-iterate classification for the requested entity (the former dispatcher's
	// `(!hasNode || node.Iterate == nil) && isBed` test): CheckBeds() is every disposable non-member
	// node, so an iterate entity IS also a bed — HasIterate is the discriminator.
	_, reply.IsBed = uf.CheckBeds()[req.Entity]
	node, hasNode := uf.Bundle[req.Entity]
	reply.HasNode = hasNode
	reply.HasIterate = hasNode && node.Iterate != nil

	// The readiness cap set the bed-runner's stepReady poll uses (opaque; a kit-default fallback
	// covers its absence). Project-wide, so resolved unconditionally.
	if rj, mErr := json.Marshal(loadedReadiness()); mErr == nil {
		reply.ReadinessJSON = rj
	}

	// Iterate orchestration inputs — only for an iterate entity.
	if reply.HasIterate {
		tk, tn := ResolveIterateSandbox(uf, node.Iterate.Sandbox)
		reply.SandboxKind = string(tk)
		reply.SandboxName = tn
		if ij, mErr := json.Marshal(node.Iterate); mErr == nil {
			reply.IterateJSON = ij
		}
		// The per-run pod-restart gate: is the pod sandbox target disposable?
		if tk == TargetKindPod {
			if cfg, cErr := LoadBundleConfig(); cErr == nil {
				if entry, eErr := scorePodTargetEntry(cfg, req.Entity, tn); eErr == nil {
					reply.PodTargetDisposable = entry.IsDisposable()
				}
			}
		}
		// The include-expanded scored plan (ExpandPlanIncludes over the project candies).
		if layers, sErr := ScanCandy(dir); sErr == nil {
			if plan, eErr := ExpandPlanIncludes(uf, layers, node.Plan); eErr == nil {
				reply.Plan = plan
			}
		}
	}
	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(checkConfigBuilderKind, typedHostBuilder(checkConfigBuilderKind, hostBuildCheckConfig))
	return true
}()
