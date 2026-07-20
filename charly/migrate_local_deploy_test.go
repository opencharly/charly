package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestLoadBundleConfig_LegacySchemaErrors exercises the load-time guard:
// a deploy.yml with `images:` at top level errors with a remediation hint
// pointing at the legacy filename.
func TestLoadBundleConfig_LegacySchemaErrors(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(configDir, "charly"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configDir)
	deployPath := filepath.Join(configDir, "charly", "deploy.yml")
	legacy := "images:\n  immich:\n    bind_mounts: []\n"
	if err := os.WriteFile(deployPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := deploykit.LoadBundleConfig()
	if err == nil {
		t.Fatal("LoadBundleConfig accepted legacy schema; want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "legacy `deploy.yml` filename") {
		t.Errorf("error message missing legacy-filename hint: %s", msg)
	}
	if !strings.Contains(msg, "rename it to charly.yml") {
		t.Errorf("error message missing remediation command: %s", msg)
	}
}
