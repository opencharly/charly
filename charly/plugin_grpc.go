package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
)

// maxReverseChannelMsgBytes is the max gRPC message size (recv AND send) for the E3b
// reverse-channel server (ExecutorService + CheckContextService). go-plugin's default server
// caps messages at gRPC's 4 MiB, but a reverse ExecutorService call carries whole-FILE payloads
// — most notably vmPostApply delivering the ~27 MiB host `charly` binary into a guest via
// exec.PutFile — which overflows the 4 MiB default ("received message larger than max"). 512
// MiB comfortably covers the binary and any check-verb GetFile artifact with headroom.
const maxReverseChannelMsgBytes = 512 << 20 // 512 MiB

// This file is charly's side of the plugin wire: the server wrappers that expose
// charly's in-proc providerRegistry over gRPC (`__plugin serve`), and the client
// wrappers that turn a connected plugin's advertised capabilities into Providers.
// The handshake, dispense key, and go-plugin glue are shared with out-of-tree
// plugins via the importable plugin/sdk package (R3) — charly serves charly's
// Provider abstraction here; an external plugin serves the proto services directly
// via sdk.Serve.

// ProvidedCap is one served capability plus the CUE def that validates its
// plugin_input — the structured form of the proto ProvidedCapability, carried on
// the servedSet and lifted onto a connected unit's schema.
type ProvidedCap struct {
	Class    ProviderClass
	Word     string
	InputDef string
}

// servedSet is the set of plugin UNITS a `__plugin serve` process exposes over
// gRPC: their providers, the union of their structured capabilities, and the
// concatenation of their self-contained CUE schemas (shipped over Describe so the
// host validates plugin_input against base ++ served — identical to an external).
type servedSet struct {
	calver    string
	byKey     map[string]Provider // class:word → provider
	provided  []ProvidedCap       // sorted structured capability list
	schemaCUE string              // \n-joined concatenation of every unit's schema source
}

func newServedSet(calver string, units []PluginUnit) *servedSet {
	s := &servedSet{calver: calver, byKey: map[string]Provider{}}
	var schemas []string
	for _, u := range units {
		if u.Schema.CueSource != "" {
			schemas = append(schemas, u.Schema.CueSource)
		}
		for _, p := range u.Providers {
			k := provKey(p.Class(), p.Reserved())
			s.byKey[k] = p
			s.provided = append(s.provided, ProvidedCap{Class: p.Class(), Word: p.Reserved(), InputDef: u.Schema.InputDefs[k]})
		}
	}
	sort.Slice(s.provided, func(i, j int) bool {
		return provKey(s.provided[i].Class, s.provided[i].Word) < provKey(s.provided[j].Class, s.provided[j].Word)
	})
	s.schemaCUE = strings.Join(schemas, "\n")
	return s
}

// --- server side: charly's Provider registry over the proto services ---

type providerGRPCServer struct {
	pb.UnimplementedProviderServer
	set *servedSet
}

