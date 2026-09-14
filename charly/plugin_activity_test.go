package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opencharly/spec/ops"
	pb "github.com/opencharly/spec/proto"
)

// progressThenReturnProvider performs host-visible progress on the shared per-call
// clock for a while (the shape of a long rebuild whose host child is alive), then
// returns successfully — like check-omarchy-iso-vm's [update]: ~13min of a running
// install, far past the old 10m total cap, with NO idle gap.
type progressThenReturnProvider struct{}

func (progressThenReturnProvider) Reserved() string     { return "progress" }
func (progressThenReturnProvider) Class() ProviderClass { return ClassVerb }
func (progressThenReturnProvider) Invoke(ctx context.Context, _ *Operation) (*Result, error) {
	// A real peer's progress is its HOST-visible reverse legs (which touch s.activity);
	// here we touch the same clock the host threaded onto our ctx, standing in for
	// those legs. Steady progress across 3x the no-progress window, then success.
	clock := pluginActivityFrom(ctx)
	window := pluginInvokeNoProgress()
	deadline := time.Now().Add(3 * window)
	for time.Now().Before(deadline) {
		clock.touch()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(window / 4):
		}
	}
	return &Result{JSON: []byte(`{}`)}, nil
}

// TestInvokeProvider_ProgressingLongCallIsNotKilled is THE regression guard for the
// real defect: a plugin call that keeps performing host-visible work must survive
// LONGER than the old fixed 10m total cap. The former code killed it at exactly the
// cap (`context deadline exceeded`) though it was progressing; the idle bound must
// let it finish.
func TestInvokeProvider_ProgressingLongCallIsNotKilled(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 200 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	RegisterBuiltinProvider(progressThenReturnProvider{})
	srv := &executorReverseServer{}

	start := time.Now()
	// 600ms of steady progress — well past the 200ms idle window, the shape of a
	// 13min install vs the old 10min cap.
	if _, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{
		Class: "verb", Reserved: "progress", Op: string(ops.OpRun),
	}); err != nil {
		t.Fatalf("a progressing long call must NOT be killed by the idle bound: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("expected the call to run its full duration (~600ms), got %s", elapsed)
	}
}

// TestIdleBoundedContext_TripsOnlyWhenIdle pins the core distinction directly: with
// steady activity the bound never fires; once activity stops, it fires with the sentinel.
func TestIdleBoundedContext_TripsOnlyWhenIdle(t *testing.T) {
	old := pluginInvokeNoProgressOverride
	pluginInvokeNoProgressOverride = 150 * time.Millisecond
	defer func() { pluginInvokeNoProgressOverride = old }()

	// Idle: no touches → cancelled with the sentinel.
	a := &pluginActivity{}
	ctx, stop := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), a)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), errPluginCallIdle) {
			t.Fatalf("idle call: expected errPluginCallIdle, got %v", context.Cause(ctx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle call was not cancelled")
	}
	stop()

	// Progressing: a toucher every 50ms (< the 150ms window) → never cancelled.
	b := &pluginActivity{}
	ctx2, stop2 := idleBoundedContext(context.Background(), pluginInvokeNoProgress(), b)
	touchStop := make(chan struct{})
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-touchStop:
				return
			case <-tk.C:
				b.touch()
			}
		}
	}()
	select {
	case <-ctx2.Done():
		close(touchStop)
		t.Fatalf("progressing call must not be cancelled, got %v", context.Cause(ctx2))
	case <-time.After(600 * time.Millisecond):
		// good: outlived 4x the idle window while progressing
	}
	close(touchStop)
	stop2()
}
