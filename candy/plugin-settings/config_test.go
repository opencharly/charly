package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// config_test.go — ported from charly/runtime_config_test.go (wave γ, the config-subsystem
// relocation into this plugin). Every test here exercises a non-credential-touching key
// (engine.*, run_mode, auto_enable, bind_address), so ctx/exec are safely nil/background —
// GetConfigValue/SetConfigValue/ResetConfigValue never reach credentialCall for these keys.
// Coverage preserved 1:1 from the core tests.

var testCtx = context.Background()

func TestSetConfigValue_Validates(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	err := SetConfigValue(testCtx, nil, "engine.build", "containerd")
	if err == nil {
		t.Error("expected error for invalid engine value")
	}

	err = SetConfigValue(testCtx, nil, "run_mode", "swarm")
	if err == nil {
		t.Error("expected error for invalid run_mode value")
	}

	err = SetConfigValue(testCtx, nil, "engine.build", "podman")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := GetConfigValue(testCtx, nil, "engine.build")
	if err != nil {
		t.Fatalf("GetConfigValue() error: %v", err)
	}
	if val != "podman" {
		t.Errorf("GetConfigValue() = %q, want %q", val, "podman")
	}
}

func TestResetConfigValue(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	if err := SetConfigValue(testCtx, nil, "engine.build", "podman"); err != nil {
		t.Fatal(err)
	}
	if err := ResetConfigValue(testCtx, nil, "engine.build"); err != nil {
		t.Fatal(err)
	}

	val, _ := GetConfigValue(testCtx, nil, "engine.build")
	if val != "" {
		t.Errorf("after reset, GetConfigValue() = %q, want empty", val)
	}
}

