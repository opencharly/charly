package ollama

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:ollama placement. The
// host's command dispatch (dispatchInProcCommand) invokes this in-process with the
// pass-through args. The command is SELF-CONTAINED (plain HTTP to the Ollama server), so it
// uses NO reverse channel; Invoke and CliMain both just run runOllamaCLI.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly ollama` in-process for the compiled-in command:ollama placement: it
// decodes the pass-through args and runs the ollama logic directly. It RETURNS the error so
// a non-zero exit propagates.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-ollama: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-ollama: decode args: %w", err)
		}
	}
	if err := runOllamaCLI(in.Args); err != nil {
		return nil, err
	}
	return &pb.InvokeReply{}, nil
}
