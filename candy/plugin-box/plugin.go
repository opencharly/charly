// Package box is the charly plugin housing the build-mode `charly box …` verb HANDLERS that are
// NESTED under the core `box` command group (P15). It is a dual-placement COMMAND plugin (F8): the
// SAME NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in
// compiled_plugins (the canonical placement, P15), or cmd/serve serves them OUT-OF-PROCESS when
// they are not.
//
// It serves SIX command capabilities, all NESTED under the `box` parent (CommandParent()=="box",
// so `charly box generate/validate/new/pkg/inspect/list` parse + dispatch here while the retained
// core BoxCmd verbs — build/merge/labels/feature/the authoring verbs — stay in core):
//
//   - command:generate — `charly box generate`: builds a spec.BuildRequest and InvokeProvider's the
//     peer COMPILED-IN build:generate word (candy/plugin-build), which renders the .build/
//     Containerfile tree host-side over HostBuild("build-resolve", GenerateOnly). Zero core reentry.
//   - command:new — `charly box new candy/project/box`: calls kit.ScaffoldCandy / kit.ScaffoldProject
//     / kit.AddBox directly (the scaffold ENGINE already lives in sdk/kit). Zero core reentry.
//   - command:validate — `charly box validate`: reaches the hidden core `__box-validate` reentry over
//     HostBuild("cli") (the validation needs the fully-resolved project the plugin cannot load
//     pre-K1).
//   - command:pkg — `charly box pkg`: reaches the hidden core `__box-pkg` reentry over
//     HostBuild("cli") (the localpkg build engine needs the host build context, pre-K1).
//   - command:inspect — `charly box inspect`: reads the generic spec.ResolvedProject envelope
//     (HostBuild("resolved-project")) and prints the resolved box view — snake_case JSON by default,
//     scalar/box-aggregate fields per --format. The deploy-overlay formats (tunnel/bind_mounts) reenter
//     the hidden core `__box-inspect-overlay`. See inspect_list.go.
//   - command:list — `charly box list <sub>`: boxes/candies/targets/services/routes/volumes/aliases
//     from the same envelope; `list tags` reenters the hidden core `__box-list-tags` (podman store).
//
// COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun), so the handlers run in charly's OWN process
// and inherit charly's real stdio natively. It imports ONLY the sdk module, never charly core.
package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.194.0000"

// boxCommandWords is the set of command words this plugin serves — all nested under `box`.
var boxCommandWords = []string{"generate", "validate", "new", "pkg", "inspect", "list"}

// NewProvider returns the box command provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:generate/validate/new/pkg via sdk.NewMeta → BuildCapabilities so the
// COMPILED-IN path registers each as a command provider (the host builds its dynamic Kong grammar +
// dispatches Invoke(OpRun)). A command's args are pass-through CLI tokens, not a structured
// plugin_input, so the capabilities carry no InputDef and the plugin ships no schema.
func NewMeta() pb.PluginMetaServer {
	caps := make([]sdk.ProvidedCapability, 0, len(boxCommandWords))
	for _, w := range boxCommandWords {
		caps = append(caps, sdk.ProvidedCapability{Class: "command", Word: w})
	}
	return sdk.NewMeta(calver, caps, nil)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// The generate/validate/pkg handlers reach the host reverse channel (build:generate over
// InvokeProvider, __box-validate/__box-pkg over HostBuild("cli")), which is unavailable
// out-of-process, so this errors (like candy/plugin-vm's / candy/plugin-alias's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly box: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}

type provider struct{ pb.UnimplementedProviderServer }

// CommandParent is the optional interface buildUnitInProc detects on a compiled-in command
// plugin's provider (the SAME srv-interface-detection pattern registerCompiledPlugin uses for
// spec.DocParser / kit.RefsDownloader): every command word this plugin serves NESTS under the
// core `box` command group, so `charly box generate/validate/new/pkg` parse + dispatch here.
func (provider) CommandParent() string { return "box" }

// Invoke serves each box command's Invoke(OpRun): recover the reverse-channel executor, decode the
// pass-through args, and dispatch by the reserved command word. In-proc dispatch runs in charly's
// own process, so the handlers inherit charly's real stdin/stdout/stderr natively.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("box: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	word := req.GetReserved()
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("box %s: reach host reverse channel: %w", word, err)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("box %s: decode args: %w", word, err)
		}
	}
	if rerr := dispatchBoxCommand(&hostClient{ctx: ctx, exec: exec}, word, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
