package main

import (
	"strings"
	"testing"
)

// TestResolveAndroidHostPortRef covers the parse paths of the nested-endpoint
// ${HOST_PORT:N} resolver. The live resolution (inspecting a running parent pod)
// is exercised by the check-android-emulator-pod R10 bed's device-net leg.
func TestResolveAndroidHostPortRef(t *testing.T) {
	// A literal host:port (no ${HOST_PORT}) passes through unchanged.
	if got, err := resolveAndroidHostPortRef("192.168.1.50:5555", "stack.device-net", nil); err != nil || got != "192.168.1.50:5555" {
		t.Fatalf("literal: got (%q, %v), want (192.168.1.50:5555, nil)", got, err)
	}
	// ${HOST_PORT:N} on a non-nested device (no parent in the deploy path) errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:5037}", "toplevel", nil); err == nil || !strings.Contains(err.Error(), "not nested") {
		t.Fatalf("no-parent: expected a 'not nested' error, got %v", err)
	}
	// Non-numeric container port errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:abc}", "stack.device-net", nil); err == nil || !strings.Contains(err.Error(), "positive container port") {
		t.Fatalf("malformed: expected a 'positive container port' error, got %v", err)
	}
	// Missing closing brace errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:5037", "stack.device-net", nil); err == nil || !strings.Contains(err.Error(), "closing brace") {
		t.Fatalf("no-brace: expected a 'closing brace' error, got %v", err)
	}
}
