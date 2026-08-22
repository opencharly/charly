// Package transport (github.com/opencharly/spec/transport, #55 step1) provides
// transport-neutral gRPC connections to Charly targets. This file holds the
// go-plugin serve/dispense surface relocated from the github.com/opencharly/sdk
// root package: the handshake, the Serve/PluginMap/Conn trio, and the channel
// streaming types an out-of-process plugin + the charly host share over the
// broker. It is a fabric slice of the spec contract module — a host primitive a
// plugin cannot hold as a shared library (the gRPC dial/serve over the go-plugin
// stdio carrier) — so by #55 Rule 1 it lives in a contained spec slice whose
// only heavy dep is google.golang.org/grpc + hashicorp/go-plugin + spec/proto
// (Rule 2). charly core imports this slice INSTEAD of the sdk root; the sdk
// root keeps a thin re-export during cutover then is deleted.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	pb "github.com/opencharly/spec/proto"
)

// ProtocolVersion is the go-plugin/proto contract version — a thin secondary gate.
// CalVer (charly's version.go) is the authority; matching CalVer ⇒ matching proto.
const ProtocolVersion = 2

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

// ServedBroker is the go-plugin GRPCBroker captured when this plugin's gRPC
// server starts (grpcPlugin.GRPCServer). A deploy/step/builder plugin dials the
// host's E3b reverse-channel ExecutorService through it. One broker per plugin
// process (go-plugin's model), so a package var is the natural home. Exported so
// the sdk package's executor/checkverb helpers can read it after relocation.
var ServedBroker *plugin.GRPCBroker

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
//	func main() { transport.Main(&provider{}, &meta{}, cliMain) }
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
//	func main() { transport.Serve(&myProvider{}, &myMeta{}) }
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
	ServedBroker = broker // E3b: a deploy/step/builder Invoke dials the host's ExecutorService through this
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

// parentWatchInterval is how often the orphan backstop polls the parent PID.
// Named (not a magic literal) per CLAUDE.md R4; it bounds the worst-case delay
// between the host dying and an orphaned plugin self-terminating.
const parentWatchInterval = 2 * time.Second

// watchParentDeath starts the orphan backstop: a goroutine that self-exits this
// plugin process when its parent — the charly host that spawned it — dies.
// See the long comment in sdk/parentwatch.go (now relocated here) for the leak
// it prevents and why it does not break the unbounded credential await-unlock RPC.
func watchParentDeath() {
	ppid := os.Getppid()
	if ppid <= 1 {
		return
	}
	go runParentWatch(ppid, parentWatchInterval, os.Getppid, func() { os.Exit(0) })
}

// runParentWatch polls getppid every interval and calls onOrphaned exactly once,
// the moment the parent PID differs from startPPID (the parent died → this
// process was reparented). Extracted from watchParentDeath so tests drive it
// with injected fakes — no real fork required.
func runParentWatch(startPPID int, interval time.Duration, getppid func() int, onOrphaned func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		if getppid() != startPPID {
			onOrphaned()
			return
		}
	}
}

// Channel frame kinds are transport vocabulary, not runtime semantics. Runtime-
// specific events travel as CUE-generated JSON in ChannelFrame.PayloadJson.
const (
	ChannelOpen     = "open"
	ChannelStdin    = "stdin"
	ChannelStdout   = "stdout"
	ChannelStderr   = "stderr"
	ChannelTerminal = "terminal"
	ChannelStatus   = "status"
	ChannelResize   = "resize"
	ChannelSignal   = "signal"
	ChannelAck      = "ack"
	ChannelCancel   = "cancel"
	ChannelExit     = "exit"
	ChannelError    = "error"
	ChannelResync   = "resync"
)

// ProviderChannel is the common subset of the generated client and server
// streams. It lets in-process and gRPC providers share one channel handler.
type ProviderChannel interface {
	Context() context.Context
	Send(*pb.ChannelFrame) error
	Recv() (*pb.ChannelFrame, error)
}

