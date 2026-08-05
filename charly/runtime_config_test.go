package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/hostenv"
)

func TestLoadRuntimeConfig_Missing(t *testing.T) {
	// Point to a non-existent path
	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()

	hostenv.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "nonexistent", "config.yml"), nil
	}

	cfg, err := hostenv.LoadRuntimeConfig()
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

	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	cfg := &hostenv.RuntimeConfig{
		Engine:  hostenv.EngineConfig{Build: "podman", Run: "docker"},
		RunMode: "quadlet",
	}
	if err := hostenv.SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error: %v", err)
	}

	loaded, err := hostenv.LoadRuntimeConfig()
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
	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	// Ensure env vars are clear
	for _, key := range []string{"CHARLY_BUILD_ENGINE", "CHARLY_RUN_ENGINE", "CHARLY_RUN_MODE", "CHARLY_AUTO_ENABLE", "CHARLY_BIND_ADDRESS"} {
		_ = os.Unsetenv(key)
	}

	rt, err := hostenv.ResolveRuntime()
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

	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// Write config with podman
	cfg := &hostenv.RuntimeConfig{Engine: hostenv.EngineConfig{Build: "podman"}}
	if err := hostenv.SaveRuntimeConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Set env to override
	_ = os.Setenv("CHARLY_BUILD_ENGINE", "docker")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	rt, err := hostenv.ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BuildEngine != "docker" {
		t.Errorf("BuildEngine = %q, want %q (env should override config)", rt.BuildEngine, "docker")
	}
}

// TestResolveRuntime_MixedEngines pins the build and run engines to DIFFERENT engines and asserts
// ResolveRuntime keeps them apart.
//
// The two are independent by construction — separate env vars, separate config fields, separate
// auto-detect branches — and the documentation says so: opencharly.ai and the README both tell a
// reader that building with Podman and running under Docker is a supported combination. Until this
// test existed, nothing asserted it end to end. TestSaveAndLoadRuntimeConfig names the same pair,
// but it round-trips the config FILE; it never calls ResolveRuntime, so it proves the pair can be
// written down rather than that it survives resolution. A reviewer reading the docs claim and
// grepping for its proof would have found that test and stopped one layer short.
//
// It fails if resolution ever collapses the two onto one engine — the single way the documented
// claim could quietly stop being true.
func TestResolveRuntime_MixedEngines(t *testing.T) {
	tmpDir := t.TempDir()
	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(tmpDir, "config.yml"), nil
	}

	_ = os.Setenv("CHARLY_BUILD_ENGINE", "podman")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Setenv("CHARLY_RUN_ENGINE", "docker")
	defer os.Unsetenv("CHARLY_RUN_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	rt, err := hostenv.ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BuildEngine != "podman" {
		t.Errorf("BuildEngine = %q, want %q", rt.BuildEngine, "podman")
	}
	if rt.RunEngine != "docker" {
		t.Errorf("RunEngine = %q, want %q", rt.RunEngine, "docker")
	}

	// The mixed pair also decides the run mode: quadlet requires RunEngine==podman, so a
	// docker RUN engine must resolve to direct however the build engine is set.
	if rt.RunMode != "direct" {
		t.Errorf("RunMode = %q, want %q (quadlet requires a podman run engine)", rt.RunMode, "direct")
	}
}

func TestResolveRuntime_InvalidEngine(t *testing.T) {
	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_ = os.Setenv("CHARLY_BUILD_ENGINE", "containerd")
	defer os.Unsetenv("CHARLY_BUILD_ENGINE") //nolint:errcheck
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")

	_, err := hostenv.ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid engine")
	}
}

func TestResolveRuntime_InvalidRunMode(t *testing.T) {
	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")
	_ = os.Setenv("CHARLY_RUN_MODE", "swarm")
	defer os.Unsetenv("CHARLY_RUN_MODE") //nolint:errcheck

	_, err := hostenv.ResolveRuntime()
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
		got := hostenv.ResolveValue(tt.env, tt.cfg, tt.def)
		if got != tt.want {
			t.Errorf("ResolveValue(%q, %q, %q) = %q, want %q", tt.env, tt.cfg, tt.def, got, tt.want)
		}
	}
}

func TestAutoEnable_EnvValue1(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_BIND_ADDRESS")
	_ = os.Setenv("CHARLY_AUTO_ENABLE", "1")
	defer os.Unsetenv("CHARLY_AUTO_ENABLE") //nolint:errcheck

	rt, err := hostenv.ResolveRuntime()
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

	orig := hostenv.RuntimeConfigPath
	defer func() { hostenv.RuntimeConfigPath = orig }()
	hostenv.RuntimeConfigPath = func() (string, error) { return configPath, nil }

	_ = os.Unsetenv("CHARLY_BUILD_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_ENGINE")
	_ = os.Unsetenv("CHARLY_RUN_MODE")
	_ = os.Unsetenv("CHARLY_AUTO_ENABLE")
	_ = os.Setenv("CHARLY_BIND_ADDRESS", "10.0.0.1")
	defer os.Unsetenv("CHARLY_BIND_ADDRESS") //nolint:errcheck

	_, err := hostenv.ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid bind_address")
	}
}

// TestDetectRunMode_NonPodmanEngine — runEngine != "podman" is always
// "direct" regardless of systemd state.
func TestDetectRunMode_NonPodmanEngine(t *testing.T) {
	if got := hostenv.DetectRunMode("docker"); got != "direct" {
		t.Errorf("detectRunMode(docker) = %q, want direct", got)
	}
}

// TestSystemdUserAvailable_EmptyXDG — without XDG_RUNTIME_DIR set, the
// function returns false regardless of whether the runtime dir exists.
func TestSystemdUserAvailable_EmptyXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	dir := t.TempDir()
	orig := hostenv.SystemdUserRuntimeDir
	defer func() { hostenv.SystemdUserRuntimeDir = orig }()
	hostenv.SystemdUserRuntimeDir = func() string { return dir }

	if hostenv.SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = true with empty XDG_RUNTIME_DIR; want false")
	}
}

// TestSystemdUserAvailable_DirMissing — XDG set but the systemd dir
// doesn't exist (typical harness sandbox state) → false.
func TestSystemdUserAvailable_DirMissing(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	orig := hostenv.SystemdUserRuntimeDir
	defer func() { hostenv.SystemdUserRuntimeDir = orig }()
	missing := filepath.Join(t.TempDir(), "definitely-not-a-systemd-dir")
	hostenv.SystemdUserRuntimeDir = func() string { return missing }

	if hostenv.SystemdUserAvailable() {
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
	orig := hostenv.SystemdUserRuntimeDir
	defer func() { hostenv.SystemdUserRuntimeDir = orig }()
	hostenv.SystemdUserRuntimeDir = func() string { return filePath }

	if hostenv.SystemdUserAvailable() {
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
	orig := hostenv.SystemdUserRuntimeDir
	defer func() { hostenv.SystemdUserRuntimeDir = orig }()
	hostenv.SystemdUserRuntimeDir = func() string { return dirPath }

	if !hostenv.SystemdUserAvailable() {
		t.Error("SystemdUserAvailable() = false with all signals present; want true")
	}
}
