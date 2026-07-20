// Package tmux implements generic typed terminal sessions over tmux control
// mode. The same provider is compiled in or served out of process and is
// reachable through every Charly provider transport, including gRPC tunneled
// through SSH and tmux sessions placed behind gRPC target hops.
package tmux

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the tmux provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the two typed streaming capabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.199.1330",
		[]sdk.ProvidedCapability{
			{Class: "terminal", Word: "tmux"},
			{Class: "agent-runtime", Word: "tmux"},
		},
		schemaFS)
}
