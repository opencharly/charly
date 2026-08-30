package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockingProvider is a Provider whose Invoke blocks until the ctx is done —
// the shape of a hung out-of-process plugin (the fleet-del VM-member hang).
type blockingProvider struct{}

func (blockingProvider) Reserved() string     { return "blocking" }
func (blockingProvider) Class() ProviderClass { return ClassVerb }
func (blockingProvider) Invoke(ctx context.Context, _ *Operation) (*Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestInvokeTyped_FailsFastOnHungPlugin is the regression guard for the
// fleet-del VM-member hang: a host→plugin call with no caller deadline must
// apply the default timeout and fail fast with a clear error, never deadlock
// the host forever.
func TestInvokeTyped_FailsFastOnHungPlugin(t *testing.T) {
	old := defaultPluginInvokeTimeout
	defaultPluginInvokeTimeout = 100 * time.Millisecond
	defer func() { defaultPluginInvokeTimeout = old }()

	_, err := invokeTyped[struct{}, struct{}](context.Background(), blockingProvider{}, "blocking", "run", struct{}{})
	if err == nil {
		t.Fatal("invokeTyped with a hung plugin: expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invokeTyped with a hung plugin: expected context.DeadlineExceeded, got %v", err)
	}
}
