package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain mirrors production startup for the whole charly test package: it runs the
// sync.Once loadBuiltinPluginUnits() (the builtin plugin-schema load the binary performs
// at boot) BEFORE any test, so plugin-verb validation (validateAuthoredPluginInput) sees
// the builtin #<Word>Input defs in EVERY test invocation — not only when a sibling test
// (plugin_external_test.go) happens to trigger the load first.
//
// Without this, the plugin-verb tests passed in the full `go test ./...` suite but FAILED
// in a narrow `-run` subset, because pluginSchemas is process-global and sync.Once-filled
// — a test-isolation dependency surfaced while landing the external-charly-verb dispatch
// enabler (v2026.173.1058). loadBuiltinPluginUnits is idempotent (builtinGateOnce), so a
// later sibling call is a no-op.
func TestMain(m *testing.M) {
	if err := loadBuiltinPluginUnits(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: loadBuiltinPluginUnits: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
