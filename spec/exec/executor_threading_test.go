package exec

import (
	"context"
	"errors"
	"testing"
)

// executor_threading_test.go covers the in-proc ctx-threading path (ContextWithExecutor /
// ExecutorFromContext / ExecutorForInvoke) and the HostStepDeps threading — the pure-Go
// additions relocated from the sdk root. The gRPC/broker path (ExecutorFromInvoke with a
// real broker) is exercised by spec/transport's channel tests; here we prove the ctx path
// returns the threaded executor WITHOUT touching the broker (the compiled-in placement's
// contract: no broker needed).

func TestContextWithExecutor_RoundTrip(t *testing.T) {
	e := &Executor{}
	ctx := context.Background()
	out, ok := ExecutorFromContext(ctx)
	if ok || out != nil {
		t.Fatal("ExecutorFromContext on a bare ctx returned a non-nil executor")
	}
	ctx2 := ContextWithExecutor(ctx, e)
	out, ok = ExecutorFromContext(ctx2)
	if !ok || out == nil {
		t.Fatal("ExecutorFromContext on a threaded ctx returned ok=false / nil")
	}
	if out != e {
		t.Fatal("ExecutorFromContext returned a different *Executor than the one threaded")
	}
}

func TestExecutorForInvoke_InProcCtxWins(t *testing.T) {
	// The in-proc path: a ctx-threaded executor is returned WITHOUT calling the broker
	// (brokerID is ignored on this path — no broker is dialed).
	e := &Executor{}
	ctx := ContextWithExecutor(context.Background(), e)
	got, err := ExecutorForInvoke(ctx, 0)
	if err != nil {
		t.Fatalf("ExecutorForInvoke(ctx-threaded, 0) err = %v, want nil", err)
	}
	if got != e {
		t.Fatal("ExecutorForInvoke did not return the ctx-threaded executor")
	}
}

func TestExecutorForInvoke_NoCtxNoBrokerID_Errors(t *testing.T) {
	// The out-of-process fallback: no ctx executor AND no broker — a clear error, not a
	// silent nil. (transport.ServedBroker is nil in a fresh test process that never served.)
	got, err := ExecutorForInvoke(context.Background(), 0)
	if err == nil {
		t.Fatal("ExecutorForInvoke with no ctx executor and brokerID=0 returned nil error")
	}
	if got != nil {
		t.Fatalf("ExecutorForInvoke returned non-nil executor %v alongside an error", got)
	}
	// The error must name the missing broker (not, e.g., a panic).
	if !errors.Is(err, err) { // sanity: err is a real error value
		t.Fatal("err is not a real error value")
	}
}

func TestExecutorForInvoke_NonZeroBrokerID_NoBroker_Errors(t *testing.T) {
	// A non-zero brokerID with no broker dialed (transport.ServedBroker is nil in tests)
	// surfaces the "no go-plugin broker" error rather than panicking.
	if _, err := ExecutorForInvoke(context.Background(), 42); err == nil {
		t.Fatal("ExecutorForInvoke with a non-zero brokerID but no broker returned nil error")
	}
}

func TestNewInProcExecutor_WrapsClient(t *testing.T) {
	// NewInProcExecutor is the IN-PROCESS twin of the broker path; it wraps any client the
	// host's in-proc adapter delegates to. We assert it constructs a non-nil *Executor whose
	// ctx-threading round-trips (the client itself is exercised via the gRPC integration
	// tests in spec/transport).
	e := NewInProcExecutor(nil)
	if e == nil {
		t.Fatal("NewInProcExecutor(nil) returned nil")
	}
	ctx := ContextWithExecutor(context.Background(), e)
	if got, ok := ExecutorFromContext(ctx); !ok || got != e {
		t.Fatal("NewInProcExecutor result did not round-trip through ctx threading")
	}
}

func TestContextWithHostStepDeps_RoundTrip(t *testing.T) {
	deps := &HostStepDeps{Exec: nil, ResolveImage: func(s string) (string, error) { return s, nil }}
	ctx := context.Background()
	if got := HostStepDepsFromCtx(ctx); got != nil {
		t.Fatalf("HostStepDepsFromCtx on bare ctx = %v, want nil", got)
	}
	ctx2 := ContextWithHostStepDeps(ctx, deps)
	got := HostStepDepsFromCtx(ctx2)
	if got == nil {
		t.Fatal("HostStepDepsFromCtx on a threaded ctx returned nil")
	}
	if got != deps {
		t.Fatal("HostStepDepsFromCtx returned a different *HostStepDeps than the one threaded")
	}
	// The closure survives the round-trip (it is a live value, never serialized).
	if _, err := got.ResolveImage("ref"); err != nil {
		t.Fatalf("ResolveImage closure after ctx round-trip err = %v", err)
	}
}

func TestContextWithHostStepDeps_NilRoundTrip(t *testing.T) {
	// Threading a nil *HostStepDeps is recoverable as nil (an out-of-process placement, or a
	// test that threads none) — the caller fails loudly at the one host-engine step that
	// needs them, never a silent wrong result.
	ctx := ContextWithHostStepDeps(context.Background(), nil)
	if got := HostStepDepsFromCtx(ctx); got != nil {
		t.Fatalf("HostStepDepsFromCtx after threading nil = %v, want nil", got)
	}
}
