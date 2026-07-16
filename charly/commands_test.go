package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestBoxConfigSetupCmd_DirectModeAllowed — the 2026-04-27 cutover
// added direct-mode support to `charly config`. The pre-cutover version of
// this test (TestBoxConfigSetupCmd_DirectModeError) asserted that
// direct mode hard-errored at the run_mode gate; that gate is now a
// switch that accepts both modes. The setup command will still fail
// downstream of the gate (no real image, no podman), but the failure
// must NOT be the "run_mode=quadlet" gate error. Anything else means
// the gate accepted direct mode as expected.
func TestBoxConfigSetupCmd_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")
	// Isolate the per-host deploy config — a unit test must not read (or depend on
	// the validity of) the operator's real ~/.config/charly/charly.yml.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Setenv("CHARLY_RUN_MODE", "direct")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	cmd := &BoxConfigSetupCmd{Box: "fedora-test"}
	err := cmd.Run()
	// Some error is expected (image not pulled, etc.) but it must NOT
	// be the run_mode=quadlet gate error.
	if err != nil && strings.Contains(err.Error(), "charly config requires run_mode=quadlet (current") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}

// TestBoxConfigRemoveCmd_DirectModeAllowed — same logic for remove.
// Direct-mode remove should NOT hit the run_mode gate; it routes
// through the direct-deploy branch (podman stop + rm + marker cleanup).
func TestBoxConfigRemoveCmd_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Setenv("CHARLY_RUN_MODE", "direct")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	cmd := &BoxConfigRemoveCmd{Box: "fedora-test"}
	err := cmd.Run()
	// Direct-mode remove of a non-existent deploy is best-effort —
	// podman rm prints a warning but Run() returns nil. The pre-cutover
	// "run_mode=quadlet required" hard error must NOT fire.
	if err != nil && strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}