// ChannelProvider is the optional streaming extension to Provider. The first
// frame has already been validated as an open frame and remains available as
// open; subsequent controller frames arrive through stream. Domain payloads are
// generated from CUE and carried in open.PayloadJson.
type ChannelProvider interface {
	OpenChannel(open *pb.ChannelFrame, stream ProviderChannel) error
}

// ReceiveChannelOpen reads and validates the mandatory first frame. The
// request id, provider class/word, and operation are required so every later
// frame can be correlated without inspecting runtime-specific payloads.
func ReceiveChannelOpen(stream ProviderChannel) (*pb.ChannelFrame, error) {
	open, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if open.GetKind() != ChannelOpen {
		return nil, fmt.Errorf("transport channel: first frame kind %q, want %q", open.GetKind(), ChannelOpen)
	}
	if open.GetRequestId() == "" || open.GetClass() == "" || open.GetReserved() == "" || open.GetOp() == "" {
		return nil, errors.New("transport channel: open requires request_id, class, reserved, and op")
	}
	return open, nil
}

// OpenProviderChannel starts a generated Provider.Channel stream and sends its
// mandatory open frame. The returned stream is ready for concurrent Send/Recv,
// as supported by gRPC.
func OpenProviderChannel(ctx context.Context, client pb.ProviderClient, open *pb.ChannelFrame) (pb.Provider_ChannelClient, error) {
	if open == nil {
		return nil, errors.New("transport channel: nil open frame")
	}
	if open.Kind == "" {
		open.Kind = ChannelOpen
	}
	if open.Kind != ChannelOpen {
		return nil, fmt.Errorf("transport channel: open frame kind %q", open.Kind)
	}
	stream, err := client.Channel(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(open); err != nil {
		return nil, err
	}
	return stream, nil
}

// SequenceGate rejects duplicates, regressions, and gaps. A provider can turn
// a gap into ChannelResync using ReplayBuffer.ReplayFrom; it must never silently
// reorder process or terminal output.
type SequenceGate struct {
	mu   sync.Mutex
	next uint64
}

func NewSequenceGate(first uint64) *SequenceGate { return &SequenceGate{next: first} }

func (g *SequenceGate) Accept(frame *pb.ChannelFrame) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if frame.GetSequence() != g.next {
		if frame.GetKind() == ChannelResync && frame.GetSequence() > g.next {
			g.next = frame.GetSequence() + 1
			return nil
		}
		return fmt.Errorf("transport channel: sequence %d, want %d", frame.GetSequence(), g.next)
	}
	g.next++
	return nil
}

// Expected returns the next sequence without advancing the gate.
func (g *SequenceGate) Expected() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.next
}

// ReplayBuffer is a bounded, acknowledgement-aware frame history for detach /
// reconnect. Bounds are enforced by both frame count and protobuf byte size.
// When an unacknowledged frame would be evicted, Add fails loudly; callers must
// preserve evidence and enter the incident/RCA workflow rather than hide loss.
type ReplayBuffer struct {
	mu       sync.Mutex
	frames   []*pb.ChannelFrame
	bytes    int
	maxFrame int
	maxBytes int
	acked    uint64
}

func NewReplayBuffer(maxFrames, maxBytes int) *ReplayBuffer {
	return &ReplayBuffer{maxFrame: maxFrames, maxBytes: maxBytes}
}

