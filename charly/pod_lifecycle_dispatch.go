package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk/spec"
)

// podStartOptsCtxKey threads the direct-mode `charly start` CLI extras (--env/--port/--volume/
// --bind/auto-detect) from the verb dispatch through LifecycleTarget.Start(ctx) — which carries no
// opts — into the pod start-plan hook, preserving parity for the runDirect path. Absent ⇒ zero opts
// (the quadlet path — the deployed/bed case — ignores them anyway).
// podStartOpts carries the direct-mode `charly start` CLI extras (they apply only to the runDirect
// path; the quadlet path — the deployed/bed case — bakes config into the unit).
type podStartOpts struct {
	Env          []string
	EnvFile      string
	Port         []string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
}

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

// podShellOpts carries `charly shell`'s per-invocation CLI extras (the flags that shape the resolved
// exec/run argv but are NOT in the deploy config) through LifecycleTarget.Attach(ctx) — which carries
// only cmd+tty — into the pod attach-plan hook. Interactive/WrapPTY are the HOST-RESOLVED tty booleans
// (interactive = force_tty || isTerminal(); wrap_pty = force_tty && !isTerminal()) — computed at the
// moment of the real CLI invocation (Cutover B unit 2: host_build_pod_lifecycle_dispatch.go's
// hostBuildPodShell) and threaded as DATA, since an out-of-process plugin's own os.Stdout is not the
// operator's terminal (the P13-KERNEL walk-port direction-flip: resolvePodShellPlan/buildShellArgs/
// buildExecArgs moved to the plugin, which must never re-derive isTerminal() against its own stdio).
type podShellOpts struct {
	Tag          string
	EnvFile      string
	Env          []string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
	Interactive  bool
	WrapPTY      bool
}

type podShellOptsCtxKey struct{}

func withPodShellOpts(ctx context.Context, o podShellOpts) context.Context {
	return context.WithValue(ctx, podShellOptsCtxKey{}, o)
}

func podShellOptsFromCtx(ctx context.Context) podShellOpts {
	if o, ok := ctx.Value(podShellOptsCtxKey{}).(podShellOpts); ok {
		return o
	}
	return podShellOpts{}
}

// podCmdOpts carries `charly cmd`'s per-invocation extra (--sidecar) through Attach(ctx) into the pod
// cmd-plan resolver (agent-forwarding env is resolved host-side; --notify stays a host wrapper).
type podCmdOpts struct {
	Sidecar string
}

type podCmdOptsCtxKey struct{}

func withPodCmdOpts(ctx context.Context, o podCmdOpts) context.Context {
	return context.WithValue(ctx, podCmdOptsCtxKey{}, o)
}

func podCmdOptsFromCtx(ctx context.Context) podCmdOpts {
	if o, ok := ctx.Value(podCmdOptsCtxKey{}).(podCmdOpts); ok {
		return o
	}
	return podCmdOpts{}
}

// pod_lifecycle_dispatch.go — the F6 HOST dispatch for the pod deep-body lifecycle (the K4 move,
// P13-KERNEL step-4(ii) direction-flip). It marshals the RAW CLI opts (spec.PodStartOpts/
// PodStopOpts/PodAttachOpts — the plugin now self-resolves the actual spec.PodLifecyclePlan from
// these, candy/plugin-deploy-pod/resolve.go), threads them into the plugin's OpStart/OpStop
// op.Params, and BRACKETS the shared arbiter claim around the op:
// acquire BEFORE OpStart, release AFTER OpStop, and release ON THE FAILURE PATH (a start that errors
// after acquire must not leak the claim). The CHARLY_PREEMPT_LEASE lease is host-process M state a
// placement-agnostic plugin cannot own, so it stays the in-core proxy (acquireResourceForClaimant).
// vm registers NO plan hook — it shells `charly vm start` and manages its own claim — so this bracket
// is POD-SCOPED by construction (gated on a registered plan hook), never double-claiming a vm.

// podLifecyclePlanResolver resolves + marshals the host-side PodLifecyclePlan for a deploy op. ctx
// carries the direct-mode start opts (podStartOptsFromCtx) on the start path.
type podLifecyclePlanResolver func(ctx context.Context, box, instance string) (json.RawMessage, error)

