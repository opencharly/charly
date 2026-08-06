package kube

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"google.golang.org/grpc"
)

// fakeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double: every method
// panics EXCEPT HostBuild, which dispatches by req.Kind to the ONE canned reply this test needs —
// "deploy-plugins-connect" (a canned project dir; deployVMForwards resolves it unconditionally
// now, K-wave W3a A3-phase-2). The vm-entity resolve + the persisted VmState read are both stubbed
// via package vars (resolveVmEntityForForwards, hostConfigResolveVmState — k3s_post.go) instead
// of faked through HostBuild — a single HostBuild-kind stub cannot canned-reply the multi-leg
// loaderkit.LoadUnifiedViaExecutor chain the real loaderkit helpers now drive (mirrors
// candy/plugin-deploy-pod's loadProjectVolume/saveBundle stub pattern, R3). This is enough to
// drive deployVMForwards end-to-end without a real host reverse-channel broker.
type fakeExecutorServiceClient struct {
	projectDir string
}

func (f *fakeExecutorServiceClient) HostBuild(_ context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	switch in.GetKind() {
	case "deploy-plugins-connect":
		b, err := json.Marshal(spec.DeployPluginsConnectReply{Dir: f.projectDir})
		if err != nil {
			return nil, err
		}
		return &pb.HostBuildReply{ResultJson: b}, nil
	default:
		panic("fakeExecutorServiceClient.HostBuild: unexpected kind " + in.GetKind())
	}
}

// stubVmEntityForForwards replaces resolveVmEntityForForwards for the duration of a test —
// deployVMForwards' kind:vm self-load call — with a canned reply, restored via t.Cleanup.
func stubVmEntityForForwards(t *testing.T, vm *spec.ResolvedVm) {
	t.Helper()
	orig := resolveVmEntityForForwards
	resolveVmEntityForForwards = func(context.Context, *sdk.Executor, string, string) (*spec.ResolvedVm, error) {
		return vm, nil
	}
	t.Cleanup(func() { resolveVmEntityForForwards = orig })
}

// stubVmStateForForwards replaces hostConfigResolveVmState for the duration of a test — the
// persisted VmDeployState read — with a canned reply, restored via t.Cleanup.
func stubVmStateForForwards(t *testing.T, state *spec.VmDeployState) {
	t.Helper()
	orig := hostConfigResolveVmState
	hostConfigResolveVmState = func(context.Context, *sdk.Executor, string) (*spec.VmDeployState, error) {
		return state, nil
	}
	t.Cleanup(func() { hostConfigResolveVmState = orig })
}

func (f *fakeExecutorServiceClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) InvokeProvider(context.Context, *pb.InvokeProviderRequest, ...grpc.CallOption) (*pb.InvokeReply, error) {
	panic("unused")
}
func (f *fakeExecutorServiceClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	panic("unused")
}

// TestDeployVMForwards_ReadsPersistedAllocationViaConfigResolveSeam is the regression test for
// the R10 check-k8s-deploy bed failure: "auto port_forward \"auto:6443\" has no persisted
// host-port allocation" even though `charly vm create`'s own persist landed correctly and stayed
// on disk. Root cause: deployVMForwards used to call deploykit.LoadDeployConfigForRead directly —
// a helper that ALWAYS returns an empty config when deploykit.DeployStateHost is nil, which it is
// inside every out-of-process plugin (candy/plugin-kube is never compiled-in), regardless of
// what is actually persisted on disk. This test proves the fix: deployVMForwards now resolves the
// persisted VmDeployState via the plugin-side hostConfigResolveVmState →
// loaderkit.ResolveVmStateViaExecutor read (the config-resolve seam is DELETED, K-wave 2 cone
// R2 bank D), which drives the host loader regardless of placement.
func TestDeployVMForwards_ReadsPersistedAllocationViaVmStateRead(t *testing.T) {
	stubVmEntityForForwards(t, &spec.ResolvedVm{Network: &spec.VmNetwork{PortForwards: []string{"auto:6443"}}})
	stubVmStateForForwards(t, &spec.VmDeployState{PortForwards: map[string]int{"6443": 34325}})
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	resolved, err := deployVMForwards(context.Background(), exec, "k3s-vm", "rca-deploy4")
	if err != nil {
		t.Fatalf("deployVMForwards: %v", err)
	}
	want := []string{"34325:6443"}
	if len(resolved) != 1 || resolved[0] != want[0] {
		t.Fatalf("deployVMForwards = %v, want %v", resolved, want)
	}
}

// TestDeployVMForwards_NoPersistedAllocation_StillErrorsLoudly proves the error path survives
// the fix: when the VmState read genuinely reports no VmState (e.g. `charly vm create`
// never ran), the loud "no persisted host-port allocation" error still fires — this is NOT a
// case the fix should silently swallow, only the "the read was broken, not the write" case above.
func TestDeployVMForwards_NoPersistedAllocation_StillErrorsLoudly(t *testing.T) {
	stubVmEntityForForwards(t, &spec.ResolvedVm{Network: &spec.VmNetwork{PortForwards: []string{"auto:6443"}}})
	stubVmStateForForwards(t, nil) // no VmState at all
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	_, err := deployVMForwards(context.Background(), exec, "k3s-vm", "rca-deploy4")
	if err == nil {
		t.Fatalf("deployVMForwards: want an error when no allocation is persisted, got nil")
	}
}

// TestDeployVMForwards_EmptyVmEntity_NoOp is the regression test for task #18 (the k3s
// shared-entity clobber fix): now that ArtifactKey is domain-scoped and no longer parseable as
// "vm:<entity>", deployVMForwards takes the shared kind:vm entity name as an EXPLICIT parameter
// (VmEntity, threaded separately over the wire) instead of stripping a "vm:" prefix off the
// artifact key. A pod/local k3s-server deploy (or any caller that never resolved an entity) ships
// an empty vmEntity — this must short-circuit to a clean no-op (nil, nil), never dial the host
// reverse channel (the fake client here panics on any HostBuild call, proving no call was made).
func TestDeployVMForwards_EmptyVmEntity_NoOp(t *testing.T) {
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	resolved, err := deployVMForwards(context.Background(), exec, "", "check-local")
	if err != nil {
		t.Fatalf("deployVMForwards: want nil error for an empty vmEntity, got %v", err)
	}
	if resolved != nil {
		t.Fatalf("deployVMForwards: want a nil result for an empty vmEntity, got %v", resolved)
	}
}
