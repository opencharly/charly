package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// egress_test.go — the egress shim's contract, relocated from the deleted charly/egress_test.go
// (K-wave 2 cone R2). The shim (egress.go) Invokes verb:egress OpValidate over the reverse
// channel; the VALIDATION LOGIC + CUE schemas live in candy/plugin-egress, whose own
// egress_test.go (TestEgressValidate + the golden cloud-init fixture test) covers the real
// schema. This package's test binary links no charly core, so verb:egress is unreachable here —
// the tests drive the shim against a stub executor that answers OpValidate with canned replies,
// proving the shim's marshalling, reply decode, error propagation, and graceful-degrade contract.

// egressStubClient is a pb.ExecutorServiceClient that answers InvokeProvider("verb","egress",
// OpValidate) with a canned error reply per kind ("" = valid) and captures the last params for
// assertion. Every other method errors loudly (unreachable by these tests).
type egressStubClient struct {
	replies    map[string]string
	lastParams map[string]string
}

func (e *egressStubClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	return nil, fmt.Errorf("egressStubClient: Venue not implemented")
}
func (e *egressStubClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunSystem not implemented")
}
func (e *egressStubClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunUser not implemented")
}
func (e *egressStubClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	return nil, fmt.Errorf("egressStubClient: PutFile not implemented")
}
func (e *egressStubClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunCapture not implemented")
}
func (e *egressStubClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunInteractive not implemented")
}
func (e *egressStubClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunStream not implemented")
}
func (e *egressStubClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	return nil, fmt.Errorf("egressStubClient: GetFile not implemented")
}
func (e *egressStubClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	return nil, fmt.Errorf("egressStubClient: RunHostStep not implemented")
}
func (e *egressStubClient) HostBuild(context.Context, *pb.HostBuildRequest, ...grpc.CallOption) (*pb.HostBuildReply, error) {
	return nil, fmt.Errorf("egressStubClient: HostBuild not implemented")
}
func (e *egressStubClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	return nil, fmt.Errorf("egressStubClient: DescribeProvider not implemented")
}
func (e *egressStubClient) InvokeProvider(_ context.Context, in *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	if in.GetClass() != "verb" || in.GetReserved() != "egress" || in.GetOp() != sdk.OpValidate {
		return nil, fmt.Errorf("egressStubClient: unexpected InvokeProvider class=%q word=%q op=%q", in.GetClass(), in.GetReserved(), in.GetOp())
	}
	var p struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Mode  string `json:"mode"`
		Data  string `json:"data"`
	}
	if err := json.Unmarshal(in.GetParamsJson(), &p); err != nil {
		return nil, fmt.Errorf("egressStubClient: decode params: %w", err)
	}
	e.lastParams = map[string]string{"kind": p.Kind, "label": p.Label, "mode": p.Mode, "data": p.Data}
	out, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: e.replies[p.Kind]})
	return &pb.InvokeReply{ResultJson: out}, nil
}

// testEgressExecutor wires a stub-backed executor into the package command context for the
// duration of one test, restoring the nil (no-reverse-channel) state on cleanup so sibling tests
// that rely on the graceful-degrade path stay isolated.
func testEgressExecutor(t *testing.T, replies map[string]string) *egressStubClient {
	t.Helper()
	stub := &egressStubClient{replies: replies}
	setCommandContext(context.Background(), sdk.NewInProcExecutor(stub))
	t.Cleanup(func() { setCommandContext(context.TODO(), nil) })
	return stub
}

func TestValidateEgress_GoodPasses(t *testing.T) {
	stub := testEgressExecutor(t, map[string]string{"cloud_config": ""})
	good := []byte("hostname: vm1\nusers:\n  - default\n")
	if err := ValidateEgress("cloud_config", "good user-data", good); err != nil {
		t.Fatalf("good cloud-config should validate, got: %v", err)
	}
	// The shim must marshal the artifact as {kind,label,mode:"bytes",data} — the verb:egress wire.
	if stub.lastParams["kind"] != "cloud_config" || stub.lastParams["mode"] != "bytes" || stub.lastParams["data"] != string(good) {
		t.Fatalf("shim marshalled wrong params: %+v", stub.lastParams)
	}
}

func TestValidateEgress_BadFails(t *testing.T) {
	testEgressExecutor(t, map[string]string{"cloud_config": "cloud_config: validation failed"})
	if err := ValidateEgress("cloud_config", "bad user-data", []byte("package_update: \"yes\"\n")); err == nil {
		t.Fatal("malformed cloud-config must be REJECTED, got nil")
	}
}

func TestValidateEgress_UnknownKind(t *testing.T) {
	testEgressExecutor(t, map[string]string{"no-such-kind": "no egress schema registered for kind"})
	if err := ValidateEgress("no-such-kind", "x", []byte("a: 1\n")); err == nil {
		t.Fatal("unknown egress kind must error, got nil")
	}
}

func TestValidateEgressValue_GoodPasses(t *testing.T) {
	testEgressExecutor(t, map[string]string{"k8s_object": ""})
	good := map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec":     map[string]any{"replicas": 2},
	}
	if err := ValidateEgressValue("k8s_object", "good deployment", good); err != nil {
		t.Fatalf("valid kubernetes object should pass, got: %v", err)
	}
}

func TestValidateEgressValue_BadFails(t *testing.T) {
	testEgressExecutor(t, map[string]string{"k8s_object": "k8s_object: validation failed"})
	bad := map[string]any{"apiVersion": "v1", "kind": "", "metadata": map[string]any{"name": "x"}}
	if err := ValidateEgressValue("k8s_object", "bad deployment", bad); err == nil {
		t.Fatal("malformed kubernetes object must be REJECTED, got nil")
	}
}

