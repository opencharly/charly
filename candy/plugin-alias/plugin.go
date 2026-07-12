// Package alias is the charly plugin housing the `charly alias …` CLI — the host command
// alias manager (create / install / list / remove / uninstall wrapper scripts that shell into a
// box). It is a dual-placement COMMAND plugin (F8): the SAME NewProvider()/NewMeta()/CliMain
// compile INTO charly in-process when listed in compiled_plugins (the canonical placement, P14),
// or cmd/serve serves them OUT-OF-PROCESS when they are not.
//
// command:alias — `charly alias add/install/list/remove/uninstall`. COMPILED-IN, it dispatches
// IN-PROC via Invoke(OpRun) (runAliasCommand → kong-parse the AliasCmd tree — alias.go), so the
// handlers run in charly's OWN process and inherit charly's real stdio natively. The two handlers
// that need host image facts — `add` (does the box image exist locally) and `install` (read the
// baked ai.opencharly.alias label) — reach the host over the generic HostBuild("cli") reverse
// channel by re-running `charly box labels …` (the same seam pod/vm lifecycles use), so the plugin
// owns ONLY the alias-script logic and imports the sdk module alone, never charly core.
package alias

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.193.1052"

// NewProvider returns the alias command provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:alias via sdk.NewMeta → BuildCapabilities so the COMPILED-IN path
// registers it as a command provider (the host builds its dynamic Kong grammar + dispatches
// Invoke(OpRun)). A command's args are pass-through CLI tokens, not a structured plugin_input, so
// the capability carries no InputDef and the plugin ships no schema.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "alias"}},
		nil)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in
// placement. command:alias's `add`/`install` handlers reach the host reverse channel (image-label
// facts), which is unavailable out-of-process, so this errors (like candy/plugin-vm's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly alias: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves command:alias's Invoke(OpRun): recover the reverse-channel executor, decode the
// pass-through args, and run the AliasCmd tree (alias.go). In-proc dispatch runs in charly's own
// process, so the handlers inherit charly's real stdin/stdout/stderr natively.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("alias: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("alias command: reach host reverse channel: %w", err)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("alias command: decode args: %w", err)
		}
	}
	if rerr := dispatchAliasCLI(&hostClient{ctx: ctx, exec: exec}, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
