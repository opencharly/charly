package main

import "testing"

// reapTrackingCloser counts Close() calls — a stand-in for a connected plugin's
// clientCloser (whose real Close() does client.Kill()).
type reapTrackingCloser struct{ closes int }

func (c *reapTrackingCloser) Close() error { c.closes++; return nil }

// TestReapPlugins_ClosesRegisteredPluginClients proves reapPlugins (the host's
// exit-path reaper wired into main's defer, post-dispatch call, and shutdown
// hook) runs the registry's plugin-connection closers — the client side of the
// orphan-leak fix — and is idempotent (a second reap is a no-op, never a
// double-Close), matching the os.Exit-skips-defers reality main relies on.
func TestReapPlugins_ClosesRegisteredPluginClients(t *testing.T) {
	tc := &reapTrackingCloser{}
	// ps=nil + a non-nil conn registers ONLY the closer (no provider words), so
	// this test never collides with a reserved word in the global registry.
	if err := providerRegistry.RegisterPluginProviders(nil, "test-reap", tc); err != nil {
		t.Fatalf("register closer: %v", err)
	}

	reapPlugins()
	if tc.closes != 1 {
		t.Fatalf("reapPlugins closed the plugin client %d times; want 1", tc.closes)
	}

	// Idempotent: closers were taken + nilled under the lock, so a second reap
	// (the defer firing after the explicit post-dispatch reap) must not re-Close.
	reapPlugins()
	if tc.closes != 1 {
		t.Fatalf("second reapPlugins re-closed the client (%d total); Close must be idempotent", tc.closes)
	}
}
