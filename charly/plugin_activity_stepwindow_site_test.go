package main

import (
	"os"
	"strings"
	"testing"
)

// TestStepWindowCallSiteUsesTheClassSelector pins the WIRING, not just the helper
// mapping: invokeProvider must pass the dispatch class to the window selector. A
// regression to the bare pluginInvokeNoProgress() would silently restore the 90s
// kill for step installs while the mapping test stayed green.
func TestStepWindowCallSiteUsesTheClassSelector(t *testing.T) {
	src, err := os.ReadFile("plugin_dispatch_reverse.go")
	if err != nil {
		t.Fatalf("reading plugin_dispatch_reverse.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "idleBoundedContext(ctx, pluginInvokeNoProgressFor(class), activity)") {
		t.Fatal("the dispatch call site does not pass the class to the idle-window selector")
	}
	if strings.Contains(body, "idleBoundedContext(ctx, pluginInvokeNoProgress(), activity)") {
		t.Fatal("the dispatch call site still uses the bare probe window")
	}
}
