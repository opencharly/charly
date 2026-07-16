// Package authoring is the charly plugin housing the `charly box …` AUTHORING verb
// HANDLERS — the project charly.yml mutation + remote-repo-cache surface (P14b). It is a
// dual-placement COMMAND plugin (F8): the SAME NewProvider()/NewMeta()/CliMain compile INTO
// charly in-process when listed in compiled_plugins (the canonical placement, P14b), or
// cmd/serve serves them OUT-OF-PROCESS when they are not.
//
// It serves SEVEN command capabilities, all NESTED under the `box` parent (CommandParent()=="box",
// so `charly box set/add-candy/rm-candy/fetch/refresh/write/cat` parse + dispatch here while the
// retained core BoxCmd verbs — build/merge/pull/labels/feature/reconcile — stay in core, and the
// P15 candy/plugin-box owns generate/validate/new/pkg/inspect/list):
//
//   - command:set — `charly box set <dotpath> <value>`: calls kit.SetByDotPath directly (the
//     comment-preserving yaml.Node dot-path writer already lives in sdk/kit). Zero core reentry.
//   - command:add-candy / command:rm-candy — `charly box add-candy/rm-candy <box> <candy>`: the
//     yaml.Node candy-list EDIT helpers (addCandyToBox / removeCandyFromBox, authoring.go) walk
//     *yaml.Node trees via kit.MappingChild / kit.SaveYAMLNodeFile — the SAME utilities
//     candy/plugin-candy uses. Zero core reentry.
//   - command:write / command:cat — `charly box write/cat <rel-path>`: the project-rooted file
//     escape hatch (resolveProjectFile path-traversal guard, authoring.go). Pure stdlib. Zero
//     core reentry.
//   - command:fetch / command:refresh — `charly box fetch/refresh [<spec>]`: the remote-repo
//     cache pre-primer / force-re-clone. These reach the host-coupled repo resolver
//     (ResolveProjectRepo → EnsureRepoDownloaded: CHARLY_REPO_OVERRIDE + the refs-backend
//     dispatch + the command:migrate auto-migration) over the generic HostBuild("cli") reverse
//     channel by re-running the hidden core `__box-fetch` / `__box-refresh` reentry (the SAME
//     seam candy/plugin-box's `pkg` verb uses for `__box-pkg`). The plugin owns ONLY the
//     dispatch + the reentry call; it imports the sdk module alone, never charly core.
//
// COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun), so the handlers run in charly's OWN process
// and inherit charly's real stdio natively. It imports ONLY the sdk module, never charly core.
package authoring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.196.0000"

// authoringCommandWords is the set of command words this plugin serves — all nested under `box`.
var authoringCommandWords = []string{"set", "add-candy", "rm-candy", "fetch", "refresh", "write", "cat"}

// NewProvider returns the authoring command provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:set/add-candy/rm-candy/fetch/refresh/write/cat via sdk.NewMeta →
// BuildCapabilities so the COMPILED-IN path registers each as a command provider (the host builds
// its dynamic Kong grammar + dispatches Invoke(OpRun)). A command's args are pass-through CLI
// tokens, not a structured plugin_input, so the capabilities carry no InputDef and the plugin
// ships no schema.
func NewMeta() pb.PluginMetaServer {
	caps := make([]sdk.ProvidedCapability, 0, len(authoringCommandWords))
	for _, w := range authoringCommandWords {
		caps = append(caps, sdk.ProvidedCapability{Class: "command", Word: w})
	}
	return sdk.NewMeta(calver, caps, nil)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// The fetch/refresh handlers reach the host reverse channel (the __box-fetch/__box-refresh reentry
// over HostBuild("cli")), which is unavailable out-of-process, so this errors (like
// candy/plugin-box's / candy/plugin-alias's CliMain). The pure authoring verbs (set/add-candy/
// rm-candy/write/cat) would work out-of-process, but a command plugin is compiled-in by default.
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly box: authoring verbs require compiled-in placement (fetch/refresh reach the host reverse channel, unavailable out-of-process)")
	return 1
}

type provider struct{ pb.UnimplementedProviderServer }

// CommandParent is the optional interface buildUnitInProc detects on a compiled-in command
// plugin's provider: every command word this plugin serves NESTS under the core `box` command
// group, so `charly box set/add-candy/rm-candy/fetch/refresh/write/cat` parse + dispatch here.
func (provider) CommandParent() string { return "box" }

// Invoke serves the authoring commands' Invoke(OpRun): recover the reverse-channel executor,
// decode the pass-through args, dispatch by the reserved command word. In-proc dispatch runs in
// charly's own process (native stdio).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("authoring: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("authoring command: reach host reverse channel: %w", err)
	}
	word := req.GetReserved()
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if uerr := json.Unmarshal(req.GetParamsJson(), &in); uerr != nil {
			return nil, fmt.Errorf("authoring %s: decode args: %w", word, uerr)
		}
	}
	hc := &hostClient{ctx: ctx, exec: exec}
	if rerr := dispatchAuthoringCommand(hc, word, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// hostClient is the authoring commands' ONE host coupling: it reaches charly's host process over
// the generic HostBuild("cli") reverse channel (the same seam candy/plugin-box's `pkg` verb uses)
// to run the hidden core `__box-fetch` / `__box-refresh` reentry — the only host dependency the
// `fetch`/`refresh` verbs need (the repo resolver is host-coupled: registry + override + migration).
// Every other verb (set/add-candy/rm-candy/write/cat) is a pure sdk/kit + stdlib operation.
type hostClient struct {
	ctx  context.Context
	exec *sdk.Executor
}

// cli asks the HOST to run `charly <argv>` via the generic "cli" host-builder and returns the
// CliReply (stdout when capture, the process exit code, any spawn error). Mirrors the box/alias
// plugins' host coupling (R3 in shape; each plugin owns its own reverse-channel calls).
func (h *hostClient) cli(capture, bestEffort bool, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture, BestEffort: bestEffort})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := h.exec.HostBuild(h.ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	return r, nil
}
