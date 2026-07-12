package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
)

// host_seams.go — the command:bundle plugin's bridge to the host. The bundle CLI handlers moved out
// of charly core (P13); the config loader + deploy ledger + the deploy-dispatch kernel (ResolveTarget
// → externalDeployTarget over the executor reverse channel) are core Mechanisms a plugin cannot
// import (separate module), so the handlers reach them over the in-proc reverse channel: config →
// HostBuild("config-resolve"), ledger writes → HostBuild("config-persist"), the ResolveTarget +
// tree-walk + executor-threading + Add dispatch → HostBuild("deploy-apply"), generic charly reentry
// → HostBuild("cli"). command:bundle is COMPILED-IN and dispatches exactly ONE `charly bundle …`
// invocation per process, so the reverse-channel executor is stashed in a package var at
// Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process. Mirrors
// candy/plugin-vm/vm_host_seams.go.

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

// hostDeploySeam is the ONE bridge (R3) every deploy-driving `charly bundle …` leaf uses to
// reach the host: it JSON-marshals the wire request and forwards it to the named host-build
// seam (deploy-add / deploy-del / deploy-from-box / deploy-config) over the in-proc reverse
// channel, where the host reconstructs the core orchestration struct and runs its Run() logic
// VERBATIM. The reply is always empty — the host prints host-side (compiled-in ⇒ charly's own
// stdio) and signals failure via the error return. Mirrors candy/plugin-vm/vm_build.go's
// HostBuild call pattern.
func hostDeploySeam(kind string, reqAny any) error {
	if cmdExec == nil {
		return fmt.Errorf("bundle %s: no host reverse channel (command not compiled-in?)", kind)
	}
	reqJSON, err := json.Marshal(reqAny)
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, kind, reqJSON)
	return err
}
