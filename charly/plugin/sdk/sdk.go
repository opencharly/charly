// Package sdk is the importable surface an out-of-tree charly plugin builds
// against. An external plugin implements the proto Provider + PluginMeta services
// (github.com/opencharly/charly/charly/plugin/proto) and calls sdk.Serve from
// its main; charly connects to it through the SAME handshake + dispense key. The
// handshake/glue live here (NOT in charly's package main) so both charly and an
// external plugin share ONE definition — no drift, no duplication (R3).
package sdk

import (
	"context"
	"os"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/opencharly/charly/charly/plugin/proto"
)

// ProtocolVersion is the go-plugin/proto contract version — a thin secondary gate.
// CalVer (charly's version.go) is the authority; matching CalVer ⇒ matching proto.
const ProtocolVersion = 1

// DispenseKey is the single go-plugin plugin name; charly serves/dispenses ONE
// gRPC plugin exposing the uniform Provider + PluginMeta services.
const DispenseKey = "charly"

// Handshake is the magic-cookie handshake charly and every plugin MUST share. A
// plugin server refuses to serve unless launched with CHARLY_PLUGIN set, so a
// plugin binary run by hand prints the "not meant to be executed directly" notice
// instead of hanging.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "CHARLY_PLUGIN",
	MagicCookieValue: "charly-plugin-v1",
}

// IsServeMode reports whether this process was launched by charly as a go-plugin gRPC
// SERVER (the handshake magic-cookie env is present) rather than invoked directly as a
// CLI. charly sets the cookie ONLY when it execs a plugin to connect over gRPC
// (LocalTransport, for a verb/kind/deploy/step/builder capability); a COMMAND plugin
// fork/exec'd as a CLI passthrough (charly's syscall.Exec command dispatch strips the
// cookie) sees it absent and runs in CLI mode. The single switch a dual-mode plugin's
// main() pivots on.
func IsServeMode() bool {
	return os.Getenv(Handshake.MagicCookieKey) == Handshake.MagicCookieValue
}

// Main is the dual-mode entry point a plugin's main() delegates to. In SERVE mode
// (charly launched it over go-plugin gRPC) it serves the plugin's Provider + PluginMeta
// (its verb/kind/deploy/step/builder capabilities). Otherwise the plugin was fork/exec'd
// by charly's COMMAND dispatch (or run by hand) and owns real terminal stdio/TTY: cli
// runs the command's work with os.Args[1:], its int return becoming the process exit code.
//
//	func main() { sdk.Main(&provider{}, &meta{}, cliMain) }
func Main(providerSrv pb.ProviderServer, metaSrv pb.PluginMetaServer, cli func(args []string) int) {
	if IsServeMode() {
		Serve(providerSrv, metaSrv)
		return
	}
	os.Exit(cli(os.Args[1:]))
}

// Serve exposes a plugin's Provider + PluginMeta services over go-plugin gRPC and
// blocks serving. The host reaps it on exit by killing the client connection
// (providerRegistry.Close → client.Kill, sending the gRPC Shutdown that stops
// this server); go-plugin's server has no parent-death detection of its own, so
// watchParentDeath is the backstop that self-terminates this process if the host
// dies without reaping (crash / SIGKILL / os.Exit) — preventing orphaned
// `__plugin serve` processes. The serve half of Main (a verb/kind/deploy/step/
// builder plugin with no CLI mode may call it directly):
//
//	func main() { sdk.Serve(&myProvider{}, &myMeta{}) }
func Serve(providerSrv pb.ProviderServer, metaSrv pb.PluginMetaServer) {
	watchParentDeath()
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(providerSrv, metaSrv),
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}

// PluginMap builds the go-plugin PluginSet for the dispense key. Server side passes
// the two service impls; the client side (charly connecting) passes nil,nil and
// receives a *Conn from the dispense.
func PluginMap(providerSrv pb.ProviderServer, metaSrv pb.PluginMetaServer) plugin.PluginSet {
	return plugin.PluginSet{DispenseKey: &grpcPlugin{providerSrv: providerSrv, metaSrv: metaSrv}}
}

type grpcPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	providerSrv pb.ProviderServer
	metaSrv     pb.PluginMetaServer
}

func (p *grpcPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error { //nolint:unparam // go-plugin GRPCPlugin mandates the error return
	servedBroker = broker // E3b: a deploy/step/builder Invoke dials the host's ExecutorService through this
	pb.RegisterProviderServer(s, p.providerSrv)
	pb.RegisterPluginMetaServer(s, p.metaSrv)
	return nil
}

func (p *grpcPlugin) GRPCClient(_ context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) { //nolint:unparam // go-plugin GRPCPlugin mandates the (any,error) return
	return &Conn{Provider: pb.NewProviderClient(c), Meta: pb.NewPluginMetaClient(c), Broker: broker}, nil
}

// Conn is the dispensed client handle — charly's side of a connected plugin.
type Conn struct {
	Provider pb.ProviderClient
	Meta     pb.PluginMetaClient
	// Broker is this connection's go-plugin GRPCBroker — the host's handle to stand up
	// the E3b reverse-channel ExecutorService a deploy/step/builder plugin dials back
	// to. Nil for an in-proc transport (no reverse channel needed).
	Broker *plugin.GRPCBroker
}
