package spec

import (
	"fmt"
	"os"
	"path/filepath"
)

// deployconfig.go — the per-host deploy-overlay path resolver (RELOCATED from sdk/kit,
// #55 value extraction). A pure host-path resolver over the deploy-config E-envelope,
// shared by charly core (var DeployConfigPath = DefaultDeployConfigPath) and the
// out-of-module candy/plugin-migrate. ONE definition (R3) — both read the same
// DeployConfigEnv override, so a check bed's per-bed isolation applies uniformly.
// sdk/kit re-exports these so existing kit.DeployConfigEnv / kit.DefaultDeployConfigPath
// call sites are untouched.

// DeployConfigEnv overrides the per-host deploy-config PATH. A check bed sets it so a
// disposable run never touches the operator's real ~/.config/charly/charly.yml.
const DeployConfigEnv = "CHARLY_DEPLOY_CONFIG"

// DefaultDeployConfigPath returns the per-host deploy overlay file
// (~/.config/charly/charly.yml), honoring the DeployConfigEnv override.
func DefaultDeployConfigPath() (string, error) {
	if p := os.Getenv(DeployConfigEnv); p != "" {
		return p, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "charly.yml"), nil
}
