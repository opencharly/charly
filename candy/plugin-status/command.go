package status

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// command.go — the externalized `charly status` command. The plugin OWNS the Kong grammar
// (moved from charly/status.go's StatusCmd) + the declared-nested-tree pre-resolution
// (nested_tree.go, K5) + the PURE nested-overlay fold (overlay.go) + the render output
// (render.go). Only the LIVE per-substrate collection (pod/vm/k8s/local/android) stays host-side,
// reached via the generic "status-substrate" HostBuild seam (charly/status_substrate_host.go).
//
// status is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process
// and gets the in-proc reverse channel (dispatchInProcCommand threads it), so
// HostBuild("status-substrate") reaches the host collection engine. The out-of-process CliMain
// path has no reverse channel, so it errors.

// StatusCmd is the `charly status` Kong grammar — moved verbatim from charly/status.go.
type StatusCmd struct {
	Box      string `arg:"" optional:"" help:"Box name (omit to list all charly containers)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	All      bool   `short:"a" long:"all" help:"Include enabled-but-not-running services"`
	Nested   bool   `long:"nested" help:"Probe nested children + live k8s workloads (multi-hop, slower)"`
	JSON     bool   `long:"json" help:"Output as JSON"`
}

// runStatusCLI parses the pass-through args into StatusCmd (kong, mirroring plugin-settings' CLI
// parse — StatusCmd carries real flags rather than settings' subcommand tokens, so it uses kong to
// reparse rather than a manual switch, mirroring plugin-migrate's MigrateCmd), canonicalizes the
// deploy positional (the SAME deploykit.CanonicalizeDeployArg every deploy-arg CLI verb calls),
// drives the "status-substrate" HostBuild seam, applies the PURE nested overlay (bulk path only —
// the single-box detail path never carried nested children), and renders exactly as the former
// in-core StatusCmd.Run did.
func runStatusCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	var cmd StatusCmd
	if done, err := sdk.ParseInProcCLI("status", &cmd, args,
		kong.Description("Show service status (all if no box given)")); err != nil || done {
		return err
	}

	box, instance := deploykit.CanonicalizeDeployArg(cmd.Box, cmd.Instance)

	if box == "" {
		reply, herr := hostStatusSubstrate(ctx, exec, spec.StatusSubstrateRequest{
			IncludeAll: cmd.All,
		})
		if herr != nil {
			return herr
		}
		roots, rerr := buildStatusRootsTree(exec, ctx, cmd.Nested)
		if rerr != nil {
			return rerr
		}
		rows := applyNestedOverlay(reply.Rows, roots)
		if cmd.JSON {
			return RenderJSON(os.Stdout, rows)
		}
		if len(rows) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "No charly containers found")
			return nil
		}
		return RenderTable(os.Stdout, rows)
	}

	reply, herr := hostStatusSubstrate(ctx, exec, spec.StatusSubstrateRequest{
		Single:   true,
		Box:      box,
		Instance: instance,
	})
	if herr != nil {
		return herr
	}
	if cmd.JSON {
		return RenderJSONOne(os.Stdout, reply.Single)
	}
	return RenderDetail(os.Stdout, reply.Single)
}

// hostStatusSubstrate runs the status-collection engine over the generic "status-substrate"
// HostBuild kind. exec is nil on the out-of-process CliMain path (no reverse channel) → a clear
// error, mirroring plugin-settings' hostSettings.
func hostStatusSubstrate(ctx context.Context, exec *sdk.Executor, req spec.StatusSubstrateRequest) (spec.StatusSubstrateReply, error) {
	if exec == nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("charly status requires compiled-in placement (the status-substrate host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.StatusSubstrateReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "status-substrate", reqJSON)
	if err != nil {
		return spec.StatusSubstrateReply{}, err
	}
	var reply spec.StatusSubstrateReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return spec.StatusSubstrateReply{}, uerr
	}
	return reply, nil
}
