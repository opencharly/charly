package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
)

// host_seams.go — the command:fleet plugin's bridge to the host. The fleet CLI handlers moved out
// of charly core (P13). `add`/`del` now drive their WHOLE deploy-tree walk plugin-side (walk.go,
// the K4-C walk port) — LoadUnified-coupled config resolution (the merged-tree read/resolveDelNode) and
// registry-coupled executor-chain derivation (deriveChildExecutorForPath) are core Mechanisms a
// plugin cannot import (separate module), so the walk reaches them via three narrow host-build
// seams: deploy-plugins-connect, resolve-target-add (the per-node ResolveTarget+Add terminal step —
// the plugin COMPILES the InstallPlans IN-PROC (K4-C shape-2) and ships them already-compiled, so
// the host half does only ResolveTarget+Add, no compile), and
// deploy-node-del-dispatch (the per-node ResolveTarget+Del terminal step; the del RESOLUTION runs
// plugin-side, del_resolve.go — the deploy-del-resolve seam is DELETED, K-wave 2 cone R2 bank C).
// The former deploy-members-up/-down seams DIED (#55 W3 A4) — the walk calls sdk/deploykit.BringUpMembers/
// TearDownMembers directly now. `from-box` is fully plugin-side since K-wave 2 cone R2 (the
// "deploy-from-box" HostBuild seam is DELETED — runFromBoxPod reaches deploy:pod's OpConfigSetup
// by direct InvokeProvider). The config-management ops (show/export/import/reset/status) run
// plugin-side — reads via loaderkit.LoadHostFleetConfigViaExecutor, writes via deploykit.SaveFleetConfig
// directly (#55 K4 config-write seam-collapse; the host "deploy-config-save" leg is deleted).
// command:fleet is COMPILED-IN and dispatches
// exactly ONE `charly fleet …` invocation per process, so the reverse-channel executor is stashed
// in a package var at Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process.
// Mirrors candy/plugin-vm/vm_host_seams.go.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// cmdHostEnvJSON carries the host-side spec.HostEnv (CharlyBin/Home/Version) threaded as DATA on
// the OpRun dispatch (charly/provider_command_external.go's dispatchInProcCommand — core computes
// it, since os.Executable() is only correct in-core, R10 bed-found bug #5). The from-box pod path
// forwards it verbatim into PodConfigSetupRequest.HostEnvJSON (deploy:pod's encrypted-mount
// ExecStartPre CharlyBin line) instead of computing its own.
var cmdHostEnvJSON json.RawMessage

// setCommandContext stashes the reverse-channel executor for the duration of one `charly fleet …`
// dispatch. Called once at the top of command:fleet's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

// hostDeploySeamJSON is the host reverse-channel deploy seam (K4-C walk port): the SAME
// marshal/HostBuild/error-return contract, additionally json-unmarshaling the reply into
// replyOut when non-nil (a *spec.DeployPluginsConnectReply, *spec.DeployDelResolveReply, …).
func hostDeploySeamJSON(kind string, reqAny any, replyOut any) error {
	if cmdExec == nil {
		return fmt.Errorf("fleet %s: no host reverse channel (command not compiled-in?)", kind)
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
