package pod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// host_seams.go — the command:{start,stop,logs,shell,service,config,remove,cp,volume} plugin's
// bridge to the host. Their bodies moved out of charly core (the DEPLOY-wave CLI-struct port); the
// provider REGISTRY (ResolveTarget, the plugin loader) is a core Mechanism a plugin cannot import
// (separate module) or hold (host-only by construction), so the registry-bound handlers reach it
// over the in-proc reverse channel via per-command host-build seams — each running the existing
// core orchestration VERBATIM. command:pod is COMPILED-IN and dispatches exactly ONE `charly
// <word> …` invocation per process, so the reverse-channel executor is stashed in a package var at
// Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process. Mirrors
// candy/plugin-bundle/host_seams.go.
//
// NOT every pod command needs this seam: `restart` (pod_cmd.go) is pure sdk/kit + sdk/deploykit
// logic (deploykit.RestartPodService) with zero registry coupling, so it calls deploykit directly —
// no HostBuild round-trip. Only the genuinely registry/type-bound bodies route through here.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly <word> …`
// dispatch. Called once at the top of command:pod's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

// hostPodSeam is the ONE bridge (R3) every registry-driving `charly <word> …` leaf uses to reach
// the host: it JSON-marshals the wire request and forwards it to the named host-build seam over
// the in-proc reverse channel, where the host reconstructs the core orchestration struct and runs
// its Run() logic VERBATIM. The reply is always empty — the host prints host-side (compiled-in ⇒
// charly's own stdio) and signals failure via the error return.
func hostPodSeam(kind string, reqAny any) error {
	if cmdExec == nil {
		return fmt.Errorf("pod %s: no host reverse channel (command not compiled-in?)", kind)
	}
	reqJSON, err := json.Marshal(reqAny)
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, kind, reqJSON)
	return err
}

// hostPodLifecycle marshals payload (one of the #PodXPayload types) into
// spec.PodLifecycleRequest.Payload and forwards it via hostPodSeam("pod-lifecycle", …) — the ONE
// wire request every pod-lifecycle op (start/stop/shell/logs/service/cmd/update/remove) now
// shares (#55 W3 A10b unified the former 8 dedicated per-verb request types + HostBuild kinds into
// this single op-discriminated one, converging on the codebase's own established wire idiom —
// #ArbiterInvokeInput, charly/provider.go's own Operation.Params). node is nil for update (which
// threads a whole merged tree instead, in its own payload) and remove (which needs none).
func hostPodLifecycle(op, box, instance string, node *spec.Deploy, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return hostPodSeam("pod-lifecycle", spec.PodLifecycleRequest{Op: op, Box: box, Instance: instance, Node: node, Payload: b})
}

// hostPodSeamReply is hostPodSeam's reply-capturing sibling (R3: mirrors
// candy/plugin-deploy-pod/config_setup.go's identically-shaped `hostBuild` helper — that one is
// package-private to plugin-deploy-pod, so this module needs its own copy rather than an import)
// for the narrow case where the plugin itself must ACT on a seam's result (e.g.
// resolveContainerTunnel in remove_tunnel.go, which the plugin then drives via InvokeProvider)
// instead of letting the host print + report pass/fail alone.
func hostPodSeamReply(kind string, reqAny, replyPtr any) error {
	if cmdExec == nil {
		return fmt.Errorf("pod %s: no host reverse channel (command not compiled-in?)", kind)
	}
	reqJSON, err := json.Marshal(reqAny)
	if err != nil {
		return err
	}
	resJSON, err := cmdExec.HostBuild(cmdCtx, kind, reqJSON)
	if err != nil {
		return err
	}
	if replyPtr == nil || len(resJSON) == 0 {
		return nil
	}
	return json.Unmarshal(resJSON, replyPtr)
}
