package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
)

func TestLoadRuntimeConfig_Missing(t *testing.T) {
	// Point to a non-existent path
	orig := kit.RuntimeConfigPath
	defer func() { kit.RuntimeConfigPath = orig }()

	kit.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "nonexistent", "config.yml"), nil
	}

	cfg, err := kit.LoadRuntimeConfig()
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

	cfg := &kit.RuntimeConfig{
		Engine:  kit.EngineConfig{Build: "podman", Run: "docker"},
		RunMode: "quadlet",
	}
	if err := kit.SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error: %v", err)
	}

	loaded, err := kit.LoadRuntimeConfig()
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

	rt, err := kit.ResolveRuntime()
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
	cfg := &kit.RuntimeConfig{Engine: kit.EngineConfig{Build: "podman"}}
	if err := kit.SaveRuntimeConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Set env to override
	_ = os.Setenv("CHARLY_BUILD_ENGINE", "docker")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	rt, err := kit.ResolveRuntime()
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

	_, err := kit.ResolveRuntime()
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

	_, err := kit.ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid run_mode")
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
		got := kit.ResolveValue(tt.env, tt.cfg, tt.def)
		if got != tt.want {
			t.Errorf("ResolveValue(%q, %q, %q) = %q, want %q", tt.env, tt.cfg, tt.def, got, tt.want)
		}
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

	rt, err := kit.ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if !rt.AutoEnable {
		t.Error("AutoEnable should be true when CHARLY_AUTO_ENABLE=1")
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

	_, err := kit.ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid bind_address")
	}
}

// TestDetectRunMode_NonPodmanEngine — runEngine != "podman" is always
// "direct" regardless of systemd state.
func TestDetectRunMode_NonPodmanEngine(t *testing.T) {
	if got := kit.DetectRunMode("docker"); got != "direct" {
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

	if kit.SystemdUserAvailable() {
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

	if kit.SystemdUserAvailable() {
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

	if kit.SystemdUserAvailable() {
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

	if !kit.SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = false with all signals present; want true")
	}
}
