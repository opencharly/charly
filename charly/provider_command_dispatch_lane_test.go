package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/opencharly/spec/ops"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/transport"
)

// provider_command_dispatch_lane_test.go — the gates on opencharly/charly#1006.
//
// `charly pipeline run <entity>` could not reach ANY workflow engine for a plugin-provided
// front-end. The `--dry-run` control localised it to the DISPATCH leg: resolution + lowering
// need no host and passed, while the dispatch died `no host executor`. The mechanism was that
// the placement alone chose the lane — a compiled-in command ran in-proc and an out-of-process
// one ALWAYS syscall.Exec'd, which REPLACES the charly process, so the plugin ran its CLI face
// (plugin-pipeline cli.go:288 `runCLI(args, nil)`) with no host and every
// InvokeProvider("workflow", …) from it was structurally unreachable.
//
// The rule these tests pin is the one the spec's ProvidedCapability.interactive comment already
// documents: a command that DECLARES it needs a terminal keeps the process-replacing exec lane;
// EVERY other command dispatches through its provider's Invoke(OpRun) lane with the host reverse
// channel attached. The decision is made from the DECLARED flag and the placement, and the
// load-bearing assertion is behavioural: which LANE the capability was dispatched through.

// recordingCommandProvider is a class:command capability that records whether it was INVOKED
// (the broker-backed lane) — the observable that distinguishes the two lanes, because the other
// lane syscall.Exec's a real plugin binary and never Invokes anything.
type recordingCommandProvider struct {
	word    string
	invoked atomic.Int32
	gotOp   atomic.Value
}

func (p *recordingCommandProvider) Reserved() string     { return p.word }
func (p *recordingCommandProvider) Class() ProviderClass { return ClassCommand }

func (p *recordingCommandProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	p.invoked.Add(1)
	p.gotOp.Store(op.Op)
	rep, err := ops.ResultJSON("pass", "command-ok reserved="+op.Reserved)
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// servedCommandProvider returns a *grpcProvider whose INVOKE reaches prov over a real gRPC
// bufconn connection — the same wire the out-of-process placement uses, so "was it Invoked" is
// answered across the transport boundary rather than by a same-process shortcut.
func servedCommandProvider(t *testing.T, prov Provider) *grpcProvider {
	t.Helper()
	set := newServedSet("2026.0.0", []PluginUnit{{Providers: []Provider{prov}}})
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterProviderServer(srv, &providerGRPCServer{set: set})
	pb.RegisterPluginMetaServer(srv, &metaGRPCServer{set: set})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pc := &transport.Conn{Provider: pb.NewProviderClient(conn), Meta: pb.NewPluginMetaClient(conn)}
	return &grpcProvider{capMeta: capMeta{class: ClassCommand, word: prov.Reserved()}, conn: pc}
}

// TestDispatchCommand_OutOfProcessCommandReachesTheInvokeLane is THE regression gate for
// opencharly/charly#1006: an out-of-process (grpcProvider) command must be INVOKED, with the host
// reverse channel attached, instead of being syscall.Exec'd.
//
// It fails without the fix in the most direct way available: before it, `dispatchCommand`'s
// `if _, external := prov.(*grpcProvider); !external` branch EXCLUDED the grpc placement from the
// Invoke arm, so a connected out-of-process command fell through to dispatchExternalCommand and
// `prov.invoked` stayed 0. The assertion is on that counter, not on which function was called.
func TestDispatchCommand_OutOfProcessCommandReachesTheInvokeLane(t *testing.T) {
	rec := &recordingCommandProvider{word: "zzfrontend"}
	gp := servedCommandProvider(t, rec)

	saved := providerRegistry
	r := newRegistry()
	if err := r.register(gp, "test-grpc"); err != nil {
		t.Fatalf("register: %v", err)
	}
	providerRegistry = r
	defer func() { providerRegistry = saved }()

	field := exportedCommandField(rec.word)
	d := externalCommandDispatch{word: rec.word, holder: externalCommandHolder(rec.word, field, nil), field: field}

	if err := dispatchCommand(d, ""); err != nil {
		t.Fatalf("dispatchCommand(%s): %v", rec.word, err)
	}
	if got := rec.invoked.Load(); got != 1 {
		t.Fatalf("the out-of-process command was Invoked %d time(s), want 1 — dispatchCommand sent it down the process-replacing exec lane instead of its provider's Invoke lane, which is exactly opencharly/charly#1006: the plugin then runs its CLI face with NO host, so InvokeProvider(\"workflow\", …) is unreachable and `charly pipeline run` cannot reach any engine.", got)
	}
	if op, _ := rec.gotOp.Load().(string); op != ops.OpRun {
		t.Errorf("command dispatched with op %q, want %q (the documented command pass-through envelope)", op, ops.OpRun)
	}
}

// TestDispatchCommand_InteractiveCommandKeepsTheExecLane is the other half: a command that DECLARED
// it needs a real terminal must NOT be routed through the Invoke lane. Routing `charly mcp serve
// --stdio` (or `logs -f`) through a broker-backed Invoke would hand the plugin a pipe instead of
// the operator's terminal — a regression the declaration exists to prevent. The exec lane is not
// observable in-process (it syscall.Exec's a binary), so this asserts the DECISION: the provider is
// left un-invoked.
func TestDispatchCommand_InteractiveCommandKeepsTheExecLane(t *testing.T) {
	rec := &recordingCommandProvider{word: "zzstdio"}
	gp := servedCommandProvider(t, rec)
	gp.interactive = true

	saved := providerRegistry
	r := newRegistry()
	if err := r.register(gp, "test-grpc"); err != nil {
		t.Fatalf("register: %v", err)
	}
	providerRegistry = r
	defer func() { providerRegistry = saved }()

	field := exportedCommandField(rec.word)
	d := externalCommandDispatch{word: rec.word, holder: externalCommandHolder(rec.word, field, nil), field: field}

	// The exec lane needs a real binary and will fail here; that failure is the POINT — reaching
	// dispatchExternalCommand is what proves the declared-interactive command kept its lane.
	_ = dispatchCommand(d, "")

	if got := rec.invoked.Load(); got != 0 {
		t.Fatalf("a command DECLARING `interactive` was Invoked %d time(s) — it must keep the process-replacing exec lane so its child inherits the operator's terminal (ProvidedCapability.interactive: `shell`, `logs -f`, `mcp serve --stdio`).", got)
	}
}
