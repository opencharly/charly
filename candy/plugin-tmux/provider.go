package main

import (
	"context"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

// provider.go is the (inert) gRPC half of this command-only plugin. command:tmux is
// dispatched by charly syscall.Exec'ing this binary in CLI mode (sdk.Main → cliMain,
// command.go), never through the gRPC provider registry — so Invoke is unreachable and
// Describe advertises NO capability. Both exist only to satisfy the dual-mode sdk.Main
// signature + the host's non-empty-schema load gate (mirrors candy/plugin-udev).

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is unreachable for this command-only plugin: charly dispatches command:tmux by
// fork/exec (CLI mode), never gRPC. It returns a clear error so a stray gRPC Invoke is loud,
// never a silent surprise.
func (provider) Invoke(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("plugin-tmux: command:tmux is dispatched via the CLI (charly fork/execs this binary), not gRPC Invoke")
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises NO gRPC capability — command:tmux is CLI-dispatched, not resolved
// through the gRPC provider registry. It ships only the self-contained doc schema to satisfy
// the host's non-empty-schema load gate and the params codegen loop. The SDK compiles the
// embedded schema STANDALONE here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.179.0000",
		[]sdk.ProvidedCapability{},
		schemaFS, "schema")
}