func TestResetConfigValue_All(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	if err := SetConfigValue(testCtx, nil, "engine.build", "podman"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(testCtx, nil, "run_mode", "quadlet"); err != nil {
		t.Fatal(err)
	}
	if err := ResetConfigValue(testCtx, nil, ""); err != nil {
		t.Fatal(err)
	}

	cfg, _ := kit.LoadRuntimeConfig()
	if cfg.Engine.Build != "" || cfg.RunMode != "" {
		t.Errorf("after full reset, config should be empty, got %+v", cfg)
	}
}

func TestListConfigValues(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")
	_ = os.Unsetenv("CHARLY_ENCRYPTED_STORAGE_PATH")
	_ = os.Unsetenv("CHARLY_SECRET_BACKEND")
	_ = os.Unsetenv("CHARLY_VM_BACKEND")
	_ = os.Unsetenv("CHARLY_VM_DISK_SIZE")
	_ = os.Unsetenv("CHARLY_VM_RAM")
	_ = os.Unsetenv("CHARLY_VM_CPUS")

	if err := SetConfigValue(testCtx, nil, "engine.build", "podman"); err != nil {
		t.Fatal(err)
	}

	vals, err := ListConfigValues()
	if err != nil {
		t.Fatalf("ListConfigValues() error: %v", err)
	}
	if len(vals) != 19 {
		t.Fatalf("expected 19 values, got %d", len(vals))
	}

	// engine.build should come from config
	if vals[0].Key != "engine.build" || vals[0].Value != "podman" || vals[0].Source != "config" {
		t.Errorf("engine.build entry: %+v", vals[0])
	}
	// engine.run should be default "auto"
	if vals[1].Key != "engine.run" || vals[1].Value != "auto" || vals[1].Source != "default" {
		t.Errorf("engine.run entry: %+v", vals[1])
	}
	// engine.rootful should be default "auto"
	if vals[2].Key != "engine.rootful" || vals[2].Value != "auto" || vals[2].Source != "default" {
		t.Errorf("engine.rootful entry: %+v", vals[2])
	}
	// auto_enable should be default true
	if vals[4].Key != "auto_enable" || vals[4].Value != "true" || vals[4].Source != "default" {
		t.Errorf("auto_enable entry: %+v", vals[4])
	}
	// bind_address should be default 127.0.0.1
	if vals[5].Key != "bind_address" || vals[5].Value != "127.0.0.1" || vals[5].Source != "default" {
		t.Errorf("bind_address entry: %+v", vals[5])
	}
}

func TestGetConfigValue_UnknownKey(t *testing.T) {
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_, err := GetConfigValue(testCtx, nil, "foo.bar")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

// assertConfigKeySetGetReset points kit.RuntimeConfigPath at a fresh temp config and
// exercises one config key's set/get/invalid/reset lifecycle: set valid1 + read
// it back, set valid2 + read it back, reject invalid, then reset to empty.
// Shared by the per-key *_SetGetReset tests (R3).
func assertConfigKeySetGetReset(t *testing.T, key, valid1, valid2, invalid string) {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	t.Cleanup(func() { kit.RuntimeConfigPath = orig })
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// First valid value.
	if err := SetConfigValue(testCtx, nil, key, valid1); err != nil {
		t.Fatalf("SetConfigValue(%s, %s) error: %v", key, valid1, err)
	}
	val, err := GetConfigValue(testCtx, nil, key)
	if err != nil {
		t.Fatalf("GetConfigValue(%s) error: %v", key, err)
	}
	if val != valid1 {
		t.Errorf("GetConfigValue(%s) = %q, want %q", key, val, valid1)
	}

	// Second valid value.
	if err := SetConfigValue(testCtx, nil, key, valid2); err != nil {
		t.Fatalf("SetConfigValue(%s, %s) error: %v", key, valid2, err)
	}
	val, _ = GetConfigValue(testCtx, nil, key)
	if val != valid2 {
		t.Errorf("GetConfigValue(%s) = %q, want %q", key, val, valid2)
	}

	// Invalid value is rejected.
	if err := SetConfigValue(testCtx, nil, key, invalid); err == nil {
		t.Errorf("expected error for invalid %s value", key)
	}

	// Reset clears it.
	if err := ResetConfigValue(testCtx, nil, key); err != nil {
		t.Fatalf("ResetConfigValue(%s) error: %v", key, err)
	}
	val, _ = GetConfigValue(testCtx, nil, key)
	if val != "" {
		t.Errorf("after reset, GetConfigValue(%s) = %q, want empty", key, val)
	}
}

func TestAutoEnable_SetGetReset(t *testing.T) {
	assertConfigKeySetGetReset(t, "auto_enable", "true", "false", "yes")
}

func TestAutoEnable_EnvOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	// Config says false
	if err := SetConfigValue(testCtx, nil, "auto_enable", "false"); err != nil {
		t.Fatal(err)
	}

	// Env says true
	_ = os.Setenv("CHARLY_AUTO_ENABLE", "true")
	defer os.Unsetenv("CHARLY_AUTO_ENABLE") //nolint:errcheck

	rt, err := kit.ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if !rt.AutoEnable {
		t.Error("AutoEnable should be true when CHARLY_AUTO_ENABLE=true overrides config")
	}
}

func TestAutoEnable_ListConfigValues(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	if err := SetConfigValue(testCtx, nil, "auto_enable", "true"); err != nil {
		t.Fatal(err)
	}

	vals, err := ListConfigValues()
	if err != nil {
		t.Fatalf("ListConfigValues() error: %v", err)
	}

	// Find auto_enable entry
	found := false
	for _, v := range vals {
		if v.Key == "auto_enable" {
			found = true
			if v.Value != "true" || v.Source != "config" {
				t.Errorf("auto_enable entry: %+v, want value=true source=config", v)
			}
		}
	}
	if !found {
		t.Error("auto_enable not found in ListConfigValues output")
	}
}

func TestBindAddress_SetGetReset(t *testing.T) {
	assertConfigKeySetGetReset(t, "bind_address", "0.0.0.0", "127.0.0.1", "192.168.1.1")
}

func TestBindAddress_EnvOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")

	// Config says 127.0.0.1
	if err := SetConfigValue(testCtx, nil, "bind_address", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	// Env says 0.0.0.0
	_ = os.Setenv("CHARLY_BIND_ADDRESS", "0.0.0.0")
	defer os.Unsetenv("CHARLY_BIND_ADDRESS") //nolint:errcheck

	rt, err := kit.ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q, want %q (env should override config)", rt.BindAddress, "0.0.0.0")
	}
}
