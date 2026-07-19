package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// testVerbProvider is an in-proc Provider used to exercise the gRPC contract.
type testVerbProvider struct{ word string }

func (p testVerbProvider) Reserved() string     { return p.word }
func (p testVerbProvider) Class() ProviderClass { return ClassVerb }
func (p testVerbProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	j, err := json.Marshal(pluginCheckResult{Status: "pass", Message: "testprobe-ok reserved=" + op.Reserved})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

// TestPluginGRPCRoundTrip proves the uniform Provider envelope AND the served
// schema survive the gRPC boundary: a served in-proc unit (providerGRPCServer +
// metaGRPCServer) is reached by the client-side grpcProvider through Describe +
// buildUnit + Invoke, with the structured capabilities, the schema source, the
// pass verdict, and the marker all round-tripping intact. This is the wire
// contract LocalTransport / ExecutorTransport ride on (the go-plugin exec/reattach
// envelope itself is proven by the RDD spikes).
func TestPluginGRPCRoundTrip(t *testing.T) {
	unit := PluginUnit{
		Providers: []Provider{testVerbProvider{word: "testprobe"}},
		Schema: PluginSchema{
			CueSource: "#TestprobeInput: {marker?: string}\n",
			InputDefs: map[string]string{"verb:testprobe": "#TestprobeInput"},
		},
	}
	set := newServedSet("2026.0.0", []PluginUnit{unit})

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterProviderServer(srv, &providerGRPCServer{set: set})
	pb.RegisterPluginMetaServer(srv, &metaGRPCServer{set: set})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	pc := &sdk.Conn{Provider: pb.NewProviderClient(conn), Meta: pb.NewPluginMetaClient(conn)}

	caps, err := describe(context.Background(), pc)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if caps.GetCalver() != "2026.0.0" {
		t.Fatalf("describe calver = %q, want 2026.0.0", caps.GetCalver())
	}
	if caps.GetSchemaCue() == "" {
		t.Fatal("describe schema_cue is empty — the served schema must travel over Describe")
	}
	foundCap := false
	for _, c := range caps.GetProvided() {
		if c.GetClass() == "verb" && c.GetWord() == "testprobe" && c.GetInputDef() == "#TestprobeInput" {
			foundCap = true
		}
	}
	if !foundCap {
		t.Fatalf("describe provided = %v, want verb:testprobe with input_def #TestprobeInput", caps.GetProvided())
	}

	got, err := buildUnit(pc, caps)
	if err != nil {
		t.Fatalf("buildUnit: %v", err)
	}
	provs := got.Providers
	if len(provs) != 1 || provs[0].Class() != ClassVerb || provs[0].Reserved() != "testprobe" {
		t.Fatalf("buildUnit providers = %+v, want one verb:testprobe", provs)
	}
	if got.Schema.CueSource == "" || got.Schema.InputDefs["verb:testprobe"] != "#TestprobeInput" {
		t.Fatalf("buildUnit schema = %+v, want non-empty source + #TestprobeInput input def", got.Schema)
	}

	out, err := provs[0].Invoke(context.Background(), &Operation{
		Reserved: "testprobe", Op: OpRun, Params: []byte(`{"plugin":"testprobe"}`), Env: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		t.Fatalf("decode result: %v (%s)", err, out.JSON)
	}
	if pr.Status != "pass" || !strings.Contains(pr.Message, "testprobe-ok") {
		t.Fatalf("result = %+v, want pass + testprobe-ok", pr)
	}
}

// TestBuildUnitRefusesProtocolMismatch proves the version gate (PE4): a plugin
// advertising a different proto/SDK ProtocolVersion is refused with a readable
// error before any Invoke, instead of being silently accepted to fail later as a
// wire panic. CalVer is carried in the diagnostic but is not itself a gate.
func TestBuildUnitRefusesProtocolMismatch(t *testing.T) {
	caps := &pb.Capabilities{
		Calver:          "2026.172.0001",
		ProtocolVersion: sdk.ProtocolVersion + 1, // a plugin built against a different charly
		Provided:        []*pb.ProvidedCapability{{Class: "verb", Word: "externalprobe", InputDef: "#ExternalprobeInput"}},
		SchemaCue:       "#ExternalprobeInput: {marker?: string}\n",
	}
	// conn is nil: the gate must refuse before constructing any grpcProvider.
	if _, err := buildUnit(nil, caps); err == nil {
		t.Fatal("buildUnit accepted a protocol-version mismatch; want a readable refusal")
	} else if !strings.Contains(err.Error(), "protocol version mismatch") {
		t.Fatalf("error = %v, want it to name the protocol version mismatch", err)
	}
}

// TestProviderRegistryDispatch proves the registry resolves a verb provider and
// the (class, word) keying keeps a kind and a verb of the SAME word distinct.
func TestProviderRegistryDispatch(t *testing.T) {
	r := newRegistry()
	if err := r.register(testVerbProvider{word: "dup"}, "builtin"); err != nil {
		t.Fatalf("register verb: %v", err)
	}
	// Same word, different class — must NOT collide.
	if err := r.register(testKindProvider{word: "dup"}, "builtin"); err != nil {
		t.Fatalf("register kind (same word, diff class) should not collide: %v", err)
	}
	// Duplicate (class, word) — must be refused.
	if err := r.register(testVerbProvider{word: "dup"}, "builtin"); err == nil {
		t.Fatalf("duplicate verb:dup should be refused")
	}
	if p, ok := r.ResolveVerb("dup"); !ok || p.Class() != ClassVerb {
		t.Fatalf("ResolveVerb(dup) = %v,%v want verb", p, ok)
	}
	if p, ok := r.ResolveKind("dup"); !ok || p.Class() != ClassKind {
		t.Fatalf("ResolveKind(dup) = %v,%v want kind", p, ok)
	}
}

func TestSSHExecutorForTargetHopPreservesGenericTransportOptions(t *testing.T) {
	hop := spec.TargetHop{
		Transport:    "ssh",
		Address:      "box.example",
		User:         "agent",
		Port:         2207,
		IdentityFile: "/keys/id_ed25519",
		Options: spec.StrMap{
			"ProxyJump":             "bastion",
			"StrictHostKeyChecking": "accept-new",
		},
	}
	exec := sshExecutorForTargetHop(hop)
	if exec.User != hop.User || exec.Host != hop.Address || exec.Port != int(hop.Port) {
		t.Fatalf("executor coordinates = %+v, want hop %+v", exec, hop)
	}
	wantArgs := []string{"-i", "/keys/id_ed25519", "-o", "ProxyJump=bastion", "-o", "StrictHostKeyChecking=accept-new"}
	if strings.Join(exec.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("executor args = %#v, want %#v", exec.Args, wantArgs)
	}
}

func TestSplitTargetProcessPairPreservesDialAndRecursesAcrossRouteMatrix(t *testing.T) {
	tests := []struct {
		name      string
		hops      []spec.TargetHop
		wantFirst string
		wantRest  []string
		ok        bool
	}{
		{name: "exec grpc tmux", hops: []spec.TargetHop{{Transport: "exec"}, {Transport: "grpc"}, {Transport: "tmux"}}, wantFirst: "exec", wantRest: []string{"tmux"}, ok: true},
		{name: "ssh grpc tmux", hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "grpc"}, {Transport: "tmux"}}, wantFirst: "ssh", wantRest: []string{"tmux"}, ok: true},
		{name: "ssh grpc exec grpc tmux", hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "grpc"}, {Transport: "exec"}, {Transport: "grpc"}, {Transport: "tmux"}}, wantFirst: "ssh", wantRest: []string{"exec", "grpc", "tmux"}, ok: true},
		{name: "terminal local", hops: []spec.TargetHop{{Transport: "tmux"}}, wantRest: []string{"tmux"}},
		{name: "missing grpc", hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "tmux"}}, wantRest: []string{"ssh", "tmux"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := spec.TargetSpec{Deployment: "placement", WorkingDir: "/work", Hops: test.hops}
			dial, remaining, ok := splitTargetProcessPair(target)
			if ok != test.ok {
				t.Fatalf("split ok = %v, want %v", ok, test.ok)
			}
			if ok && (len(dial.Hops) != len(test.hops) || dial.Hops[0].Transport != test.wantFirst) {
				t.Fatalf("dial route = %#v, want full route starting %s", dial.Hops, test.wantFirst)
			}
			gotRest := make([]string, 0, len(remaining.Hops))
			for _, hop := range remaining.Hops {
				gotRest = append(gotRest, hop.Transport)
			}
			if strings.Join(gotRest, ",") != strings.Join(test.wantRest, ",") {
				t.Fatalf("remaining route = %v, want %v", gotRest, test.wantRest)
			}
			if remaining.Deployment != target.Deployment || remaining.WorkingDir != target.WorkingDir {
				t.Fatalf("orthogonal target fields were lost: %#v", remaining)
			}
		})
	}
}

type testKindProvider struct{ word string }

func (p testKindProvider) Reserved() string     { return p.word }
func (p testKindProvider) Class() ProviderClass { return ClassKind }
func (p testKindProvider) Invoke(_ context.Context, _ *Operation) (*Result, error) {
	return &Result{JSON: []byte("{}")}, nil
}
