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
// candy/plugin-fleet/host_seams.go.
//
// NOT every pod command needs this seam: `restart` (pod_cmd.go) is pure sdk/kit + sdk/deploykit
// logic (deploykit.RestartPodService) with zero registry coupling, so it calls deploykit directly —
// no HostBuild round-trip. Only the genuinely registry/type-bound bodies route through here.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// cmdHostEnvJSON carries the host-side spec.HostEnv (CharlyBin/Home/Version) threaded as DATA on
// the OpRun dispatch (charly/provider_command_external.go's dispatchInProcCommand — core computes
// it, since os.Executable() is only correct in-core, R10 bed-found bug #5). The config setup/remove
// leaves forward it verbatim into PodConfigSetupRequest.HostEnvJSON (deploy:pod's encrypted-mount
// ExecStartPre CharlyBin line) when they dispatch deploy:pod peer-to-peer — the SAME data-threading
// idiom candy/plugin-fleet's from_box_pod.go uses.
var cmdHostEnvJSON json.RawMessage

// dispatchPodConfigOp reaches deploy:pod's sdk.OpConfigSetup/OpConfigRemove peer-to-peer (the
// "pod-config-setup"/"pod-config-remove" host-build seams are DELETED, K-wave 2 cone R3): the
// config orchestration lives in candy/plugin-deploy-pod, so the CLI grammar forwards the wire
// request DIRECTLY via InvokeProvider — the same peer-dispatch idiom candy/plugin-fleet's
// from_box_pod.go uses for the source-less from-box path. A compiled-in COMMAND's own reverse
// channel carries no venue executor, so the host must re-materialize one from a SELF-DESCRIBED
// venue (the S1 seam — the same `spec.VenueDescriptor{Kind: "shell"}` the deleted core seam
// passed as `specexec.ShellExecutor{}`); deploy:pod's Invoke handler reads the threaded executor
// for its HostBuild callbacks.
func dispatchPodConfigOp(op string, reqJSON []byte) ([]byte, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("pod config: no host reverse channel (command not compiled-in?)")
	}
	opts := sdk.InvokeProviderOpts{VenueDescriptor: &spec.VenueDescriptor{Kind: "shell"}}
	resJSON, err := cmdExec.InvokeProvider(cmdCtx, "deploy", "pod", op, reqJSON, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("deploy:pod config: %w", err)
	}
	return resJSON, nil
}

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
