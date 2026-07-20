package pod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
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
