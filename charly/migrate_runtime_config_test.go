package main

// migrate_runtime_config_test.go — the CORE runtime-config loader's clean-load
// path (LoadRuntimeConfig + hostenv.RuntimeConfigPath). The legacy kdbx-residual reject
// gate was removed at the migration-baseline reset.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/hostenv"
)

func TestLoadRuntimeConfig_CleanConfigOK(t *testing.T) {
	dir := t.TempDir()
	origPath := hostenv.RuntimeConfigPath
	hostenv.RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
	defer func() { hostenv.RuntimeConfigPath = origPath }()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("secret_backend: keyring\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.LoadRuntimeConfig(); err != nil {
		t.Fatalf("clean config should load, got: %v", err)
	}
}
