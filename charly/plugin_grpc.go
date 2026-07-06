package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
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
// out-of-process peer of a built-in. Call sites never distinguish the two.
type grpcProvider struct {
	conn       *sdk.Conn
	class      ProviderClass
	word       string
	contract   *stepContract // set ONLY for a class:step capability declaring a StepContract (F3); nil otherwise
	structural bool          // set ONLY for a class:kind capability that decodes a STRUCTURAL entity (F5)
	lifecycle  bool          // set ONLY for a class:deploy capability bringing its OWN host-side venue lifecycle (F6)
	preresolve bool          // set ONLY for a class:deploy capability declaring a host-side preresolve step (F6)
	validates  bool          // set ONLY for a class:kind capability serving a deep OpValidate check (F7/C8)
	phase      string        // the plugin lifecycle phase (F9; sdk.Phase*, normalized — "" → runtime)
	primary    string        // set ONLY for a class:verb capability declaring a scalar-sugar primary input field
}

func (g *grpcProvider) Reserved() string     { return g.word }
func (g *grpcProvider) Class() ProviderClass { return g.class }

// declaredStepContract implements stepContractCarrier — a class:step provider's plugin-declared
// Scope/Venue/Gate (F3), nil/false for every other capability.
func (g *grpcProvider) declaredStepContract() (stepContract, bool) {
	if g.contract == nil {
		return stepContract{}, false
	}
	return *g.contract, true
}

// isStructuralKind implements structuralKindCarrier — a class:kind provider whose decode
// returns a spec.Deploy member tree (-> uf.Bundle) rather than a flat body (F5).
func (g *grpcProvider) isStructuralKind() bool { return g.structural }

// isValidatingKind implements validatingKindCarrier — a class:kind provider serving a deep
// OpValidate check the host dispatches at load (F7/C8).
func (g *grpcProvider) isValidatingKind() bool { return g.validates }

// pluginPhase implements phaseCarrier — the plugin lifecycle phase the kernel loads/invokes this
// provider in (F9; normalized, never empty).
func (g *grpcProvider) pluginPhase() string { return g.phase }
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
func (g *grpcProvider) InvokeWithExecutor(ctx context.Context, op *Operation, exec DeployExecutor, build buildEngineContext, rebootable bool, cc *checkContextReverseServer) (*Result, error) {
	var brokerID uint32
	if g.conn.Broker != nil && (exec != nil || cc != nil) {
		id := g.conn.Broker.NextId()
		var srv *grpc.Server
		go g.conn.Broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
			// Raise the message-size cap above gRPC's 4 MiB default so a whole-file reverse
			// call (vmPostApply's ~27 MiB charly PutFile) is not rejected (maxReverseChannelMsgBytes).
			srv = grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(maxReverseChannelMsgBytes), grpc.MaxSendMsgSize(maxReverseChannelMsgBytes))...)
			// Both reverse services share ONE broker id, registered on ONE server: a
			// deploy/step op supplies exec (ExecutorService); a host-coupled check verb
			// supplies BOTH exec and cc (ExecutorService for the venue + CheckContextService
			// for HTTPDo/AddBackground — F2).
			if exec != nil {
				// live overlay-build inputs (M4): a lifecycle Invoke attaches them to the ctx
				// (withOverlayBuildInputs) so the reverse server can re-thread them onto a
				// HostBuild("overlay") builder ctx; nil for every other Invoke.
				pb.RegisterExecutorServiceServer(srv, &executorReverseServer{exec: exec, build: build, rebootable: rebootable, live: overlayBuildInputsFrom(ctx)})
			}
			if cc != nil {
				pb.RegisterCheckContextServiceServer(srv, cc)
			}
			return srv
		})
		defer func() {
			if srv != nil {
				srv.Stop()
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
	provided := caps.GetProvided()
	providers := make([]Provider, 0, len(provided))
	inputDefs := make(map[string]string, len(provided))
	for _, c := range provided {
		class := ProviderClass(c.GetClass())
		if !providerClasses[class] || c.GetWord() == "" {
			return nil, fmt.Errorf("plugin advertised malformed capability %q:%q", c.GetClass(), c.GetWord())
		}
		gp := &grpcProvider{conn: conn, class: class, word: c.GetWord()}
		// A class:step capability may DECLARE its install-step contract (F3): the host carries
		// the plugin-declared Scope/Venue/Gate so compileActOp builds an externalStep with it.
		if sc := c.GetStepContract(); class == ClassStep && sc != nil {
			gp.contract = &stepContract{Scope: scopeFromName(sc.GetScope()), Venue: Venue(sc.GetVenue()), Gate: Gate(sc.GetGate()), Emits: sc.GetEmits()}
		}
		// A class:kind capability may declare it decodes a STRUCTURAL entity (F5): runPluginKind
		// folds its spec.Deploy reply into uf.Bundle instead of landing a flat body opaquely.
		if class == ClassKind && c.GetStructural() {
			gp.structural = true
		}
		// A class:kind capability may declare a deep OpValidate check (F7/C8).
		if class == ClassKind && c.GetValidates() {
			gp.validates = true
		}
		// Every capability declares a lifecycle PHASE (F9; normalized, default runtime).
		gp.phase = sdk.NormalizePhase(c.GetPhase())
		if class == ClassVerb {
			gp.primary = c.GetPrimary()
		}
		// A class:deploy capability may declare it brings its OWN venue lifecycle (F6): the host
		// registers a wire-backed substrateLifecycle for it at plugin-load.
		if class == ClassDeployTarget && c.GetLifecycle() {
			gp.lifecycle = true
		}
		// A class:deploy capability may declare a host-side preresolve step (F6).
		if class == ClassDeployTarget && c.GetPreresolve() {
			gp.preresolve = true
		}
		providers = append(providers, gp)
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return &PluginUnit{
		Providers: providers,
		Schema:    PluginSchema{CueSource: caps.GetSchemaCue(), InputDefs: inputDefs},
	}, nil
}

// primaryInput implements primaryCarrier (the scalar-sugar primary, F/verb).
func (g *grpcProvider) primaryInput() string { return g.primary }
