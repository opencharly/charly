package main

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	pb "github.com/opencharly/spec/proto"
)

// TestInvokeProvider_FailsFastOnHungPeer is the regression guard for the
// deploy-del PLUGIN→PLUGIN hang: a plugin calling back to the host via the
// broker (executorReverseServer.InvokeProvider) with no deadline of its own
// must apply the default invoke timeout and fail fast, never block the host
// goroutine in futex_wait forever (the recurring deploy-del VM-member hang that
// #468's host→plugin guard did not cover).
func TestInvokeProvider_FailsFastOnHungPeer(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 100 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// Register a blocking provider the broker can resolve.
	RegisterBuiltinProvider(blockingProvider{})

	srv := &executorReverseServer{}
	_, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{
		Class:    "verb",
		Reserved: "blocking",
		Op:       "run",
	})
	if err == nil {
		t.Fatal("InvokeProvider with a hung peer: expected an idle-timeout error, got nil")
	}
	if !errors.Is(err, errPluginCallIdle) {
		t.Fatalf("InvokeProvider with a hung peer: expected errPluginCallIdle, got %v", err)
	}
}

// legFreeHostWorkProvider is a compiled-in (in-proc) provider doing long LEG-FREE host-side work:
// it makes no reverse call a caller could see, and burns no CPU on any peer tree — the shape of the
// overlay resolve (build:generate op=resolve), which runs entirely host-side. It does beat the clock
// on its OWN ctx, as the callee's own long host-side work does in production (host_build_cli.go:90
// wraps its blocking work in startPluginActivityHeartbeat for exactly this reason), so its own call
// clock keeps advancing. What it does not — and structurally cannot — beat is the CALLER's clock.
type legFreeHostWorkProvider struct{}

func (legFreeHostWorkProvider) Reserved() string     { return "legfree" }
func (legFreeHostWorkProvider) Class() ProviderClass { return ClassVerb }
func (legFreeHostWorkProvider) Invoke(ctx context.Context, _ *Operation) (*Result, error) {
	own := pluginActivityFrom(ctx)
	deadline := time.Now().Add(3 * pluginInvokeNoProgress())
	for time.Now().Before(deadline) {
		own.touch()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pluginInvokeNoProgress() / 4):
		}
	}
	return &Result{JSON: []byte(`{}`)}, nil
}

// TestInvokeProvider_ServedHostWorkHeartbeatsTheCallerClock is the OTHER half of the seam above,
// and the reason the idle bound is not widened by it: a peer plugin parked in exec.InvokeProvider
// has NO progress signal of its own while the host serves its request, so the SERVED dispatch must
// heartbeat the CALLER's clock (s.activity) for exactly as long as it runs — the invariant
// HostBuild already honors for the builds it runs on a plugin's behalf. Without it a legitimately
// long, entirely host-side call is false-killed as idle: the overlay resolve, measured live, ran
// with the host burning ~1.3 cores for 90 s while the peer stayed frozen at 33 ticks (#699).
//
// The caller here is a REAL live process parked in its callback — alive, burning no CPU, making no
// reverse leg, exactly the deploy-pod plugin in that measured kill — because a pid-0 clock would
// not be a faithful stand-in and would make the failure report ambiguous.
func TestInvokeProvider_ServedHostWorkHeartbeatsTheCallerClock(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 200 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	caller := exec.Command("sleep", "30")
	startInOwnGroup(t, caller)

	RegisterBuiltinProvider(legFreeHostWorkProvider{})

	// The CALLER's clock, exactly as the host builds it for the call it is serving: the clock
	// watching the peer plugin that is now parked in its own exec.InvokeProvider callback. It is
	// WATCHED here (idleBoundedContext) because that is what makes it able to kill the caller's
	// call — a clock nobody watches proves nothing.
	callerClock := newPluginActivity(caller.Process.Pid)
	ctx, cancel := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), callerClock)
	defer cancel()

	// The nested reverse server as plugin_grpc.go builds it: the caller's clock, threaded from the
	// ctx of the call being served (pluginActivityFrom).
	srv := &executorReverseServer{activity: callerClock}

	start := time.Now()
	if _, err := srv.InvokeProvider(ctx, &pb.InvokeProviderRequest{
		Class:    "verb",
		Reserved: "legfree",
		Op:       "run",
	}); err != nil {
		t.Fatalf("a served host call must keep the CALLER's clock alive: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("expected the served call to run its full duration (~600ms), got %s", elapsed)
	}
}
