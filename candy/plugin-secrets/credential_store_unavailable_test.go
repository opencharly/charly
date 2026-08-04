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

	// These drive resolveStoreChain through withClassifierState — the active store and the probe
	// error set directly — instead of through CHARLY_SECRET_BACKEND. The env pin used to be
	// `config`, a backend that no longer exists; more importantly it conflated two things, since
	// what these cases actually vary is the CLASSIFIER's two inputs, not which backend a setting
	// selects. Setting them directly is what lets case 1 hold a combination the current selector
	// no longer produces on its own (see the CHANGELOG note on headless fast-fail).

	// Case 1: the config store is active and the probe was clean — nothing failed, the
	// credential simply is not stored. Source should be "default", which callers treat as
	// terminal: no amount of retrying conjures a credential that was never stored.
	t.Run("clean config store returns default", func(t *testing.T) {
		withClassifierState(t, &ConfigFileStore{}, nil)

		val, source := ResolveCredential("TEST_UNSET", "charly/enc", "nonexistent", "fallback")
		if source != "default" {
			t.Errorf("source = %q, want %q", source, "default")
		}
		if val != "fallback" {
			t.Errorf("val = %q, want %q", val, "fallback")
		}
	})

	// Case 2: the config store is active because the keyring probe FAILED, and nothing is
	// stored. Source should be "unavailable", which callers treat as retryable — the keyring
	// may come up shortly after boot.
	t.Run("probe-failed fallback returns unavailable", func(t *testing.T) {
		withClassifierState(t, &ConfigFileStore{}, errSimulatedProbeFail)

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
		// Prime config.yml with a VNC password (CredServiceVNC is the only
		// config-storable service in the current code — good enough for this
		// verification).
		cfgPath := filepath.Join(dir, "config.yml")
		_ = os.Remove(cfgPath)
		cfs := &ConfigFileStore{}
		if err := cfs.Set(CredServiceVNC, "testimg", "testpw"); err != nil {
			t.Fatalf("seeding config: %v", err)
		}

		withClassifierState(t, &ConfigFileStore{}, errSimulatedProbeFail)

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