func (b *ReplayBuffer) Add(frame *pb.ChannelFrame) error {
	if frame == nil || frame.GetSequence() == 0 {
		return errors.New("transport channel: replay frame requires a non-zero sequence")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := len(b.frames); n > 0 && frame.GetSequence() <= b.frames[n-1].GetSequence() {
		return fmt.Errorf("transport channel: replay sequence %d is not monotonic", frame.GetSequence())
	}
	cloned := proto.Clone(frame).(*pb.ChannelFrame)
	size := proto.Size(cloned)
	if (b.maxFrame > 0 && len(b.frames)+1 > b.maxFrame) || (b.maxBytes > 0 && b.bytes+size > b.maxBytes) {
		if len(b.frames) == 0 || b.frames[0].GetSequence() > b.acked {
			unacknowledged := frame.GetSequence()
			if len(b.frames) > 0 {
				unacknowledged = b.frames[0].GetSequence()
			}
			return fmt.Errorf("transport channel: replay capacity exceeded with unacknowledged sequence %d", unacknowledged)
		}
		b.dropAcknowledgedLocked()
	}
	if (b.maxFrame > 0 && len(b.frames)+1 > b.maxFrame) || (b.maxBytes > 0 && b.bytes+size > b.maxBytes) {
		return fmt.Errorf("transport channel: frame %d exceeds replay capacity", frame.GetSequence())
	}
	b.frames = append(b.frames, cloned)
	b.bytes += size
	return nil
}

func (b *ReplayBuffer) Acknowledge(sequence uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sequence > b.acked {
		b.acked = sequence
	}
	b.dropAcknowledgedLocked()
}

func (b *ReplayBuffer) dropAcknowledgedLocked() {
	cut := 0
	for cut < len(b.frames) && b.frames[cut].GetSequence() <= b.acked {
		b.bytes -= proto.Size(b.frames[cut])
		cut++
	}
	b.frames = b.frames[cut:]
}

func (b *ReplayBuffer) ReplayFrom(sequence uint64) ([]*pb.ChannelFrame, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.frames) > 0 && sequence < b.frames[0].GetSequence() {
		return nil, fmt.Errorf("transport channel: sequence %d is no longer available; oldest is %d", sequence, b.frames[0].GetSequence())
	}
	out := make([]*pb.ChannelFrame, 0, len(b.frames))
	for _, frame := range b.frames {
		if frame.GetSequence() >= sequence {
			out = append(out, proto.Clone(frame).(*pb.ChannelFrame))
		}
	}
	return out, nil
}

// CopyChannel relays frames until EOF or cancellation. It is intentionally a
// byte-preserving transport primitive; it does not inspect agent or terminal
// payloads.
func CopyChannel(dst interface{ Send(*pb.ChannelFrame) error }, src interface {
	Recv() (*pb.ChannelFrame, error)
}) error {
	for {
		frame, err := src.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := dst.Send(frame); err != nil {
			return err
		}
	}
}

// RelayChannel connects a controller-side ProviderChannel to a downstream gRPC
// channel with ordered half-close semantics. Provider output is the evidence
// writer: once that direction ends, no later frame can mutate controller state
// and the relay may return. If controller input ends first, CloseSend delivers
// the protocol EOF and the relay drains provider output before returning. This
// prevents a completed command from racing its successor's durable cursor.
//
// Cancellation ownership: when the provider-output direction finishes first,
// RelayChannel returns while the input-copy goroutine may still be blocked in
// upstream.Recv(). The CALLER owns upstream's lifecycle — after return, cancel
// the context upstream carries (or close upstream) to release that goroutine.
func RelayChannel(upstream ProviderChannel, downstream interface {
	ProviderChannel
	CloseSend() error
}) error {
	type result struct {
		providerOutput bool
		err            error
	}
	results := make(chan result, 2)
	go func() {
		err := CopyChannel(downstream, upstream)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		closeErr := downstream.CloseSend()
		if errors.Is(closeErr, io.EOF) {
			closeErr = nil
		}
		err = errors.Join(err, closeErr)
		results <- result{err: err}
	}()
	go func() {
		results <- result{providerOutput: true, err: CopyChannel(upstream, downstream)}
	}()
	first := <-results
	if first.providerOutput {
		return first.err
	}
	second := <-results
	return errors.Join(first.err, second.err)
}
