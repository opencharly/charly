package cdp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// browser_cdp_timeout_test.go — the CDP call bound must come from the step's remaining budget, not
// a fixed constant. The constant it replaces (30s) made a step's own `eventually:` retry budget
// vacuous in exactly the case retries exist for: box/fedora's cdp-web-fixture-rendered measured a
// wedged call cycle at 37.8s, so every attempt was guillotined at 30s and the step never completed
// a single one however long it was given.

// stallingCDPServer accepts a WebSocket and then answers nothing — the wedged-endpoint shape a
// loaded Chrome presents. It never writes, so every Call must end on a bound, never on a reply.
func stallingCDPServer(t *testing.T) (wsURL string) {
	t.Helper()
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		<-t.Context().Done() // hold the connection open, reply to nothing
		_ = ws.Close()
	}))
	t.Cleanup(srv.Close)
	return "ws://" + strings.TrimPrefix(srv.URL, "http://")
}

func dialStalling(t *testing.T, ctx context.Context) *CDPClient {
	t.Helper()
	c, err := NewCDPClient(ctx, stallingCDPServer(t))
	if err != nil {
		t.Fatalf("NewCDPClient() error = %v", err)
	}
	t.Cleanup(func() { _ = c.ws.Close() })
	return c
}

// TestCDPCall_StallingEndpointEndsOnTheStepBudget drives a real stalling WebSocket and asserts the
// call ends when the STEP's budget ends — not 30s later, and not immediately.
func TestCDPCall_StallingEndpointEndsOnTheStepBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	c := dialStalling(t, ctx)

	started := time.Now()
	_, err := c.Call("Runtime.evaluate", map[string]any{"expression": "1"})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("Call() against a stalling endpoint returned no error — it must end on a bound")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Call() took %s against a 600ms step budget — the bound is not derived from the step at all", elapsed)
	}
	if !strings.Contains(err.Error(), "step context ended") {
		t.Errorf("error = %q, want it to name the step context as the reason the call ended", err)
	}
	// The abandoned call must not leak its pending slot: a client reused across a step's remaining
	// calls would otherwise accumulate one dead channel per timeout.
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Errorf("client has %d pending calls after a timeout, want 0", pending)
	}
}

// TestCDPCallTimeout_DerivedFromRemainingBudget is the direct assertion on the bound itself, across
// the three cases: a generous step budget must EXPAND the bound past the old 30s constant (the
// regression that made retries vacuous), no deadline must fall back to the never-hang default, and
// a nearly-spent budget must still leave a floor rather than collapsing to zero.
func TestCDPCallTimeout_DerivedFromRemainingBudget(t *testing.T) {
	t.Run("generous step budget expands past the old constant", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		got := (&CDPClient{ctx: ctx}).callTimeout()
		if got <= cdpCallTimeoutDefault {
			t.Fatalf("callTimeout() = %s for a 90s step, want more than the %s default — a step given 90s to converge must get attempts longer than 30s", got, cdpCallTimeoutDefault)
		}
	})
	t.Run("no deadline falls back to the never-hang default", func(t *testing.T) {
		if got := (&CDPClient{ctx: context.Background()}).callTimeout(); got != cdpCallTimeoutDefault {
			t.Errorf("callTimeout() = %s, want the %s default when the step carries no deadline", got, cdpCallTimeoutDefault)
		}
	})
	t.Run("nearly-spent budget keeps the floor", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if got := (&CDPClient{ctx: ctx}).callTimeout(); got != cdpCallTimeoutFloor {
			t.Errorf("callTimeout() = %s, want the %s floor — a 0ms wait would report a browser timeout for a call never given a chance", got, cdpCallTimeoutFloor)
		}
	})
	t.Run("a nil ctx never panics", func(t *testing.T) {
		c, err := NewCDPClient(nil, stallingCDPServer(t)) //nolint:staticcheck // explicitly asserting the nil-ctx guard
		if err != nil {
			t.Fatalf("NewCDPClient(nil ctx) error = %v", err)
		}
		defer func() { _ = c.ws.Close() }()
		if got := c.callTimeout(); got != cdpCallTimeoutDefault {
			t.Errorf("callTimeout() = %s with a nil ctx, want the %s default", got, cdpCallTimeoutDefault)
		}
	})
}