// attachPlanResolver resolves the host-side #PodLiveStdioPlan (a single resolved `script`) for the F12
// interactive/live-stdio Attach op: tty=true → the `charly shell` resolver (`podman run/exec -it`);
// tty=false → the `charly cmd` resolver (`engine exec -i … sh -c`). cmd is the user's command argv.
type attachPlanResolver func(ctx context.Context, box, instance string, cmd []string, tty bool) (json.RawMessage, error)

// logsPlanResolver resolves the host-side #PodLiveStdioPlan for the F12 `charly logs [-f]` op (the
// resolved `journalctl`/`<engine> logs` stream command). A substrate with no logs resolver (vm) keeps
// the plain opts-threaded OpLogs path (grpcSubstrateLifecycle.Logs).
type logsPlanResolver func(ctx context.Context, box, instance string, opts LogsOpts) (json.RawMessage, error)

var (
	lifecycleStartPlanHooks  = map[string]podLifecyclePlanResolver{}
	lifecycleStopPlanHooks   = map[string]podLifecyclePlanResolver{}
	lifecycleAttachPlanHooks = map[string]attachPlanResolver{}
	lifecycleLogsPlanHooks   = map[string]logsPlanResolver{}
)

// registerLifecycleLivePlanHooks records the F12 attach/logs plan resolvers for a substrate word.
// Called at package-var init (race-free, like registerLifecyclePlanHooks).
func registerLifecycleLivePlanHooks(word string, attach attachPlanResolver, logs logsPlanResolver) {
	if word == "" {
		return
	}
	if attach != nil {
		lifecycleAttachPlanHooks[word] = attach
	}
	if logs != nil {
		lifecycleLogsPlanHooks[word] = logs
	}
}

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

// P13-KERNEL step-4(ii): the pod lifecycle now SELF-RESOLVES its start/stop/attach plans
// (candy/plugin-deploy-pod's resolve.go/resolve_f12.go) from these RAW opts + the deploy key
// already on lifecycleParams.Name, instead of the host pre-resolving a spec.PodLifecyclePlan /
// spec.PodLiveStdioPlan and threading the RESULT. The registered closures below therefore marshal
// the plain CUE-generated opts types (spec.PodStartOpts/PodStopOpts/PodAttachOpts) — NOT a
// resolved plan — reusing the SAME "plan" wire slot (lifecycleParams.Plan) unchanged: the map/
// registration MECHANISM in substrate_lifecycle_grpc.go (the arbiter-claim bracket gated on
// hasPlan) is untouched, only the payload CONTENT changes. Logs registers NO hook — Logs() already
// threads its LogsOpts unconditionally (extra["opts"]), which is all candy/plugin-deploy-pod's
// resolvePodLogsPlan needs (box/instance come from the deploy key on lifecycleParams.Name).
var _ = func() bool {
	registerLifecyclePlanHooks("pod",
		func(ctx context.Context, _, _ string) (json.RawMessage, error) {
			o := podStartOptsFromCtx(ctx)
			return marshalJSON(spec.PodStartOpts{
				Env: o.Env, EnvFile: o.EnvFile, Port: o.Port, VolumeFlag: o.VolumeFlag,
				Bind: o.Bind, NoAutoDetect: o.NoAutoDetect,
			})
		},
		func(ctx context.Context, _, _ string) (json.RawMessage, error) {
			return marshalJSON(spec.PodStopOpts{Unmount: podStopUnmountFromCtx(ctx)})
		},
	)
	registerLifecycleLivePlanHooks("pod",
		func(ctx context.Context, _, _ string, cmd []string, tty bool) (json.RawMessage, error) {
			o := podShellOptsFromCtx(ctx)
			co := podCmdOptsFromCtx(ctx)
			return marshalJSON(spec.PodAttachOpts{
				Cmd: cmd, Tty: tty,
				Shell: spec.PodShellOpts{
					Tag: o.Tag, EnvFile: o.EnvFile, Env: o.Env, VolumeFlag: o.VolumeFlag,
					Bind: o.Bind, NoAutoDetect: o.NoAutoDetect, Interactive: o.Interactive, WrapPTY: o.WrapPTY,
				},
				CmdOpts: spec.PodCmdOpts{Sidecar: co.Sidecar},
			})
		},
		nil, // Logs needs no hook — its LogsOpts already threads unconditionally
	)
	return true
}()
