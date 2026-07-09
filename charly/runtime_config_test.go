package main

import (
	"github.com/opencharly/sdk/kit"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfig_Missing(t *testing.T) {
	// Point to a non-existent path
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()

	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "nonexistent", "config.yml"), nil
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing config, got: %v", err)
	}
	if cfg.Engine.Build != "" || cfg.Engine.Run != "" || cfg.RunMode != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestSaveAndLoadRuntimeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	cfg := &RuntimeConfig{
		Engine:  EngineConfig{Build: "podman", Run: "docker"},
		RunMode: "quadlet",
	}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error: %v", err)
	}

	loaded, err := LoadRuntimeConfig()
	if err != nil {
		t.Fatalf("LoadRuntimeConfig() error: %v", err)
	}
	if loaded.Engine.Build != "podman" {
		t.Errorf("Engine.Build = %q, want %q", loaded.Engine.Build, "podman")
	}
	if loaded.Engine.Run != "docker" {
		t.Errorf("Engine.Run = %q, want %q", loaded.Engine.Run, "docker")
	}
	if loaded.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want %q", loaded.RunMode, "quadlet")
	}
}

func TestResolveRuntime_Defaults(t *testing.T) {
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	// Ensure env vars are clear
	for _, key := range []string{"CHARLY_BUILD_ENGINE", "CHARLY_RUN_ENGINE", "CHARLY_RUN_MODE", "CHARLY_AUTO_ENABLE", "CHARLY_BIND_ADDRESS"} {
		_ = os.Unsetenv(key)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	// With auto-detection, the resolved engine should be "podman" or "docker"
	// depending on what's available on the system (not "auto")
	if rt.BuildEngine != "podman" && rt.BuildEngine != "docker" {
		t.Errorf("BuildEngine = %q, want \"podman\" or \"docker\"", rt.BuildEngine)
	}
	if rt.RunEngine != "podman" && rt.RunEngine != "docker" {
		t.Errorf("RunEngine = %q, want \"podman\" or \"docker\"", rt.RunEngine)
	}
	// With auto-detection, run mode is "quadlet" when podman+systemctl present, else "direct"
	if rt.RunMode != "direct" && rt.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want \"direct\" or \"quadlet\"", rt.RunMode)
	}
	if !rt.AutoEnable {
		t.Error("AutoEnable should default to true")
	}
	if rt.BindAddress != "127.0.0.1" {
		t.Errorf("BindAddress = %q, want %q", rt.BindAddress, "127.0.0.1")
	}
}

func TestResolveRuntime_EnvOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// Write config with podman
	cfg := &RuntimeConfig{Engine: EngineConfig{Build: "podman"}}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Set env to override
	_ = os.Setenv("CHARLY_BUILD_ENGINE", "docker")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BuildEngine != "docker" {
		t.Errorf("BuildEngine = %q, want %q (env should override config)", rt.BuildEngine, "docker")
	}
}

func TestResolveRuntime_InvalidEngine(t *testing.T) {
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_ = os.Setenv("CHARLY_BUILD_ENGINE", "containerd")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	_, err := ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid engine")
	}
}

func TestResolveRuntime_InvalidRunMode(t *testing.T) {
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")
	_ = os.Setenv("CHARLY_RUN_MODE", "swarm")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	_, err := ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid run_mode")
	}
}

