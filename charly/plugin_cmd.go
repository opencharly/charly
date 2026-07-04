package main

import (
	"fmt"

	"github.com/opencharly/sdk"
)

// PluginInternalCmd is the hidden `__plugin` command group — the plugin
// server/relay plumbing the registry spawns. Operators never type it; the normal
// verbs (charly check, charly bundle add, …) drive it invisibly.
type PluginInternalCmd struct {
	Serve PluginServeCmd `cmd:"" help:"serve the in-process providers over go-plugin gRPC (internal)"`
	List  PluginListCmd  `cmd:"" help:"list the registered providers and their capabilities"`
}

// PluginServeCmd exposes this charly instance's in-proc providerRegistry over
// go-plugin gRPC. Used out-of-process for crash isolation and as the in-venue
// server the ExecutorTransport reattaches to. sdk.Serve writes the handshake to
// stdout and blocks serving; the host reaps it via client.Kill (the gRPC Shutdown
// RPC), and sdk.Serve's parent-death watch self-terminates this process if the
// host dies without reaping (the orphan backstop).
type PluginServeCmd struct{}

func (c *PluginServeCmd) Run() error { //nolint:unparam // Kong Run signature requires error; sdk.Serve blocks until disconnect
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("__plugin serve: builtin schema gate: %w", err)
	}
	set := newServedSet(CharlyVersion(), providerRegistry.allServedUnits())
	sdk.Serve(&providerGRPCServer{set: set}, &metaGRPCServer{set: set})
	return nil
}

// PluginListCmd prints the registered providers ("<class>:<word>"), one per line.
type PluginListCmd struct{}

func (c *PluginListCmd) Run() error { //nolint:unparam // Kong Run signature requires the error return
	for _, p := range providerRegistry.allProviders() {
		fmt.Printf("%s:%s\n", p.Class(), p.Reserved())
	}
	return nil
}
