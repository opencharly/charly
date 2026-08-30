package main

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/opencharly/spec/proto"
)

// TestInvokeProvider_FailsFastOnHungPeer is the regression guard for the
// fleet-del PLUGIN→PLUGIN hang: a plugin calling back to the host via the
// broker (executorReverseServer.InvokeProvider) with no deadline of its own
// must apply the default invoke timeout and fail fast, never block the host
// goroutine in futex_wait forever (the recurring fleet-del VM-member hang that
// #468's host→plugin guard did not cover).
func TestInvokeProvider_FailsFastOnHungPeer(t *testing.T) {
	old := defaultPluginInvokeTimeout
	defaultPluginInvokeTimeout = 100 * time.Millisecond
	defer func() { defaultPluginInvokeTimeout = old }()

	// Register a blocking provider the broker can resolve.
	RegisterBuiltinProvider(blockingProvider{})

	srv := &executorReverseServer{}
	_, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{
		Class:    "verb",
		Reserved: "blocking",
		Op:       "run",
	})
	if err == nil {
		t.Fatal("InvokeProvider with a hung peer: expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InvokeProvider with a hung peer: expected context.DeadlineExceeded, got %v", err)
	}
}
