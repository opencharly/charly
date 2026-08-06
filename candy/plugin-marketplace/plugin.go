// Package marketplace is the charly plugin housing the `charly marketplace …` CLI — the
// generator that regenerates the ENTIRE `charly-plugins` marketplace + harness surface from
// candy config (the plugins→candies migration's deliverable). It reads every candy's `skill:` /
// `hook:` / `marketplace:` kind entities, aggregates them by marketplace family, and emits the
// plugins/ corpus + .claude/ hooks + settings + the R0 dispatcher. `charly marketplace drift` is
// the fail-closed no-op gate.
//
// command:marketplace — `charly marketplace generate|drift`. This plugin is deliberately NOT
// listed in charly/charly.yml's compiled_plugins: it is a DEV-TIME generator, run on a
// contributor's machine to regenerate the harness surface, and has no business inside every
// shipped static charly binary. So it takes the OUT-OF-PROCESS placement (the plugin-docs
// model): the host prescans the declared `marketplace` word into the Kong grammar before parse
// and syscall.Exec's this binary in CLI mode (sdk.Main → CliMain) on first invocation.
//
// The generator is SELF-CONTAINED — it reads files and writes files, never reaching the host
// reverse channel — which is what makes the out-of-process placement free here. Invoke(OpRun)
// dispatches to the SAME entry point as CliMain, so the plugin stays placement-invisible even
// though out-of-process is the placement it actually ships in.
package marketplace

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.218.1200"

// NewProvider returns the marketplace command provider for out-of-proc serving (or in-proc
// registration, were the plugin ever listed in compiled_plugins).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:marketplace via sdk.NewMeta → BuildCapabilities. A command's args
// are pass-through CLI tokens, not a structured plugin_input, so the capability carries no
// InputDef and the plugin ships no CUE schema (the plugin-docs/plugin-alias precedent).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "marketplace"}},
		nil)
}

// CliMain is the OUT-OF-PROCESS command entry — the placement this plugin actually ships in.
func CliMain(args []string) int { return dispatchMarketplaceCLI(args) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves command:marketplace's Invoke(OpRun) for the compiled-in placement. The generator
// needs no reverse-channel executor (it only reads files and writes files), so this decodes the
// pass-through args and runs the very same dispatch CliMain does.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("marketplace: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("marketplace command: decode args: %w", err)
		}
	}
	if code := dispatchMarketplaceCLI(in.Args); code != 0 {
		return nil, fmt.Errorf("marketplace command: generation failed (exit %d)", code)
	}
	return &pb.InvokeReply{}, nil
}
