package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// from_box_pod_test.go — the pod path of `charly fleet from-box`, relocated from the deleted
// charly/fleet_from_box_cmd.go (K-wave 2 cone R2 bank B). runFromBoxPod reaches deploy:pod's
// OpConfigSetup by direct InvokeProvider — the tests drive that dispatch against a stub executor
// and assert the marshalled PodConfigSetupRequest (ExplicitRef + the HostEnv threaded as DATA on
// the OpRun dispatch), the quadlet service start, and the empty-ref guard.

// fromBoxStubClient is a pb.ExecutorServiceClient that answers InvokeProvider("deploy","pod",
// OpConfigSetup) with an empty reply and captures the request for assertion. Every other method
// errors loudly (unreachable by these tests).
type fromBoxStubClient struct {
	lastClass string
	lastWord  string
	lastOp    string
	lastReq   specPodConfigSetupRequest
}

// specPodConfigSetupRequest is the subset of spec.PodConfigSetupRequest these tests assert.
type specPodConfigSetupRequest struct {
	Box         string          `json:"box"`
	Instance    string          `json:"instance,omitempty"`
	Env         []string        `json:"env,omitempty"`
	Port        []string        `json:"port,omitempty"`
	ExplicitRef string          `json:"explicit_ref,omitempty"`
	HostEnvJSON json.RawMessage `json:"host_env_json,omitempty"`
}

func (e *fromBoxStubClient) InvokeProvider(_ context.Context, in *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	e.lastClass = in.GetClass()
	e.lastWord = in.GetReserved()
	e.lastOp = in.GetOp()
	if in.GetClass() != "deploy" || in.GetReserved() != "pod" || in.GetOp() != sdk.OpConfigSetup {
		return nil, fmt.Errorf("fromBoxStubClient: unexpected InvokeProvider class=%q word=%q op=%q", in.GetClass(), in.GetReserved(), in.GetOp())
	}
	if err := json.Unmarshal(in.GetParamsJson(), &e.lastReq); err != nil {
		return nil, fmt.Errorf("fromBoxStubClient: decode params: %w", err)
	}
	return &pb.InvokeReply{}, nil
}
func (e *fromBoxStubClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: Venue not implemented")
}
func (e *fromBoxStubClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunSystem not implemented")
}
func (e *fromBoxStubClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunUser not implemented")
}
func (e *fromBoxStubClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: PutFile not implemented")
}
func (e *fromBoxStubClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunCapture not implemented")
}
func (e *fromBoxStubClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunInteractive not implemented")
}
func (e *fromBoxStubClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunStream not implemented")
}
func (e *fromBoxStubClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: GetFile not implemented")
}
func (e *fromBoxStubClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: RunHostStep not implemented")
}
func (e *fromBoxStubClient) HostBuild(context.Context, *pb.HostBuildRequest, ...grpc.CallOption) (*pb.HostBuildReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: HostBuild not implemented")
}
func (e *fromBoxStubClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	return nil, fmt.Errorf("fromBoxStubClient: DescribeProvider not implemented")
}

// testFromBoxExecutor wires a stub-backed executor into the package command context + stashes the
// threaded HostEnv JSON for the duration of one test, restoring nil state on cleanup.
func testFromBoxExecutor(t *testing.T, hostEnvJSON json.RawMessage) *fromBoxStubClient {
	t.Helper()
	stub := &fromBoxStubClient{}
	setCommandContext(context.Background(), sdk.NewInProcExecutor(stub))
	cmdHostEnvJSON = hostEnvJSON
	t.Cleanup(func() {
		setCommandContext(context.TODO(), nil)
		cmdHostEnvJSON = nil
	})
	return stub
}

func TestRunFromBoxPod_EmptyRefErrors(t *testing.T) {
	if err := runFromBoxPod(&FleetFromBoxCmd{}); err == nil {
		t.Fatal("an empty ref must error, got nil")
	}
}

func TestRunFromBoxPod_InvokesConfigSetupWithExplicitRef(t *testing.T) {
	stub := testFromBoxExecutor(t, json.RawMessage(`{"charly_bin":"/usr/bin/charly"}`))
	// Direct mode avoids the quadlet systemctl start.
	t.Setenv("CHARLY_RUN_MODE", "direct")

	c := &FleetFromBoxCmd{
		Ref:      "ghcr.io/opencharly/selkies-kde-nvidia:2026.153.1026",
		Instance: "work",
		Env:      []string{"FOO=bar"},
		Port:     []string{"5901:5900"},
	}
	if err := runFromBoxPod(c); err != nil {
		t.Fatalf("runFromBoxPod: %v", err)
	}
	if stub.lastClass != "deploy" || stub.lastWord != "pod" || stub.lastOp != sdk.OpConfigSetup {
		t.Fatalf("runFromBoxPod dispatched wrong provider: class=%q word=%q op=%q", stub.lastClass, stub.lastWord, stub.lastOp)
	}
	// The deploy name derives from the image ref basename (no explicit Name).
	if stub.lastReq.Box != "selkies-kde-nvidia" {
		t.Fatalf("deploy name not derived from ref: %q", stub.lastReq.Box)
	}
	if stub.lastReq.Instance != "work" || stub.lastReq.ExplicitRef != c.Ref {
		t.Fatalf("request missing instance/explicit_ref: %+v", stub.lastReq)
	}
	// The host spec.HostEnv threaded as DATA on OpRun must ride into HostEnvJSON.
	if string(stub.lastReq.HostEnvJSON) != `{"charly_bin":"/usr/bin/charly"}` {
		t.Fatalf("HostEnvJSON not threaded as data: %s", stub.lastReq.HostEnvJSON)
	}
}

func TestRunFromBoxPod_NameOverride(t *testing.T) {
	stub := testFromBoxExecutor(t, nil)
	t.Setenv("CHARLY_RUN_MODE", "direct")

	if err := runFromBoxPod(&FleetFromBoxCmd{Ref: "ghcr.io/opencharly/redis:7", Name: "my-redis"}); err != nil {
		t.Fatalf("runFromBoxPod: %v", err)
	}
	if stub.lastReq.Box != "my-redis" {
		t.Fatalf("explicit name must win over the derived one, got %q", stub.lastReq.Box)
	}
}

func TestRunFromBoxPod_QuadletStartsService(t *testing.T) {
	testFromBoxExecutor(t, nil)
	t.Setenv("CHARLY_RUN_MODE", "quadlet")

	// Stub systemctl with a fake that records its args.
	bin := t.TempDir()
	called := filepath.Join(bin, "systemctl.called")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + called + "\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake systemctl: %v", err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	if err := runFromBoxPod(&FleetFromBoxCmd{Ref: "ghcr.io/opencharly/redis:7"}); err != nil {
		t.Fatalf("runFromBoxPod: %v", err)
	}
	data, err := os.ReadFile(called)
	if err != nil {
		t.Fatalf("fake systemctl not invoked: %v", err)
	}
	// The fake records one arg per line: `--user start charly-redis.service`.
	if string(data) != "--user\nstart\ncharly-redis.service\n" {
		t.Fatalf("systemctl invoked with wrong args: %q", string(data))
	}
}
