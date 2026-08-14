package helm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-helm/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// provider.go is the out-of-process provider for BOTH helm capabilities. Invoke
// branches on the request class:
//
//   - a "step" op drives the `step:helm-release` install step (the F3 external
//     step-kind): the host's OPEN DEFAULT ARM (invokeExternalStep) dispatches its
//     ops.OpExecute over the E3b reverse channel with the OPAQUE Payload (the
//     #HelmReleaseStep def) as params_json. The plugin dials the host executor
//     (sdk.ExecutorFromInvoke), runs `helm upgrade --install` IN-VENUE, and returns
//     a teardown ReverseOp (`helm uninstall`) the host records + replays
//     (record-and-replay). Emits=false, so ops.OpEmit is never served (a deploy-only
//     step) — the default arm returns an empty reply for it.
//   - every other op is the `helm:` check VERB: the host dispatches a `helm:` check
//     step through the registry (ResolveVerb("helm") → this grpcProvider →
//     invokeVerbProvider) with the FULL #Op marshaled as params_json and a CheckEnv
//     snapshot as env. The verb is EXEC-based (like wl/dbus): it drives the venue's
//     helm binary ONLY through the host's live executor over the E3b reverse channel,
//     so a missing broker is a HARD FAIL. Because the out-of-process verb path does
//     NOT run the host-side matcher pipeline, this Invoke OWNS the whole verdict:
//     dispatch the method, then evaluate the stdout/stderr/exit_status matchers itself
//     (via the shared sdk implementation — R3), and return the wire {status,message}
//     the host decodes.

// helmEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for a `helm:` check step (provider_checkenv.go) — only Mode matters
// here (helm probes a cluster's releases, not a container, so it needs no container
// resolution).
type helmEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one operation for the plugin's capabilities. The step leg decodes the
// opaque #HelmReleaseStep payload and runs the helm upgrade --install IN-VENUE; the
// verb leg decodes the full #Op + the desugared #HelmInput, skips in box mode (no
// cluster to probe on a disposable `charly check box`), dispatches the method, and
// self-evaluates the matchers.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "step" {
		return invokeHelmReleaseStep(ctx, req)
	}
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "helm: decode op: "+err.Error())
		}
	}
	var env helmEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The verb's method + method-exclusive modifiers ride the desugared plugin input
	// (the schema-compaction cutover moved every verb-exclusive field out of core #Op).
	var in params.HelmInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no cluster to reach on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("helm: %s requires a running cluster (skip under charly check box)", method))
	}

	// helm is EXEC-based: it drives the venue's helm binary ONLY through the host's
	// live executor over the E3b reverse channel. A missing broker is therefore a HARD
	// FAIL with a clear message, never a silent skip — the verb cannot do its job
	// without the venue.
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("helm: %s has no host executor attached — helm needs the live venue (%v)", method, err))
	}

	out, runErr := dispatchVerb(ctx, exec, &in)

	// The shared exit/stdout/stderr verdict pipeline (R3). helm produces no artifact.
	return sdk.VerbVerdict("helm", method, out, runErr, &op, false)
}

// invokeHelmReleaseStep serves the step:helm-release install step's ops.OpExecute:
// decode the opaque #HelmReleaseStep payload, dial the host executor, run the
// `helm upgrade --install` invocation IN-VENUE, and return the teardown ReverseOp
// (`helm uninstall`) the host records + replays. ops.OpEmit (the build leg) is never
// served — the step declares Emits=false (deploy-only) — so it returns an empty reply.
func invokeHelmReleaseStep(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() == "emit" {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	var in params.HelmReleaseStep
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-helm: decode step payload: %w", err)
		}
	}
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-helm: step:helm-release: reach host reverse channel: %w", err)
	}
	if err := runHelmUpgradeInstall(ctx, exec, &in); err != nil {
		return nil, fmt.Errorf("plugin-helm: step:helm-release: %w", err)
	}
	// Teardown: `helm uninstall` the release we just installed (record-and-replay).
	// Scope system — helm needs the kubeconfig + the release's namespace, both
	// operator-level, and the uninstall must survive the deploy user's teardown.
	uninstall := fmt.Sprintf("helm uninstall %s", spec.ShellQuote(in.Release))
	if in.Namespace != "" {
		uninstall += " -n " + spec.ShellQuote(in.Namespace)
	}
	reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeSystem, uninstall)}
	return sdk.BuildDeployReply(reverseOps, "plugin-helm", calver)
}
