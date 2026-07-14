package main

import (
	"context"
	"encoding/json"
)

// podStartOptsCtxKey threads the direct-mode `charly start` CLI extras (--env/--port/--volume/
// --bind/auto-detect) from the verb dispatch through LifecycleTarget.Start(ctx) — which carries no
// opts — into the pod start-plan hook, preserving parity for the runDirect path. Absent ⇒ zero opts
// (the quadlet path — the deployed/bed case — ignores them anyway).
type podStartOptsCtxKey struct{}

func withPodStartOpts(ctx context.Context, o podStartOpts) context.Context {
	return context.WithValue(ctx, podStartOptsCtxKey{}, o)
}

func podStartOptsFromCtx(ctx context.Context) podStartOpts {
	if o, ok := ctx.Value(podStartOptsCtxKey{}).(podStartOpts); ok {
		return o
	}
	return podStartOpts{}
}

// podStopUnmountCtxKey threads `charly stop --unmount` (enc FUSE teardown) through
// LifecycleTarget.Stop(ctx) into the pod stop-plan hook.
type podStopUnmountCtxKey struct{}

func withPodStopUnmount(ctx context.Context, unmount bool) context.Context {
	return context.WithValue(ctx, podStopUnmountCtxKey{}, unmount)
}

func podStopUnmountFromCtx(ctx context.Context) bool {
	u, _ := ctx.Value(podStopUnmountCtxKey{}).(bool)
	return u
}

// pod_lifecycle_dispatch.go — the F6 HOST dispatch for the pod deep-body lifecycle (the K4 move). It
// resolves the spec.PodLifecyclePlan host-side (pod_lifecycle_resolve.go = #59 inventory), threads it
// into the plugin's OpStart/OpStop op.Params, and BRACKETS the shared arbiter claim around the op:
// acquire BEFORE OpStart, release AFTER OpStop, and release ON THE FAILURE PATH (a start that errors
// after acquire must not leak the claim). The CHARLY_PREEMPT_LEASE lease is host-process M state a
// placement-agnostic plugin cannot own, so it stays the in-core proxy (acquireResourceForClaimant).
// vm registers NO plan hook — it shells `charly vm start` and manages its own claim — so this bracket
// is POD-SCOPED by construction (gated on a registered plan hook), never double-claiming a vm.

// podLifecyclePlanResolver resolves + marshals the host-side PodLifecyclePlan for a deploy op. ctx
// carries the direct-mode start opts (podStartOptsFromCtx) on the start path.
type podLifecyclePlanResolver func(ctx context.Context, box, instance string) (json.RawMessage, error)

var (
	lifecycleStartPlanHooks = map[string]podLifecyclePlanResolver{}
	lifecycleStopPlanHooks  = map[string]podLifecyclePlanResolver{}
)

// registerLifecyclePlanHooks records the start/stop plan resolvers for a substrate word. Called at
// package-var init (before any init(), race-free — like registerDeployPreresolver / the vm prepare hook).
func registerLifecyclePlanHooks(word string, start, stop podLifecyclePlanResolver) {
	if word == "" {
		return
	}
	if start != nil {
		lifecycleStartPlanHooks[word] = start
	}
	if stop != nil {
		lifecycleStopPlanHooks[word] = stop
	}
}

var _ = func() bool {
	registerLifecyclePlanHooks("pod",
		func(ctx context.Context, box, instance string) (json.RawMessage, error) {
			plan, err := resolvePodStartPlan(box, instance, podStartOptsFromCtx(ctx))
			if err != nil {
				return nil, err
			}
			return marshalJSON(plan)
		},
		func(ctx context.Context, box, instance string) (json.RawMessage, error) {
			plan, err := resolvePodStopPlan(box, instance, podStopUnmountFromCtx(ctx))
			if err != nil {
				return nil, err
			}
			return marshalJSON(plan)
		},
	)
	return true
}()
