package main

import (
	"context"
	"errors"
	"strings"
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
// TOTAL-duration bound (pluginLeafCapDefault, via pluginLeafCap; the
// pluginLeafCapOverride seam shrank it for this test).
//
// The error must be IDENTIFIABLE as the leaf cap and must still carry the deadline, so a
// caller can tell "the bound fired" from "the plugin failed" — the distinction the raw
// `context canceled` did not make (a >10m cold image build was misread as a crash).
func TestInvokeTyped_FailsFastOnHungPlugin(t *testing.T) {
	old := pluginLeafCapOverride
	pluginLeafCapOverride = 100 * time.Millisecond
	defer func() { pluginLeafCapOverride = old }()

	_, err := invokeTyped[struct{}, struct{}](context.Background(), blockingProvider{}, "blocking", "run", struct{}{})
	if err == nil {
		t.Fatal("invokeTyped with a hung plugin: expected a leaf-cap error, got nil")
	}
	if !errors.Is(err, errPluginLeafCap) {
		t.Fatalf("invokeTyped with a hung plugin: error is not identifiable as the leaf cap: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invokeTyped with a hung plugin: the leaf-cap error must still carry context.DeadlineExceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "CHARLY_PLUGIN_LEAF_CAP") {
		t.Errorf("the leaf-cap error must name the knob that raises it: %v", err)
	}
}
