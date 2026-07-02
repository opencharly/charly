package vmshared

import (
	"sync/atomic"
	"testing"
)

// TestShutdownHooks_RunRegistered proves a registered shutdown hook runs when
// the signal handler invokes runShutdownHooks — the seam main uses to reap
// connected plugin clients on SIGTERM/SIGINT/SIGHUP (the orphan-leak fix).
func TestShutdownHooks_RunRegistered(t *testing.T) {
	var ran atomic.Int64
	RegisterShutdownHook(func() { ran.Add(1) })
	RegisterShutdownHook(nil) // nil is a no-op (must not panic)

	runShutdownHooks()
	if got := ran.Load(); got != 1 {
		t.Fatalf("registered shutdown hook ran %d times; want 1", got)
	}
}
