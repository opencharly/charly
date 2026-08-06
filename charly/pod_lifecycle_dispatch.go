package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/spec/spec"
)

// ctxBox[T] is the generic ctx-value plumbing every pod-lifecycle CLI-extras type threads from the
// host_build_pod_lifecycle_dispatch.go handler, through a LifecycleTarget call that carries no opts
// of its own (Start/Stop/Attach), into the pod plan hooks below (#55 W3 A10, R3): ONE generic
// with/from pair replaces what used to be four hand-rolled ctxKey+With+From triplets. Each opts type
// below instantiates its own ctxBox[T] — a distinct Go type per T, so the context key stays
// collision-free by construction, exactly as the former per-type empty-struct keys were.
type ctxBox[T any] struct{}

func (ctxBox[T]) with(ctx context.Context, v T) context.Context {
	return context.WithValue(ctx, ctxBox[T]{}, v)
}

func (ctxBox[T]) from(ctx context.Context) T {
	v, _ := ctx.Value(ctxBox[T]{}).(T)
	return v
}

// podStartOpts carries the direct-mode `charly start` CLI extras (--env/--port/--volume/--bind/
// auto-detect; they apply only to the runDirect path — the quadlet path bakes config into the unit)
// from the verb dispatch through LifecycleTarget.Start(ctx) into the pod start-plan hook. Absent ⇒
// zero opts.
type podStartOpts struct {
	Env          []string
	EnvFile      string
	Port         []string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
}

func withPodStartOpts(ctx context.Context, o podStartOpts) context.Context {
	return ctxBox[podStartOpts]{}.with(ctx, o)
}

func podStartOptsFromCtx(ctx context.Context) podStartOpts {
	return ctxBox[podStartOpts]{}.from(ctx)
}

// podStopUnmount threads `charly stop --unmount` (enc FUSE teardown) through LifecycleTarget.Stop(ctx)
// into the pod stop-plan hook. A distinct named bool (not a raw bool) so its ctxBox[T] key can never
// collide with an unrelated bool-typed ctx value added to this package later.
type podStopUnmount bool

func withPodStopUnmount(ctx context.Context, unmount bool) context.Context {
	return ctxBox[podStopUnmount]{}.with(ctx, podStopUnmount(unmount))
}

func podStopUnmountFromCtx(ctx context.Context) bool {
	return bool(ctxBox[podStopUnmount]{}.from(ctx))
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

func withPodShellOpts(ctx context.Context, o podShellOpts) context.Context {
	return ctxBox[podShellOpts]{}.with(ctx, o)
}

func podShellOptsFromCtx(ctx context.Context) podShellOpts {
	return ctxBox[podShellOpts]{}.from(ctx)
}

// podCmdOpts carries `charly cmd`'s per-invocation extra (--sidecar) through Attach(ctx) into the pod
// cmd-plan resolver (agent-forwarding env is resolved host-side; --notify stays a host wrapper).
type podCmdOpts struct {
	Sidecar string
}

func withPodCmdOpts(ctx context.Context, o podCmdOpts) context.Context {
	return ctxBox[podCmdOpts]{}.with(ctx, o)
}

func podCmdOptsFromCtx(ctx context.Context) podCmdOpts {
	return ctxBox[podCmdOpts]{}.from(ctx)
}

// pod_lifecycle_dispatch.go — the F6 HOST-side plan-hook table for the pod deep-body lifecycle
// (the K4 move, P13-KERNEL step-4(ii) direction-flip). These closures marshal the RAW CLI opts
// (spec.PodStartOpts/PodStopOpts/PodAttachOpts — the plugin self-resolves the actual
// spec.PodLifecyclePlan from these, candy/plugin-deploy-pod/resolve.go) into the payload
// pluginDeployTarget.Start/Stop/Attach (unified_targets.go, S3b) threads as the dispatch request's
// OptsJSON. hasPlan (lifecycleStartPlanHooks/… presence, keyed by word) ALSO gates whether the Q1
// arbiter bracket applies — pluginDeployTarget.Start/Stop read the SAME map's `ok` boolean directly
// (no separate mirror; see arbiter_bracket.go's doc comment for why the bracket itself stays
// core-resident rather than living inside these closures — CHARLY_PREEMPT_LEASE is host-process
// env state a placement-agnostic plugin cannot own). vm registers NO plan hook — it
// shells `charly vm start` and manages its own claim — so the bracket is POD-SCOPED by
// construction, never double-claiming a vm.

// podLifecyclePlanResolver resolves + marshals the host-side PodLifecyclePlan for a deploy op. ctx
// carries the direct-mode start opts (podStartOptsFromCtx) on the start path.
type podLifecyclePlanResolver func(ctx context.Context, box, instance string) (json.RawMessage, error)

// attachPlanResolver resolves the host-side #PodLiveStdioPlan (a single resolved `script`) for the F12
// interactive/live-stdio Attach op: tty=true → the `charly shell` resolver (`podman run/exec -it`);
// tty=false → the `charly cmd` resolver (`engine exec -i … sh -c`). cmd is the user's command argv.
type attachPlanResolver func(ctx context.Context, box, instance string, cmd []string, tty bool) (json.RawMessage, error)

var (
	lifecycleStartPlanHooks  = map[string]podLifecyclePlanResolver{}
	lifecycleStopPlanHooks   = map[string]podLifecyclePlanResolver{}
	lifecycleAttachPlanHooks = map[string]attachPlanResolver{}
)

// registerLifecycleLivePlanHooks records the F12 attach plan resolver for a substrate word.
// Called at package-var init (race-free, like registerLifecyclePlanHooks). The former logs
// resolver slot is DELETED (K-wave 2 cone R5) — Logs() threads its opts unconditionally, which
// is all candy/plugin-deploy-pod's resolvePodLogsPlan needs.
func registerLifecycleLivePlanHooks(word string, attach attachPlanResolver) {
	if word == "" {
		return
	}
	if attach != nil {
		lifecycleAttachPlanHooks[word] = attach
	}
}

// registerLifecyclePlanHooks records the start/stop plan resolvers for a substrate word. Called at
// package-var init (before any init(), race-free — like registerHostBuilder).
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
// resolved plan — reusing the SAME "plan" wire slot (lifecycleParams.Plan) unchanged: the
// lifecycleStartPlanHooks/lifecycleStopPlanHooks registration MECHANISM (this file) and the
// arbiter-claim bracket it gates (arbiter_bracket.go, S3b — was substrate_lifecycle_grpc.go
// before the deploy-dispatch cluster moved) are untouched, only the payload CONTENT changes.
// Logs registers NO hook (the former logs-resolver slot is DELETED, K-wave 2 cone R5) — Logs()
// already threads its DeployTargetLogsOpts unconditionally (extra["opts"]), which is all
// candy/plugin-deploy-pod's resolvePodLogsPlan needs (box/instance come from the deploy key on
// lifecycleParams.Name).
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
		})
	return true
}()
