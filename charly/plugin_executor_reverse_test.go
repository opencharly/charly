package main

import (
	"context"
	"net"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
)

// reverseFakeExec is a fake DeployExecutor that records the scripts it is asked to run
// — enough to prove the reverse-channel server forwards a plugin's RunSystem/RunUser/
// Venue calls to the live executor. The embedded nil DeployExecutor satisfies the
// methods executorReverseServer never calls.
type reverseFakeExec struct {
	deploykit.DeployExecutor
	lastSystem, lastUser string

	// PutFile capture: the bytes the server materialized + the placement args.
	putContent   []byte
	putRemote    string
	putMode      uint32
	putOwnerRoot bool
}

func (r *reverseFakeExec) Venue() string { return "fake-venue" }
func (r *reverseFakeExec) RunSystem(_ context.Context, s string, _ deploykit.EmitOpts) error {
	r.lastSystem = s
	return nil
}
func (r *reverseFakeExec) RunUser(_ context.Context, s string, _ deploykit.EmitOpts) error {
	r.lastUser = s
	return nil
}

// PutFile reads back the host temp file the reverse server materialized from the
// wire-carried bytes, so the test can assert the content + placement args survived the
// RPC and reached the live executor.
func (r *reverseFakeExec) PutFile(_ context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, _ deploykit.EmitOpts) error {
	b, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	r.putContent = b
	r.putRemote = remotePath
	r.putMode = mode
	r.putOwnerRoot = ownerRoot
	return nil
}

// TestExecutorReverseServer_DelegatesToExecutor proves the HOST side of the E3b
// reverse channel: executorReverseServer, served over gRPC (here on an in-memory
// bufconn, exactly as it is served on the go-plugin broker in production), forwards a
// caller's Venue/RunSystem/RunUser to the live DeployExecutor — so an out-of-process
// deploy/step/builder plugin's call-backs reach the real venue. Fails if the server
// does not delegate.
func TestExecutorReverseServer_DelegatesToExecutor(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	fake := &reverseFakeExec{}
	srv := grpc.NewServer()
	pb.RegisterExecutorServiceServer(srv, &executorReverseServer{exec: fake})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.Dial("bufnet", //nolint:staticcheck // grpc.Dial is fine for an in-memory bufconn test
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewExecutorServiceClient(conn)
	ctx := context.Background()

	if _, err := client.RunSystem(ctx, &pb.RunRequest{Script: "echo system"}); err != nil {
		t.Fatalf("RunSystem: %v", err)
	}
	if fake.lastSystem != "echo system" {
		t.Fatalf("RunSystem did not reach the executor, got %q", fake.lastSystem)
	}
	if _, err := client.RunUser(ctx, &pb.RunRequest{Script: "echo user"}); err != nil {
		t.Fatalf("RunUser: %v", err)
	}
	if fake.lastUser != "echo user" {
		t.Fatalf("RunUser did not reach the executor, got %q", fake.lastUser)
	}
	v, err := client.Venue(ctx, &pb.Empty{})
	if err != nil || v.GetVenue() != "fake-venue" {
		t.Fatalf("Venue: %q err=%v", v.GetVenue(), err)
	}
}

// TestExecutorReverseServer_PutFile proves the F2 file-PLACEMENT leg: a plugin's
// reverse-channel PutFile (binary-safe content + path + mode + owner_root) is
// materialized to a host temp file by executorReverseServer and forwarded to the live
// DeployExecutor.PutFile — so an out-of-process deploy/step plugin that EXECUTES an
// InstallPlan's steps can push files (units, env.d, the charly binary, builder
// artifacts) onto the venue. Fails if the bytes or the placement args do not survive.
func TestExecutorReverseServer_PutFile(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	fake := &reverseFakeExec{}
	srv := grpc.NewServer()
	pb.RegisterExecutorServiceServer(srv, &executorReverseServer{exec: fake})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.Dial("bufnet", //nolint:staticcheck // grpc.Dial is fine for an in-memory bufconn test
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewExecutorServiceClient(conn)
	ctx := context.Background()

	// Binary content (an embedded NUL) proves the bytes path is not string-mangled.
	content := []byte("unit body\x00\nExecStart=/usr/bin/true\n")
	reply, err := client.PutFile(ctx, &pb.PutFileRequest{
		Path:      "/etc/systemd/system/charly-x.service",
		Content:   content,
		Mode:      0o600,
		OwnerRoot: true,
	})
	if err != nil {
		t.Fatalf("PutFile RPC: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("PutFile reply error: %s", reply.GetError())
	}
	if string(fake.putContent) != string(content) {
		t.Fatalf("PutFile content did not survive: got %q want %q", fake.putContent, content)
	}
	if fake.putRemote != "/etc/systemd/system/charly-x.service" {
		t.Fatalf("PutFile remote path wrong: %q", fake.putRemote)
	}
	if fake.putMode != 0o600 {
		t.Fatalf("PutFile mode wrong: %o", fake.putMode)
	}
	if !fake.putOwnerRoot {
		t.Fatalf("PutFile owner_root did not survive")
	}
}
