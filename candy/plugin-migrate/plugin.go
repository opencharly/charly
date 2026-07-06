// Package migrate is the COMPILED-IN charly plugin owning the config-schema migration
// engine (M15). It advertises command:migrate — the `charly migrate` operator command AND
// the in-proc engine charly's remote-cache auto-migration (refs.go) invokes with OpRun. The
// CUE-anchored declarative migration table + the generic op-walker + the file-walk drivers
// (engine.go) live HERE now, out of charly's core; #Migration is read from the SDK schema
// (sdk/schema), and the migration DATA (migrations.cue) is embedded here. Same
// NewProvider()/NewMeta() + CliMain() shape as every command plugin (plugin-preempt); it
// JOINS compiled_plugins so command:migrate resolves at init() independent of any config —
// migrate must run when the config is exactly what cannot load.
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

const calver = "2026.186.0100"

// NewProvider returns the migrate command provider (the in-proc OpRun dispatch surface the
// host's `charly migrate` + refs.go auto-migration invoke).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:migrate + the plugin's self-contained CUE schema via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "migrate"}},
		nil)
}

// CliMain is the plugin's CLI entrypoint (command:migrate dispatch — `charly migrate …`).
func CliMain(args []string) int {
	if err := runMigrateCLI(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly migrate` in-process for the compiled-in command:migrate placement:
// the host's command dispatch AND refs.go's remote-cache auto-migration (OpRun with
// params {"args": [...]}). It RETURNS the engine error (unlike a fire-and-forget verb) so a
// below-floor refusal propagates a non-zero exit / a failed cache auto-migration.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("migrate: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	if w := req.GetReserved(); w != "migrate" {
		return nil, fmt.Errorf("migrate: unexpected reserved word %q", w)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("migrate: decode args: %w", err)
		}
	}
	if err := runMigrateCLI(in.Args); err != nil {
		return nil, err
	}
	return &pb.InvokeReply{}, nil
}
