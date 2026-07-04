package main

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
)

// plugin_dispatch_reverse.go — the F10 reverse legs on ExecutorService: PLUGIN↔PLUGIN dispatch
// (InvokeProvider) + HOST-BUILD (HostBuild). Both are served on the SAME broker InvokeWithExecutor
// stands up for the calling plugin's Invoke, so any plugin running with a reverse channel
// (deploy/step/check/build) can reach them — the generalization of the RunHostStep ExternalPlugin
// arm (one fixed OpExecute step) to "invoke ANY provider/op" + "request a host build".

// InvokeProvider dispatches op on another provider (class, reserved) on the calling plugin's
// behalf (F10) — the host is the dispatch BROKER (plugin→host→plugin), since it owns the registry.
// An OUT-OF-PROCESS target is Invoked WITH the SAME venue executor + build context threaded onto a
// fresh nested broker (executorInvoker.InvokeWithExecutor — the generalization of invokeStepExecute
// from OpExecute-only to any op); an IN-PROC target (compiled-in/builtin) is Invoked directly. The
// target must already be registered (loaded at deploy/check); an unresolved word is a loud error.
func (s *executorReverseServer) InvokeProvider(ctx context.Context, req *pb.InvokeProviderRequest) (*pb.InvokeReply, error) {
	class := ProviderClass(req.GetClass())
	word := req.GetReserved()
	prov, ok := providerRegistry.resolve(class, word)
	if !ok {
		return nil, fmt.Errorf("InvokeProvider: no provider registered for %s:%s (the target plugin must be loaded before a peer invokes it)", class, word)
	}
	op := &Operation{Reserved: word, Op: req.GetOp(), Params: req.GetParamsJson(), Env: req.GetEnvJson()}
	var (
		res *Result
		err error
	)
	if inv, isInv := prov.(executorInvoker); isInv {
		// OUT-OF-PROCESS target: thread the SAME venue executor + build onto a nested reverse
		// channel (the nested-broker round-trip — the one-level RunHostStep ExternalPlugin arm,
		// generalized to any class/op).
		res, err = inv.InvokeWithExecutor(ctx, op, s.exec, s.build, s.rebootable, nil)
	} else {
		// IN-PROC target (compiled-in / builtin): a direct Invoke, no broker needed.
		res, err = prov.Invoke(ctx, op)
	}
	if err != nil {
		return nil, fmt.Errorf("InvokeProvider %s:%s op=%s: %w", class, word, op.Op, err)
	}
	if res == nil {
		return &pb.InvokeReply{}, nil
	}
	return &pb.InvokeReply{ResultJson: res.JSON}, nil
}

// HostBuild runs the registered host-builder for kind on the calling plugin's behalf (F10) — the
// build ENGINE stays in core (podman/toolchain/Generator), so a plugin REQUESTS a host-side build
// and gets the builder's opaque result. The generalization of the RunHostStep per-step build legs
// to a standalone build request. M13/M14 register the image/kustomize builders onto this seam.
func (s *executorReverseServer) HostBuild(ctx context.Context, req *pb.HostBuildRequest) (*pb.HostBuildReply, error) {
	fn, ok := hostBuilderFor(req.GetKind())
	if !ok {
		return &pb.HostBuildReply{Error: fmt.Sprintf("no host-builder registered for kind %q", req.GetKind())}, nil
	}
	result, err := fn(ctx, req.GetSpecJson(), s.build)
	if err != nil {
		return &pb.HostBuildReply{Error: err.Error()}, nil
	}
	return &pb.HostBuildReply{ResultJson: result}, nil
}

// hostBuilder runs a host-side build for one kind: it interprets specJSON, runs the build engine
// (with the host buildEngineContext), and returns the opaque result JSON. The seam M13/M14 register
// the image/kustomize builders onto.
type hostBuilder func(ctx context.Context, specJSON []byte, build buildEngineContext) ([]byte, error)

// hostBuilders maps a HostBuild kind → its host-side builder. Populated at package-var init time
// (before any init(), like the substrate/preresolver registries), so the lookup is race-free.
var hostBuilders = map[string]hostBuilder{}

// registerHostBuilder records one host-builder kind (F10). Panics on a duplicate (a startup
// invariant, like registerSubstrateLifecycle / registerDeployPreresolver).
func registerHostBuilder(kind string, fn hostBuilder) {
	if kind == "" || fn == nil {
		panic("registerHostBuilder: empty kind or nil builder")
	}
	if _, dup := hostBuilders[kind]; dup {
		panic(fmt.Sprintf("registerHostBuilder: duplicate host-builder for %q", kind))
	}
	hostBuilders[kind] = fn
}

// hostBuilderFor returns the registered host-builder for kind, if any.
func hostBuilderFor(kind string) (hostBuilder, bool) {
	fn, ok := hostBuilders[kind]
	return fn, ok
}

// hostBuildPluginBinary is the "plugin-binary" host-builder (F10): build a candy's plugin provider
// binary on the host (buildPluginBinary — go build on the host toolchain), returning {"path": …}.
// The concrete host-build proving the HostBuild capability; M13/M14 register "kustomize"/"image".
func hostBuildPluginBinary(ctx context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var spec struct {
		CandyDir string `json:"candy_dir"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("plugin-binary host-build: decode spec: %w", err)
	}
	if spec.CandyDir == "" || spec.Name == "" {
		return nil, fmt.Errorf("plugin-binary host-build: spec requires candy_dir + name")
	}
	bin, err := buildPluginBinary(ctx, spec.CandyDir, spec.Name)
	if err != nil {
		return nil, err
	}
	return marshalJSON(map[string]string{"path": bin})
}

// Register the plugin-binary host-builder at package-var init (before any init()), like the
// substrate/preresolver registries.
var _ = func() bool { registerHostBuilder("plugin-binary", hostBuildPluginBinary); return true }()
