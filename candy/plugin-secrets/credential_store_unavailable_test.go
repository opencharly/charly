package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveCredential_Default_vs_Unavailable verifies that the new
// "unavailable" source is returned distinctly from "default" when the
// preferred backend failed to probe — and that a clean ConfigFileStore
// session (no probe error) still returns "default".
func TestResolveCredential_Default_vs_Unavailable(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	// Case 1: explicit config backend, clean probe path, nothing stored.
	// Source should be "default".
	t.Run("clean config backend returns default", func(t *testing.T) {
		resetDefaultStore()
		defer resetDefaultStore()
		t.Setenv("CHARLY_SECRET_BACKEND", "config")
		defaultStoreProbeErr = nil

		val, source := ResolveCredential("TEST_UNSET", "charly/enc", "nonexistent", "fallback")
		if source != "default" {
			t.Errorf("source = %q, want %q", source, "default")
		}
		if val != "fallback" {
			t.Errorf("val = %q, want %q", val, "fallback")
		}
	})

	// Case 2: auto backend, probe failed (simulated via defaultStoreProbeErr),
	// storage is the ConfigFileStore fallback with nothing stored.
	// Source should be "unavailable".
	t.Run("probe-failed fallback returns unavailable", func(t *testing.T) {
		resetDefaultStore()
		defer func() {
			defaultStoreProbeErr = nil
			resetDefaultStore()
		}()
		t.Setenv("CHARLY_SECRET_BACKEND", "config")

		// Force DefaultCredentialStore() to materialize as ConfigFileStore
		// (triggers sync.Once).
		_ = DefaultCredentialStore()
		// Simulate a probe failure captured during auto-dispatch.
		defaultStoreProbeErr = errSimulatedProbeFail

		val, source := ResolveCredential("TEST_UNSET", "charly/enc", "nonexistent", "fallback")
		if source != "unavailable" {
			t.Errorf("source = %q, want %q", source, "unavailable")
		}
		if val != "fallback" {
			t.Errorf("val = %q, want %q", val, "fallback")
		}
	})

	// Case 3: probe failed but the credential IS in the config fallback.
	// Source should be "config", not "unavailable" — the fallback served the value.
	t.Run("probe-failed but config has value returns config", func(t *testing.T) {
		resetDefaultStore()
		defer func() {
			defaultStoreProbeErr = nil
			resetDefaultStore()
		}()

		// Prime config.yml with a VNC password (CredServiceVNC is the only
		// config-storable service in the current code — good enough for this
		// verification).
		cfgPath := filepath.Join(dir, "config.yml")
		_ = os.Remove(cfgPath)
		cfs := &ConfigFileStore{}
		if err := cfs.Set(CredServiceVNC, "testimg", "testpw"); err != nil {
			t.Fatalf("seeding config: %v", err)
		}

		t.Setenv("CHARLY_SECRET_BACKEND", "config")
		_ = DefaultCredentialStore()
		defaultStoreProbeErr = errSimulatedProbeFail

		val, source := ResolveCredential("TEST_UNSET", CredServiceVNC, "testimg", "fallback")
		if source != "config" {
			t.Errorf("source = %q, want %q", source, "config")
		}
		if val != "testpw" {
			t.Errorf("val = %q, want %q", val, "testpw")
		}
	})
}

// errSimulatedProbeFail is a sentinel error used by tests to mark
// defaultStoreProbeErr without actually invoking a broken keyring.
var errSimulatedProbeFail = simulatedProbeError("simulated probe failure for test")

type simulatedProbeError string

func (e simulatedProbeError) Error() string { return string(e) }
