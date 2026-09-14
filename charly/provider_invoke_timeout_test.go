package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockingProvider is a Provider whose Invoke blocks until the ctx is done —
// the shape of a hung out-of-process plugin (the deploy-del VM-member hang).
type blockingProvider struct{}

func (blockingProvider) Reserved() string     { return "blocking" }
func (blockingProvider) Class() ProviderClass { return ClassVerb }
func (blockingProvider) Invoke(ctx context.Context, _ *Operation) (*Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestInvokeTyped_FailsFastOnHungPlugin is the regression guard for the
// deploy-del VM-member hang on the host→plugin LEAF path: it attaches no reverse
// channel, so a hung plugin offers no progress signal and the guard is a
// TOTAL-duration bound (the readiness absolute_cap, via pluginLeafCap).
func TestInvokeTyped_FailsFastOnHungPlugin(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 100 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	_, err := invokeTyped[struct{}, struct{}](context.Background(), blockingProvider{}, "blocking", "run", struct{}{})
	if err == nil {
		t.Fatal("invokeTyped with a hung plugin: expected an idle-timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invokeTyped with a hung plugin: expected context.DeadlineExceeded (the leaf cap), got %v", err)
	}
}
