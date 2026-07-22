package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_arbiter_bracket.go — the "arbiter-bracket-acquire"/"arbiter-bracket-release" F10
// host-builders (FLOOR-SLIM-proper Unit-8, the K4-exit the former charly/arbiter_bracket.go's
// header tracked). OWNERSHIP of the Q1 resource-arbiter bracket call — deciding WHEN to
// acquire/release, around which dispatch — moved to command:bundle's handleLifecycleSimple
// (candy/plugin-bundle/deploy_target.go); the actual os.Setenv/os.Getenv(CHARLY_PREEMPT_LEASE)
// still execute HERE, in the host process, so a nested `charly` subprocess spawned by the SAME
// outer invocation still inherits the lease and skips re-acquiring — the property arbiter_bracket.go
// existed to preserve. Same registerHostBuilder/typedHostBuilder shape as
// host_build_deploy_config_save_state.go (Q2), the precedent this exit follows.

const arbiterBracketAcquireKind = "arbiter-bracket-acquire"
const arbiterBracketReleaseKind = "arbiter-bracket-release"

// hostBuildArbiterBracketAcquire acquires the shared resource-arbiter claim for a persistent
// (non-transient) claimant — the SAME call arbiterBracketedStart used to make inline, now reached
// from the plugin's own Start dispatch.
func hostBuildArbiterBracketAcquire(_ context.Context, req spec.ArbiterBracketAcquireRequest, _ buildEngineContext) (spec.ArbiterBracketAcquireReply, error) {
	if _, err := acquireResourceForClaimant(req.Name, req.Node, false); err != nil {
		return spec.ArbiterBracketAcquireReply{}, err
	}
	return spec.ArbiterBracketAcquireReply{}, nil
}

// hostBuildArbiterBracketRelease releases claimant's lease (a no-op when it holds none, or when
// an outer orchestrator owns the lease) — the SAME call both arbiterBracketedStart's
// release-on-failure leg and arbiterBracketedStop's unconditional release-after-stop leg used to
// make inline.
func hostBuildArbiterBracketRelease(_ context.Context, req spec.ArbiterBracketReleaseRequest, _ buildEngineContext) (spec.ArbiterBracketReleaseReply, error) {
	releaseResourceClaim(req.Name)
	return spec.ArbiterBracketReleaseReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(arbiterBracketAcquireKind, typedHostBuilder(arbiterBracketAcquireKind, hostBuildArbiterBracketAcquire))
	registerHostBuilder(arbiterBracketReleaseKind, typedHostBuilder(arbiterBracketReleaseKind, hostBuildArbiterBracketRelease))
	return true
}()
