package doctor

import (
	"context"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
)

// provider.go is the (inert) gRPC half of this command-only plugin. command:doctor is
// dispatched by charly syscall.Exec'ing this binary in CLI mode (sdk.Main → cliMain,
// command.go), never through the gRPC provider registry — so Invoke is unreachable and
// Describe advertises NO capability. Both exist only to satisfy the dual-mode sdk.Main
// signature + the host's non-empty-schema load gate (mirrors candy/plugin-udev / plugin-tmux /
// plugin-preempt / plugin-feature).

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is unreachable for this command-only plugin: charly dispatches command:doctor by
// fork/exec (CLI mode), never gRPC. It returns a clear error so a stray gRPC Invoke is loud,
// never a silent surprise.
func (provider) Invoke(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("plugin-doctor: command:doctor is dispatched via the CLI (charly fork/execs this binary), not gRPC Invoke")
}
