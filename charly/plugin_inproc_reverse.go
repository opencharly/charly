package main

import (
	"context"

	"google.golang.org/grpc"

	specexec "github.com/opencharly/spec/exec"
	pb "github.com/opencharly/spec/proto"
)

// plugin_inproc_reverse.go — the IN-PROCESS reverse channel for a COMPILED-IN plugin.
//
// The E3b reverse channel (executorReverseServer: HostBuild / RunHostStep / RunSystem / …) is
// served to an OUT-OF-PROCESS plugin over a go-plugin gRPC broker. A COMPILED-IN plugin has no
// broker (it runs in the SAME process), so it cannot dial that gRPC server. inprocExecutorClient
// bridges the gap: it implements the proto ExecutorServiceClient by delegating DIRECTLY to a
// host-side executorReverseServer (no socket). A caller wraps it via sdk.NewInProcExecutor and
// threads the resulting *sdk.Executor onto the Invoke context (sdk.ContextWithExecutor); the
// compiled-in plugin's Invoke picks it up via sdk.ExecutorForInvoke — so the plugin's Invoke code
// is byte-identical whether compiled-in or served out-of-process (placement-invisible above the
// registry). This is the general foundation the "every builtin routes through the reverse channel"
// direction needs; the first consumer is the build:box / build:generate dispatch (dispatchBuild),
// which needs only HostBuild — the venue-op legs delegate faithfully but panic on a nil executor
// (a build reverse server carries none; HostBuild uses only the host build-engine context).
type inprocExecutorClient struct{ srv *executorReverseServer }

// hostInProcCtx returns a context carrying a FRESH in-proc reverse channel — the ONE construction of
// the compiled-in-plugin dispatch context (R3). Every host out-call to a compiled-in plugin that may
// itself call back (HostBuild, InvokeProvider, RunHostStep) needs one. The literal it replaces was
// copy-pasted at ten production sites across the check / preempt / validate / loader / command
// dispatch, and K-wave 2 cone R1's relocation of the fetch orchestration into candy/plugin-loader
// would have added several more. Each call builds its own server + executor exactly as those copies
// did — the value is stateless, so sharing the CONSTRUCTOR changes nothing about lifetime or
// isolation, it only stops the shape drifting per call site.
func hostInProcCtx() context.Context {
	return specexec.ContextWithExecutor(context.Background(),
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
}

func (c *inprocExecutorClient) Venue(ctx context.Context, in *pb.Empty, _ ...grpc.CallOption) (*pb.VenueReply, error) {
	return c.srv.Venue(ctx, in)
}

func (c *inprocExecutorClient) RunSystem(ctx context.Context, in *pb.RunRequest, _ ...grpc.CallOption) (*pb.RunReply, error) {
	return c.srv.RunSystem(ctx, in)
}

func (c *inprocExecutorClient) RunUser(ctx context.Context, in *pb.RunRequest, _ ...grpc.CallOption) (*pb.RunReply, error) {
	return c.srv.RunUser(ctx, in)
}

func (c *inprocExecutorClient) PutFile(ctx context.Context, in *pb.PutFileRequest, _ ...grpc.CallOption) (*pb.PutFileReply, error) {
	return c.srv.PutFile(ctx, in)
}

func (c *inprocExecutorClient) RunCapture(ctx context.Context, in *pb.RunRequest, _ ...grpc.CallOption) (*pb.CaptureReply, error) {
	return c.srv.RunCapture(ctx, in)
}

func (c *inprocExecutorClient) RunInteractive(ctx context.Context, in *pb.RunRequest, _ ...grpc.CallOption) (*pb.LiveReply, error) {
	return c.srv.RunInteractive(ctx, in)
}

func (c *inprocExecutorClient) RunStream(ctx context.Context, in *pb.RunRequest, _ ...grpc.CallOption) (*pb.LiveReply, error) {
	return c.srv.RunStream(ctx, in)
}

func (c *inprocExecutorClient) GetFile(ctx context.Context, in *pb.GetFileRequest, _ ...grpc.CallOption) (*pb.GetFileReply, error) {
	return c.srv.GetFile(ctx, in)
}

func (c *inprocExecutorClient) RunHostStep(ctx context.Context, in *pb.HostStepRequest, _ ...grpc.CallOption) (*pb.HostStepReply, error) {
	return c.srv.RunHostStep(ctx, in)
}

func (c *inprocExecutorClient) InvokeProvider(ctx context.Context, in *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	return c.srv.InvokeProvider(ctx, in)
}

func (c *inprocExecutorClient) HostBuild(ctx context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	return c.srv.HostBuild(ctx, in)
}

func (c *inprocExecutorClient) DescribeProvider(ctx context.Context, in *pb.DescribeProviderRequest, _ ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	return c.srv.DescribeProvider(ctx, in)
}
