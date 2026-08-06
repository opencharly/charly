package deploypod

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives this package the same per-run XDG_CONFIG_HOME override charly's own TestMain
// (charly/test_main_test.go) established after the 2026-07-20 host-state-leak RCA — the R3
// generalization applied to the package that INHERITED the per-host-overlay write when the
// "pod-config-*" seams collapsed plugin-side (#55 Cone A Unit 2 / coneC-dsh).
//
// Why the package needs it even though every current test stubs its writes: the write path
// (saveFleet / mutateFleet → deploykit.SaveFleetConfig) resolves the overlay path
// INDEPENDENTLY through kit.DefaultDeployConfigPath (kit.DeployConfigEnv if set, else
// os.UserConfigDir() + "charly/charly.yml"). Nothing about that path consults a package var, so
// per-test var stubs protect only the tests that remember to install them: a new test that drives
// runConfig, resolveDeployPorts or persistResourceCaps without a stub writes the OPERATOR'S REAL
// ~/.config/charly/charly.yml. This host carried exactly that residue — a `check-addcandy-pod`
// entry whose resolved_image was the literal test constant `check-addcandy-pod-overlay:abc123`
// (removed via `charly fleet reset`). The stubs are the per-test contract; this is the floor
// underneath them, so "forgot to isolate" degrades to a temp dir instead of the operator's config.
//
// XDG_CONFIG_HOME, not kit.DeployConfigEnv, and for the reason charly's TestMain documents at
// length: kit.DeployConfigEnv wins unconditionally when non-empty, so setting it package-wide
// would shadow (and collapse into one shared file) every test that isolates via its own
// XDG_CONFIG_HOME. XDG_CONFIG_HOME composes with both mechanisms.
func TestMain(m *testing.M) {
	configHomeDir, err := os.MkdirTemp("", "charly-deploypod-test-xdg-config-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: create per-run XDG_CONFIG_HOME temp dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", configHomeDir); err != nil {
		_ = os.RemoveAll(configHomeDir)
		fmt.Fprintf(os.Stderr, "TestMain: set XDG_CONFIG_HOME: %v\n", err)
		os.Exit(1)
	}

	// os.Exit runs no deferred calls, so the cleanup happens around the captured m.Run().
	code := m.Run()
	_ = os.RemoveAll(configHomeDir)
	os.Exit(code)
}
