// Package docs is the charly plugin housing the `charly docs …` CLI — the generator that
// renders the opencharly.ai documentation site's REFERENCE half from this repo's canonical
// sources (VISION.md, the plugins submodule's skill corpus, every candy/box charly.yml, and
// every plugin candy's `plugin:` block + CUE schema).
//
// command:docs — `charly docs generate`. This plugin is deliberately NOT listed in
// charly/charly.yml's compiled_plugins: it is a DEV-TIME documentation generator, run on a
// contributor's machine to regenerate the site, and has no business inside every shipped
// static charly binary. So it takes the OUT-OF-PROCESS placement: the host prescans the
// declared `docs` word into the Kong grammar before parse and syscall.Exec's this binary in
// CLI mode (sdk.Main → CliMain) on the first actual `charly docs …` invocation.
//
// The generator is SELF-CONTAINED — it reads files and writes markdown, and never reaches the
// host reverse channel — which is exactly what makes the out-of-process placement free here
// (the same property that lets `charly candy` and `charly migrate` run in either placement).
// Invoke(OpRun) therefore dispatches to the SAME entry point as CliMain, so the plugin stays
// placement-invisible even though out-of-process is the placement it actually ships in.
//
// Why the generator reads DECLARATIVE sources rather than charly's assembled CLI model: the
// host's Kong grammar renders every dynamic command word with a generic stub description, the
// depth-1 `--help` is intercepted by the host, and plugin-served help comes in at least three
// mutually incompatible formats (kong trees, custom usage lines, and parse errors). Parsing
// those would be precisely the fragile shim R4 forbids. Every fact the site needs is already
// declared: `plugin.providers`, the per-plugin schema/*.cue, and each candy's description:.
package docs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.215.1140"

// NewProvider returns the docs command provider for out-of-proc serving (or in-proc
// registration, were the plugin ever listed in compiled_plugins).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:docs via sdk.NewMeta → BuildCapabilities. A command's args are
// pass-through CLI tokens, not a structured plugin_input, so the capability carries no InputDef
// and the plugin ships no CUE schema (the candy/plugin-alias precedent).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "docs"}},
		nil)
}

// CliMain is the OUT-OF-PROCESS command entry — the placement this plugin actually ships in.
// charly syscall.Exec's this binary in CLI mode, so the generator owns real stdio and reports
// progress straight to the contributor's terminal.
func CliMain(args []string) int { return dispatchDocsCLI(args) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves command:docs's Invoke(OpRun) for the compiled-in placement. The generator needs
// no reverse-channel executor (it only reads files and writes markdown), so this decodes the
// pass-through args and runs the very same dispatch CliMain does.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("docs: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("docs command: decode args: %w", err)
		}
	}
	if code := dispatchDocsCLI(in.Args); code != 0 {
		return nil, fmt.Errorf("docs command: generation failed (exit %d)", code)
	}
	return &pb.InvokeReply{}, nil
}
