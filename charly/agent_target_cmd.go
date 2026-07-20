package main

import (
	"fmt"

	"google.golang.org/grpc"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/targetkit"
)

// AgentTargetInternalCmd is the fixed remote endpoint used by targetkit. It is
// intentionally generic despite its historical command name: it serves every
// Provider.Channel class and recursively relays CUE-authored target routes.
type AgentTargetInternalCmd struct {
	Serve AgentTargetServeCmd `cmd:"" help:"serve provider gRPC on stdin/stdout"`
}

type AgentTargetServeCmd struct {
	Stdio bool `name:"stdio" help:"serve one gRPC connection on stdin/stdout" required:""`
}

func (c *AgentTargetServeCmd) Run() error {
	if !c.Stdio {
		return fmt.Errorf("__agent-target serve: --stdio is required")
	}
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("__agent-target serve: builtin schema gate: %w", err)
	}
	set := newServedSet(CharlyVersion(), providerRegistry.allServedUnits())
	in, out := targetkit.StdioFiles()
	return targetkit.ServeStdio(in, out, func(server *grpc.Server) {
		pb.RegisterProviderServer(server, &providerGRPCServer{set: set})
		pb.RegisterPluginMetaServer(server, &metaGRPCServer{set: set})
	}, grpc.MaxRecvMsgSize(maxReverseChannelMsgBytes), grpc.MaxSendMsgSize(maxReverseChannelMsgBytes))
}
