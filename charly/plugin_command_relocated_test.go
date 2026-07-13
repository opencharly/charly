package main

import (
	"context"
	"strings"
	"testing"
)

// TestRelocatedCommandVerb_DispatchesViaKit proves the `command` check verb — relocated to
// candy/plugin-command (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter) and runs all three exec paths: in-container
// (via the fake executor), host-side foreground (real `sh -c echo` + a stdout matcher), and
// background (real `sh -c sleep`, fire-and-forget). Exercises the new CheckContext
// AddBackground extension (nil-Scenario → no-op).
func TestRelocatedCommandVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("command")
	if !ok {
		t.Fatal("command verb not registered — compiled-in kit candy (candy/plugin-command) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("command provider is not a CheckVerbProvider: %T", prov)
	}

	// In-container: the wrapped command runs via the executor, exit 0 → pass.
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "{ ", exit: 0}}}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, RunModeLive),
		&Op{PluginInput: map[string]any{"command": "true"}})
	if res.Status != TestPass {
		t.Fatalf("in-container: want pass, got %v: %s", res.Status, res.Message)
	}

	// Host-side foreground: real `sh -c 'echo …'` with a stdout contains matcher → pass.
	res2 := cv.RunVerb(context.Background(), hostVerbResolverFor(nil, RunModeLive),
		&Op{PluginInput: map[string]any{"command": "echo charly-cmd-ok", "from_host": true},
			Stdout: MatcherList{{Op: "contains", Value: "charly-cmd-ok"}}})
	if res2.Status != TestPass {
		t.Fatalf("host-foreground: want pass, got %v: %s", res2.Status, res2.Message)
	}

	// Background: real `sh -c 'sleep …'`, fire-and-forget → pass with a pid message.
	res3 := cv.RunVerb(context.Background(), hostVerbResolverFor(nil, RunModeLive),
		&Op{PluginInput: map[string]any{"command": "sleep 0.2", "from_host": true, "background": true}})
	if res3.Status != TestPass || !strings.Contains(res3.Message, "backgrounded") {
		t.Fatalf("background: want pass + backgrounded, got %v: %s", res3.Status, res3.Message)
	}
}
