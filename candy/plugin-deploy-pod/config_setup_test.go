package deploypod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestRunPodConfigSetup_DirectModeAllowed — relocated from the deleted charly-core
// commands_test.go's TestBoxConfigSetupCmd_DirectModeAllowed onto the P13-KERNEL direction-flip's
// ported runPodConfigSetup. The run_mode gate (quadlet OR direct accepted) is the FIRST thing
// runPodConfigSetup checks, before any executor/HostBuild use, so it is safe to call with a nil
// *sdk.Executor and recover from the inevitable nil-executor panic once past the gate — the
// original test's invariant was never "no error at all", only "not the gate error", which holds
// unchanged.
func TestRunPodConfigSetup_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Setenv("CHARLY_RUN_MODE", "direct")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Past the gate, into a code path that needed a real executor — exactly
				// the "some other error" the original test tolerated, just surfaced as a
				// panic instead of a returned error since ex is nil in this unit test.
				err = nil
			}
		}()
		err = runPodConfigSetup(t.Context(), nil, &spec.PodConfigSetupRequest{Box: "fedora-test"})
	}()
	if err != nil && strings.Contains(err.Error(), "charly config requires run_mode=quadlet or direct (current") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}