func (s *providerGRPCServer) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	p, ok := s.set.byKey[req.GetClass()+":"+req.GetReserved()]
	if !ok {
		return nil, fmt.Errorf("plugin serve: no provider %s:%s", req.GetClass(), req.GetReserved())
	}
	out, err := p.Invoke(ctx, &Operation{
		Reserved: req.GetReserved(), Op: req.GetOp(),
		Params: req.GetParamsJson(), Env: req.GetEnvJson(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out.JSON}, nil
}

func (s *providerGRPCServer) InvokeStream(req *pb.InvokeRequest, srv pb.Provider_InvokeStreamServer) error {
	// Single-frame: a genuinely-streaming provider (record/logcat) is a follow-up
	// (StreamingProvider) — non-streaming providers send exactly one frame.
	rep, err := s.Invoke(srv.Context(), req)
	if err != nil {
		return err
	}
	return srv.Send(&pb.Frame{ResultJson: rep.GetResultJson()})
}

type metaGRPCServer struct {
	pb.UnimplementedPluginMetaServer
	set *servedSet
}

func (m *metaGRPCServer) Describe(_ context.Context, _ *pb.Empty) (*pb.Capabilities, error) {
	provided := make([]*pb.ProvidedCapability, 0, len(m.set.provided))
	for _, c := range m.set.provided {
		provided = append(provided, &pb.ProvidedCapability{Class: string(c.Class), Word: c.Word, InputDef: c.InputDef})
	}
	return &pb.Capabilities{
		Calver:          m.set.calver,
		ProtocolVersion: sdk.ProtocolVersion,
		Provided:        provided,
		SchemaCue:       m.set.schemaCUE,
	}, nil
}

// --- client side: a connected plugin → charly Providers ---

// describe reads a connected plugin's capability manifest.
func describe(ctx context.Context, conn *sdk.Conn) (*pb.Capabilities, error) {
	return conn.Meta.Describe(ctx, &pb.Empty{})
}

// grpcProvider is a Provider backed by a remote plugin over gRPC — the
// out-of-process peer of a built-in. Call sites never distinguish the two. It embeds capMeta (the
// shared class/word + carrier methods, R3) and adds ONLY the out-of-process extras: the gRPC
// connection/broker, the executorInvoker reverse channel (InvokeWithExecutor), and the deploy
// lifecycle/preresolve flags (grpc-only — a substrate lifecycle needs the reverse channel, so the
// executorInvoker discriminator, satisfied SOLELY by *grpcProvider, is what routes it).
type grpcProvider struct {
	capMeta
	conn       *sdk.Conn
	lifecycle  bool // set ONLY for a class:deploy capability bringing its OWN host-side venue lifecycle (F6)
	preresolve bool // set ONLY for a class:deploy capability declaring a host-side preresolve step (F6)
}

func (g *grpcProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := g.conn.Provider.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(g.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// InvokeWithExecutor invokes a deploy/step/builder op WITH the E3b reverse channel: it
// stands up the host's ExecutorService (delegating to exec) on this connection's
// go-plugin broker, passes the broker id in the request, and the out-of-process plugin
// dials back to run shell/SSH ops on exec's real venue. The reverse server lives for
// the duration of the (synchronous) Invoke. `build` is the host-engine context the
// RunHostStep leg needs (the project Config + dir + DistroCfg for a Builder/SystemPackages
// host step); the zero value is fine for a deploy/step with no host-engine step to drive.
// `rebootable` marks the venue as a charly-owned guest a RebootStep may reboot mid-walk (a
// VM); false (the default) makes a RebootStep skip-and-note (a host venue is never rebooted).
// Falls back to a plain Invoke (broker id 0) when the connection has no broker (an in-proc
// transport) or no executor is given.
func (g *grpcProvider) InvokeWithExecutor(ctx context.Context, op *Operation, exec deploykit.DeployExecutor, build buildEngineContext, rebootable bool, cc *checkContextReverseServer) (*Result, error) {
	var brokerID uint32
	if g.conn.Broker != nil && (exec != nil || cc != nil) {
		id := g.conn.Broker.NextId()
		// srv is WRITTEN by the AcceptAndServe callback (which runs on the broker-accept
		// goroutine below) and READ by the deferred Stop() on THIS goroutine after Invoke
		// returns — a cross-goroutine handoff that must be synchronized (an unguarded
		// `var srv *grpc.Server` was a data race the -race detector flags). An atomic pointer
		// gives the happens-before edge without a serialize-to-hide: the callback Stores the
		// server it built, the deferred Load reads it (or nil if the plugin never dialed back
		// and the callback never ran — nothing to stop).
		var srv atomic.Pointer[grpc.Server]
		go g.conn.Broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
			// Raise the message-size cap above gRPC's 4 MiB default so a whole-file reverse
			// call (vmPostApply's ~27 MiB charly PutFile) is not rejected (maxReverseChannelMsgBytes).
			s := grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(maxReverseChannelMsgBytes), grpc.MaxSendMsgSize(maxReverseChannelMsgBytes))...)
			// Both reverse services share ONE broker id, registered on ONE server: a
			// deploy/step op supplies exec (ExecutorService); a host-coupled check verb
			// supplies BOTH exec and cc (ExecutorService for the venue + CheckContextService
			// for HTTPDo/AddBackground — F2).
			if exec != nil {
				// live overlay-build inputs (M4): a lifecycle Invoke attaches them to the ctx
				// (withOverlayBuildInputs) so the reverse server can re-thread them onto a
				// HostBuild("overlay") builder ctx; nil for every other Invoke.
				pb.RegisterExecutorServiceServer(s, &executorReverseServer{exec: exec, build: build, rebootable: rebootable, live: overlayBuildInputsFrom(ctx)})
			}
			if cc != nil {
				pb.RegisterCheckContextServiceServer(s, cc)
			}
			srv.Store(s)
			return s
		})
		defer func() {
			if s := srv.Load(); s != nil {
				s.Stop()
			}
		}()
		brokerID = id
	}
	rep, err := g.conn.Provider.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env,
		Class: string(g.class), ExecutorBrokerId: brokerID,
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// buildUnit lifts a connected plugin's Describe reply into a *PluginUnit: the
// gRPC-backed Providers AND the served CUE schema (source + per-capability input
// defs). This is THE client-side construction — identical for an external plugin
// and a builtin served out-of-process; the host never reads a candy schema/ dir.
func buildUnit(conn *sdk.Conn, caps *pb.Capabilities) (*PluginUnit, error) {
	// Version gate — a readable refusal here, never a later wire panic.
	// ProtocolVersion is the ENFORCED wire-compatibility gate: a plugin built
	// against a different charly proto/SDK speaks a different contract and is
	// refused before any Invoke. CalVer is the plugin's advisory version stamp,
	// surfaced in the refusal for the operator but NOT an equality gate — plugins
	// are independent repos at independent CalVers, and a same-host builtin served
	// out-of-process may advertise an empty/unstamped CalVer (identical binary).
	if caps.GetProtocolVersion() != sdk.ProtocolVersion {
		return nil, fmt.Errorf("plugin protocol version mismatch: plugin advertises protocol %d (CalVer %q), host requires protocol %d — rebuild the plugin against this charly",
			caps.GetProtocolVersion(), caps.GetCalver(), sdk.ProtocolVersion)
	}
	// The capability-lift loop is shared with buildUnitInProc via liftCapabilities (R3); the grpc
	// factory adds the out-of-process extras — the connection plus the class:deploy lifecycle /
	// preresolve flags (F6), which ONLY *grpcProvider carries (they need the reverse channel, and
	// the executorInvoker discriminator is satisfied SOLELY by *grpcProvider).
	providers, inputDefs, err := liftCapabilities(caps.GetProvided(), "plugin", func(meta capMeta, c *pb.ProvidedCapability) Provider {
		gp := &grpcProvider{capMeta: meta, conn: conn}
		if meta.class == ClassDeployTarget && c.GetLifecycle() {
			gp.lifecycle = true
		}
		if meta.class == ClassDeployTarget && c.GetPreresolve() {
			gp.preresolve = true
		}
		return gp
	})
	if err != nil {
		return nil, err
	}
	return &PluginUnit{
		Providers: providers,
		Schema:    PluginSchema{CueSource: caps.GetSchemaCue(), InputDefs: inputDefs},
	}, nil
}
