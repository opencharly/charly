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
//
// It ALSO sets a per-run XDG_CONFIG_HOME override BEFORE any test runs — the R3
// generalization of a real host-state leak (RCA 2026-07-20, Cutover B unit 3+4): a
// `charly` test writing deploy state goes through deploykit.SaveDeployState /
// LoadDeployConfigForRead, which resolve the per-host overlay path INDEPENDENTLY via
// kit.DefaultDeployConfigPath (kit.DeployConfigEnv if set, else os.UserConfigDir()
// joined with "charly/charly.yml") — never any package-main var a single test might
// locally override (e.g. the charly-side DeployConfigPath var, which
// host_build_pod_config_seams_test.go overrode without ALSO setting the env var, and
// still leaked). A test that never thinks about isolation at all previously fell
// through to the operator's REAL ~/.config/charly/charly.yml — confirmed on this
// host: fixture keys from multiple tests (check-addcandy-pod, plus historical
// residue from before the vm-state / deploy-save test families gained their own
// isolation: check-other-vm, vm:e00..vm:e11, vm:one, vm:two) were found
// contaminating the real file alongside the operator's actual deploys.
//
// The override is XDG_CONFIG_HOME, NOT kit.DeployConfigEnv — deliberately. Most
// existing tests already isolate via `t.Setenv("XDG_CONFIG_HOME", dir)` (which
// redirects os.UserConfigDir(), the kit.DeployConfigEnv fallback path); a FEW
// isolate via `t.Setenv(kit.DeployConfigEnv, path)` directly. kit.DefaultDeployConfigPath
// checks kit.DeployConfigEnv FIRST and unconditionally wins when non-empty — so
// setting kit.DeployConfigEnv package-wide here (tried first, reverted: every
// XDG_CONFIG_HOME-isolated test failed, each one now silently sharing ONE
// process-wide temp file instead of its own, because kit.DeployConfigEnv's
// package-wide value shadowed their per-test XDG_CONFIG_HOME override completely)
// would defeat every test using the OTHER mechanism. XDG_CONFIG_HOME composes
// correctly with BOTH: a test doing nothing falls through to this safe default; a
// test setting kit.DeployConfigEnv itself still wins (unaffected, since this default
// leaves kit.DeployConfigEnv unset); a test setting XDG_CONFIG_HOME itself simply
// shadows this default with its own value for that one test, exactly as before this
// change existed. The temp dir is removed after the whole run.
func TestMain(m *testing.M) {
	if err := loadBuiltinPluginUnits(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: loadBuiltinPluginUnits: %v\n", err)
		os.Exit(1)
	}

	configHomeDir, err := os.MkdirTemp("", "charly-test-xdg-config-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: create per-run XDG_CONFIG_HOME temp dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", configHomeDir); err != nil {
		os.RemoveAll(configHomeDir)
		fmt.Fprintf(os.Stderr, "TestMain: set XDG_CONFIG_HOME: %v\n", err)
		os.Exit(1)
	}

	// os.Exit runs no deferred calls, so the temp-dir cleanup must happen BEFORE it,
	// around the captured m.Run() code — not via defer.
	code := m.Run()
	os.RemoveAll(configHomeDir)
	os.Exit(code)
}
