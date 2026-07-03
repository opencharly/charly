package main

import (
	"path/filepath"
	"testing"
)

// A check bed isolates its per-host deploy config via CHARLY_DEPLOY_CONFIG so concurrent
// beds never share (and corrupt) the operator's ~/.config/charly/charly.yml.
func TestDeployConfigPath_EnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, want)
	got, err := DeployConfigPath()
	if err != nil {
		t.Fatalf("DeployConfigPath: %v", err)
	}
	if got != want {
		t.Errorf("DeployConfigPath with %s set = %q, want the isolated path %q", DeployConfigEnv, got, want)
	}
}

func TestDeployConfigPath_DefaultWhenUnset(t *testing.T) {
	t.Setenv(DeployConfigEnv, "") // empty → falls through to os.UserConfigDir
	got, err := DeployConfigPath()
	if err != nil {
		t.Fatalf("DeployConfigPath: %v", err)
	}
	if filepath.Base(got) != "charly.yml" || !filepath.IsAbs(got) {
		t.Errorf("default path = %q, want an absolute .../charly/charly.yml", got)
	}
}