func TestValidateEgressValue_DeployRecord(t *testing.T) {
	stub := testEgressExecutor(t, map[string]string{"deploy_record": ""})
	good := map[string]any{"deploy_id": "abc123", "image": "ghcr.io/x/y:tag", "target": "host", "deployed_at": "2026-06-15T00:00:00Z"}
	if err := ValidateEgressValue("deploy_record", "good deploy rec", good); err != nil {
		t.Fatalf("valid deploy record should pass, got: %v", err)
	}
	// The value must be marshalled to JSON bytes before the egress gate sees it.
	if stub.lastParams["kind"] != "deploy_record" || stub.lastParams["mode"] != "bytes" {
		t.Fatalf("shim marshalled wrong params: %+v", stub.lastParams)
	}
	var round map[string]any
	if err := json.Unmarshal([]byte(stub.lastParams["data"]), &round); err != nil {
		t.Fatalf("shim did not marshal the value to JSON: %v", err)
	}
	if round["deploy_id"] != "abc123" {
		t.Fatalf("shim marshalled wrong value: %+v", round)
	}
}

func TestValidateEgressValue_CandyRecord(t *testing.T) {
	testEgressExecutor(t, map[string]string{"candy_record": ""})
	good := map[string]any{"candy": "ripgrep", "deployed_by": []string{"abc123"}, "deployed_at": "2026-06-15T00:00:00Z"}
	if err := ValidateEgressValue("candy_record", "good candy rec", good); err != nil {
		t.Fatalf("valid candy record should pass, got: %v", err)
	}
}

func TestValidateTextEgress_RenderedText(t *testing.T) {
	stub := testEgressExecutor(t, map[string]string{"rendered_text": ""})
	good := "FROM fedora:43\nRUN dnf install -y git\nUSER 1000\n"
	if err := egressValidate("rendered_text", "good containerfile", "text", good); err != nil {
		t.Fatalf("clean rendered text should pass, got: %v", err)
	}
	if stub.lastParams["mode"] != "text" {
		t.Fatalf("text mode must ride the wire as mode=text, got %+v", stub.lastParams)
	}
	// teeth: a Go text/template nil-field marker means a render failure.
	testEgressExecutor(t, map[string]string{"rendered_text": "rendered_text: validation failed"})
	bad := "[Service]\nExecStart=<no value>\nRestart=always\n"
	if err := egressValidate("rendered_text", "broken unit", "text", bad); err == nil {
		t.Fatal("rendered text containing the template-failure marker <no value> must be REJECTED, got nil")
	}
}

// TestEgressValidate_NoReverseChannelDegrades proves the graceful-degrade contract: a nil
// reverse-channel executor (a non-command context) skips validation rather than failing the
// write — the SAME contract the moved VM CLI's vm_egress_shim.go documents, and what keeps the
// ledger-write tests (recordDeploy/recordVenueLedger, which call spec.ValidateRecord without a
// command context) green.
func TestEgressValidate_NoReverseChannelDegrades(t *testing.T) {
	setCommandContext(context.TODO(), nil)
	if err := egressValidate("deploy_record", "no-channel", "bytes", "{}"); err != nil {
		t.Fatalf("no reverse channel must degrade gracefully, got: %v", err)
	}
}

// TestEgressValidate_ProviderErrorIsLoud proves the loud-on-missing-provider contract: a
// reachable-but-failing provider (InvokeProvider returns an error) must abort the write loudly,
// never silently pass.
func TestEgressValidate_ProviderErrorIsLoud(t *testing.T) {
	stub := &egressStubClient{replies: map[string]string{}}
	setCommandContext(context.Background(), sdk.NewInProcExecutor(&egressErrorClient{inner: stub}))
	t.Cleanup(func() { setCommandContext(context.TODO(), nil) })
	if err := ValidateEgress("deploy_record", "provider-down", []byte("{}")); err == nil {
		t.Fatal("a failing egress provider must be LOUD, got nil")
	}
}

// egressErrorClient wraps an egressStubClient and fails every InvokeProvider call — the
// "provider not registered / unreachable" case the shim must propagate loudly.
type egressErrorClient struct {
	inner *egressStubClient
}

func (e *egressErrorClient) InvokeProvider(ctx context.Context, in *pb.InvokeProviderRequest, opts ...grpc.CallOption) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("verb:egress provider not reachable")
}
func (e *egressErrorClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	return nil, fmt.Errorf("egressErrorClient: Venue not implemented")
}
func (e *egressErrorClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunSystem not implemented")
}
func (e *egressErrorClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunUser not implemented")
}
func (e *egressErrorClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	return nil, fmt.Errorf("egressErrorClient: PutFile not implemented")
}
func (e *egressErrorClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunCapture not implemented")
}
func (e *egressErrorClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunInteractive not implemented")
}
func (e *egressErrorClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunStream not implemented")
}
func (e *egressErrorClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	return nil, fmt.Errorf("egressErrorClient: GetFile not implemented")
}
func (e *egressErrorClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	return nil, fmt.Errorf("egressErrorClient: RunHostStep not implemented")
}
func (e *egressErrorClient) HostBuild(context.Context, *pb.HostBuildRequest, ...grpc.CallOption) (*pb.HostBuildReply, error) {
	return nil, fmt.Errorf("egressErrorClient: HostBuild not implemented")
}
func (e *egressErrorClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	return nil, fmt.Errorf("egressErrorClient: DescribeProvider not implemented")
}
