package candy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:candy placement. The host's
// command dispatch (dispatchInProcCommand) invokes this in-process with the pass-through args. candy is
// SELF-CONTAINED (it mutates candy/<name>/charly.yml via the generic kit yaml utilities), so — unlike
// clean/settings — it uses NO reverse channel; Invoke and CliMain both just run runCandyCLI.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly candy` in-process for the compiled-in command:candy placement: it decodes the
// pass-through args and runs the candy logic directly. It RETURNS the error so a non-zero exit
// propagates.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-candy: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-candy: decode args: %w", err)
		}
	}
	if err := runCandyCLI(in.Args); err != nil {
		return nil, err
	}
	return &pb.InvokeReply{}, nil
}
