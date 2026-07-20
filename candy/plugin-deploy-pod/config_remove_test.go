package deploypod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestRunPodConfigRemove_DirectModeAllowed — relocated from the deleted charly-core
// commands_test.go's TestBoxConfigRemoveCmd_DirectModeAllowed onto the P13-KERNEL direction-flip's
// ported runPodConfigRemove. Direct-mode remove does NOT hit the run_mode=quadlet gate; it routes
// through the direct-deploy branch (podman stop + rm + marker cleanup) — no executor/HostBuild
// round-trip needed for this path, so it is callable directly in a unit test.
func TestRunPodConfigRemove_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Setenv("CHARLY_RUN_MODE", "direct")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	err := runPodConfigRemove(&spec.PodConfigRemoveRequest{Box: "fedora-test"})
	// Direct-mode remove of a non-existent deploy is best-effort — podman rm prints a
	// warning but returns nil. The pre-cutover "run_mode=quadlet required" hard error must NOT fire.
	if err != nil && strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}
