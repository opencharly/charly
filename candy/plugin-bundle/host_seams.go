package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
)

// host_seams.go — the command:bundle plugin's bridge to the host. The bundle CLI handlers moved out
// of charly core (P13). `add`/`del` now drive their WHOLE deploy-tree walk plugin-side (walk.go,
// the K4-C walk port) — LoadUnified-coupled config resolution (resolveTreeRoot/resolveDelNode) and
// registry-coupled executor-chain derivation (deriveChildExecutorForPath) are core Mechanisms a
// plugin cannot import (separate module), so the walk reaches them via six narrow host-build
// seams: deploy-tree-resolve, deploy-node-dispatch (the per-node compile+ResolveTarget+Add
// terminal step), deploy-members-up/-down, deploy-del-resolve, and deploy-node-del-dispatch (the
// per-node ResolveTarget+Del terminal step). `from-box` still forwards its WHOLE command to
// HostBuild("deploy-from-box"), and the whole-file config-management ops (show/export/import/
// reset/status) reach the host via the narrow HostBuild("deploy-config-save") seam alone — both
// running the existing core orchestration VERBATIM. command:bundle is COMPILED-IN and dispatches
// exactly ONE `charly bundle …` invocation per process, so the reverse-channel executor is stashed
// in a package var at Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process.
// Mirrors candy/plugin-vm/vm_host_seams.go.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly bundle …`
// dispatch. Called once at the top of command:bundle's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

// hostDeploySeam is the reply-less form of hostDeploySeamJSON below — every deploy-driving
// `charly bundle …` leaf that needs no reply data (from-box, the config-management ops) uses it.
// Mirrors candy/plugin-vm/vm_build.go's HostBuild call pattern.
func hostDeploySeam(kind string, reqAny any) error {
	return hostDeploySeamJSON(kind, reqAny, nil)
}

// hostDeploySeamJSON is hostDeploySeam's reply-carrying sibling (K4-C walk port): the SAME
// marshal/HostBuild/error-return contract, additionally json-unmarshaling the reply into
// replyOut when non-nil (a *spec.DeployTreeResolveReply, *spec.DeployDelResolveReply, …).
func hostDeploySeamJSON(kind string, reqAny any, replyOut any) error {
	if cmdExec == nil {
		return fmt.Errorf("bundle %s: no host reverse channel (command not compiled-in?)", kind)
	}
	reqJSON, err := json.Marshal(reqAny)
	if err != nil {
		return err
	}
	resJSON, err := cmdExec.HostBuild(cmdCtx, kind, reqJSON)
	if err != nil {
		return err
	}
	if replyOut == nil || len(resJSON) == 0 {
		return nil
	}
	return json.Unmarshal(resJSON, replyOut)
}