func TestSetConfigValue_Validates(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	err := SetConfigValue("engine.build", "containerd")
	if err == nil {
		t.Error("expected error for invalid engine value")
	}

	err = SetConfigValue("run_mode", "swarm")
	if err == nil {
		t.Error("expected error for invalid run_mode value")
	}

	err = SetConfigValue("engine.build", "podman")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := GetConfigValue("engine.build")
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

	if err := SetConfigValue("engine.build", "podman"); err != nil {
		t.Fatal(err)
	}
	if err := ResetConfigValue("engine.build"); err != nil {
		t.Fatal(err)
	}

	val, _ := GetConfigValue("engine.build")
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

	if err := SetConfigValue("engine.build", "podman"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue("run_mode", "quadlet"); err != nil {
		t.Fatal(err)
	}
	if err := ResetConfigValue(""); err != nil {
		t.Fatal(err)
	}

	cfg, _ := LoadRuntimeConfig()
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

	if err := SetConfigValue("engine.build", "podman"); err != nil {
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

	_, err := GetConfigValue("foo.bar")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestResolveValue(t *testing.T) {
	tests := []struct {
		env, cfg, def, want string
	}{
		{"podman", "docker", "docker", "podman"},
		{"", "podman", "docker", "podman"},
		{"", "", "docker", "docker"},
	}
	for _, tt := range tests {
		got := ResolveValue(tt.env, tt.cfg, tt.def)
		if got != tt.want {
			t.Errorf("ResolveValue(%q, %q, %q) = %q, want %q", tt.env, tt.cfg, tt.def, got, tt.want)
		}
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
	if err := SetConfigValue(key, valid1); err != nil {
		t.Fatalf("SetConfigValue(%s, %s) error: %v", key, valid1, err)
	}
	val, err := GetConfigValue(key)
	if err != nil {
		t.Fatalf("GetConfigValue(%s) error: %v", key, err)
	}
	if val != valid1 {
		t.Errorf("GetConfigValue(%s) = %q, want %q", key, val, valid1)
	}

	// Second valid value.
	if err := SetConfigValue(key, valid2); err != nil {
		t.Fatalf("SetConfigValue(%s, %s) error: %v", key, valid2, err)
	}
	val, _ = GetConfigValue(key)
	if val != valid2 {
		t.Errorf("GetConfigValue(%s) = %q, want %q", key, val, valid2)
	}

	// Invalid value is rejected.
	if err := SetConfigValue(key, invalid); err == nil {
		t.Errorf("expected error for invalid %s value", key)
	}

	// Reset clears it.
	if err := ResetConfigValue(key); err != nil {
		t.Fatalf("ResetConfigValue(%s) error: %v", key, err)
	}
	val, _ = GetConfigValue(key)
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
	if err := SetConfigValue("auto_enable", "false"); err != nil {
		t.Fatal(err)
	}

	// Env says true
	_ = os.Setenv("CHARLY_AUTO_ENABLE", "true")
	defer os.Unsetenv("CHARLY_AUTO_ENABLE") //nolint:errcheck

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if !rt.AutoEnable {
		t.Error("AutoEnable should be true when CHARLY_AUTO_ENABLE=true overrides config")
	}
}

func TestAutoEnable_EnvValue1(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")
	_ = os.Setenv("CHARLY_AUTO_ENABLE", "1")
	defer os.Unsetenv("CHARLY_AUTO_ENABLE") //nolint:errcheck

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if !rt.AutoEnable {
		t.Error("AutoEnable should be true when CHARLY_AUTO_ENABLE=1")
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

	if err := SetConfigValue("auto_enable", "true"); err != nil {
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
	if err := SetConfigValue("bind_address", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	// Env says 0.0.0.0
	_ = os.Setenv("CHARLY_BIND_ADDRESS", "0.0.0.0")
	defer os.Unsetenv("CHARLY_BIND_ADDRESS") //nolint:errcheck

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q, want %q (env should override config)", rt.BindAddress, "0.0.0.0")
	}
}

func TestBindAddress_InvalidEnv(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()
	kit.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Setenv("CHARLY_BIND_ADDRESS", "10.0.0.1")
	defer os.Unsetenv("CHARLY_BIND_ADDRESS") //nolint:errcheck

	_, err := ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid bind_address")
	}
}

// TestDetectRunMode_NonPodmanEngine — runEngine != "podman" is always
// "direct" regardless of systemd state.
func TestDetectRunMode_NonPodmanEngine(t *testing.T) {
	if got := detectRunMode("docker"); got != "direct" {
		t.Errorf("detectRunMode(docker) = %q, want direct", got)
	}
}

// TestSystemdUserAvailable_EmptyXDG — without XDG_RUNTIME_DIR set, the
// function returns false regardless of whether the runtime dir exists.
func TestSystemdUserAvailable_EmptyXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	dir := t.TempDir()
	orig := kit.SystemdUserRuntimeDir
	defer func() { kit.SystemdUserRuntimeDir = orig }()
	kit.SystemdUserRuntimeDir = func() string { return dir }

	if SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = true with empty XDG_RUNTIME_DIR; want false")
	}
}

// TestSystemdUserAvailable_DirMissing — XDG set but the systemd dir
// doesn't exist (typical harness sandbox state) → false.
func TestSystemdUserAvailable_DirMissing(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	orig := kit.SystemdUserRuntimeDir
	defer func() { kit.SystemdUserRuntimeDir = orig }()
	missing := filepath.Join(t.TempDir(), "definitely-not-a-systemd-dir")
	kit.SystemdUserRuntimeDir = func() string { return missing }

	if SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = true with missing /run/user/<uid>/systemd; want false")
	}
}

// TestSystemdUserAvailable_DirIsFile — XDG set + path exists but is a
// regular file (not a directory) → false.
func TestSystemdUserAvailable_DirIsFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "systemd")
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := kit.SystemdUserRuntimeDir
	defer func() { kit.SystemdUserRuntimeDir = orig }()
	kit.SystemdUserRuntimeDir = func() string { return filePath }

	if SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = true with regular file at probed path; want false")
	}
}

// TestSystemdUserAvailable_AllPresent — XDG set + dir exists → true.
// This is the only case where detectRunMode should pick quadlet (when
// also paired with podman engine + systemctl binary).
func TestSystemdUserAvailable_AllPresent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	tmp := t.TempDir()
	dirPath := filepath.Join(tmp, "systemd")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kit.SystemdUserRuntimeDir
	defer func() { kit.SystemdUserRuntimeDir = orig }()
	kit.SystemdUserRuntimeDir = func() string { return dirPath }

	if !SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = false with all signals present; want true")
	}
}
