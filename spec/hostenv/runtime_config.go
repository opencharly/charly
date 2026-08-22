package hostenv

// runtime_config.go — the user-level runtime configuration subsystem (RELOCATED from sdk/kit,
// #55 host-env fabric extraction). Resolving which container engine / run-mode / storage paths to
// use from env > config-file > defaults is a HOST-ENVIRONMENT primitive a plugin cannot hold, so
// it homes in spec/hostenv (the existing host-probe slice), carrying gopkg.in/yaml.v3 + os/exec in
// this slice. sdk/kit re-exports the types + funcs so the ~23 kit.ResolveRuntime / kit.RuntimeConfig
// / … call sites are untouched. The two test-injection SEAM vars (RuntimeConfigPath /
// SystemdUserRuntimeDir) live HERE ONLY — a live var that callers reassign cannot be value-copied
// across the module boundary, so every reader/writer references hostenv.RuntimeConfigPath directly
// (kit does NOT re-export them).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/opencharly/spec/container"
	"gopkg.in/yaml.v3"
)

var configPermWarningOnce sync.Once

// RuntimeConfig represents the user-level runtime configuration (~/.config/charly/config.yml)
type RuntimeConfig struct {
	Engine                 EngineConfig      `yaml:"engine" json:"engine"`
	RunMode                string            `yaml:"run_mode,omitempty" json:"run_mode,omitempty"`
	AutoEnable             *bool             `yaml:"auto_enable,omitempty" json:"auto_enable,omitempty"`
	BindAddress            string            `yaml:"bind_address,omitempty" json:"bind_address,omitempty"`
	EncryptedStoragePath   string            `yaml:"encrypted_storage_path,omitempty" json:"encrypted_storage_path,omitempty"`
	VolumesPath            string            `yaml:"volumes_path,omitempty" json:"volumes_path,omitempty"`
	SecretBackend          string            `yaml:"secret_backend,omitempty" json:"secret_backend,omitempty"`       // "auto", "keyring", "config"
	ForwardGpgAgent        *bool             `yaml:"forward_gpg_agent,omitempty" json:"forward_gpg_agent,omitempty"` // Forward host GPG agent socket into containers (default: true)
	ForwardSshAgent        *bool             `yaml:"forward_ssh_agent,omitempty" json:"forward_ssh_agent,omitempty"` // Forward host SSH agent socket into containers (default: true)
	Vm                     RuntimeVmConfig   `yaml:"vm,omitempty" json:"vm,omitempty"`
	VncPasswords           map[string]string `yaml:"vnc_passwords,omitempty" json:"vnc_passwords,omitempty"`                       // VNC passwords keyed by image[-instance]
	KeyringKeys            []string          `yaml:"keyring_keys,omitempty" json:"keyring_keys,omitempty"`                         // Shadow index: names of keys stored in keyring (no values)
	KeyringCollectionLabel string            `yaml:"keyring_collection_label,omitempty" json:"keyring_collection_label,omitempty"` // Preferred Secret Service collection label; empty means use default alias then iterate.
	// HostAliases maps short names (e.g. "o") to SSH targets (e.g.
	// "user@o.example.org"). Consulted by charly's --host flag when
	// re-execing commands on remote machines. Set via
	// `charly settings set hosts.<alias> <ssh-target>`.
	HostAliases map[string]string `yaml:"host_aliases,omitempty" json:"host_aliases,omitempty"`
}

// RuntimeVmConfig holds user-level VM defaults
type RuntimeVmConfig struct {
	Backend   string `yaml:"backend,omitempty" json:"backend,omitempty"`     // "auto", "libvirt", "qemu"
	DiskSize  string `yaml:"disk_size,omitempty" json:"disk_size,omitempty"` // default disk size
	RootSize  string `yaml:"root_size,omitempty" json:"root_size,omitempty"` // root partition size
	Ram       string `yaml:"ram,omitempty" json:"ram,omitempty"`             // default RAM
	Cpus      int    `yaml:"cpus,omitempty" json:"cpus,omitempty"`           // default CPU count
	Rootfs    string `yaml:"rootfs,omitempty" json:"rootfs,omitempty"`       // root filesystem type
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"` // image transport (registry, containers-storage)
}

// EngineConfig specifies which container engine to use
type EngineConfig struct {
	Build   string `yaml:"build,omitempty" json:"build,omitempty"`
	Run     string `yaml:"run,omitempty" json:"run,omitempty"`
	Rootful string `yaml:"rootful,omitempty" json:"rootful,omitempty"` // "auto", "machine", "sudo", "native"
}

// ResolvedRuntime holds the fully resolved runtime configuration
type ResolvedRuntime struct {
	BuildEngine          string // "docker" or "podman"
	RunEngine            string // "docker" or "podman"
	Rootful              string // "auto", "machine", "sudo", "native"
	RunMode              string // "direct" or "quadlet"
	AutoEnable           bool   // auto-enable quadlet on first start
	BindAddress          string // "127.0.0.1" or "0.0.0.0"
	EncryptedStoragePath string // path for gocryptfs encrypted storage
	VolumesPath          string // base path for bind mount volume data
	ForwardGpgAgent      bool   // forward host GPG agent socket into containers
	ForwardSshAgent      bool   // forward host SSH agent socket into containers
	VmBackend            string // "auto", "libvirt", or "qemu"
}

// RuntimeConfigPath returns the path to the user's runtime config file. A test-injection SEAM var
// (tests redirect it to a t.TempDir()); it lives here ONLY and every reader/writer references it
// directly — kit does NOT re-export it (a value-copy alias would break write-through).
var RuntimeConfigPath = defaultRuntimeConfigPath

func defaultRuntimeConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "config.yml"), nil
}

// LoadRuntimeConfig reads the runtime config file. Returns zero-value config if missing.
func LoadRuntimeConfig() (*RuntimeConfig, error) {
	path, err := RuntimeConfigPath()
	if err != nil {
		return &RuntimeConfig{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuntimeConfig{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// Warn once per session if config file has overly permissive permissions
	if info, statErr := os.Stat(path); statErr == nil {
		perm := info.Mode().Perm()
		if perm&0077 != 0 {
			configPermWarningOnce.Do(func() {
				fmt.Fprintf(os.Stderr, "WARNING: %s has permissions %04o (accessible by other users).\n", path, perm)
				fmt.Fprintf(os.Stderr, "This file may contain plaintext credentials.\n")
				fmt.Fprintf(os.Stderr, "Run: chmod 600 %s\n", path)
			})
		}
	}

	var cfg RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveRuntimeConfig writes the runtime config file, creating directories as needed.
func SaveRuntimeConfig(cfg *RuntimeConfig) error {
	path, err := RuntimeConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// ResolveRuntime resolves the runtime configuration: env vars > config file > defaults.
func ResolveRuntime() (*ResolvedRuntime, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}

	rt := &ResolvedRuntime{
		BuildEngine:          ResolveValue(os.Getenv("CHARLY_BUILD_ENGINE"), cfg.Engine.Build, "auto"),
		RunEngine:            ResolveValue(os.Getenv("CHARLY_RUN_ENGINE"), cfg.Engine.Run, "auto"),
		Rootful:              ResolveValue(os.Getenv("CHARLY_ENGINE_ROOTFUL"), cfg.Engine.Rootful, "auto"),
		RunMode:              ResolveValue(os.Getenv("CHARLY_RUN_MODE"), cfg.RunMode, "auto"),
		AutoEnable:           ResolveAutoEnable(os.Getenv("CHARLY_AUTO_ENABLE"), cfg.AutoEnable),
		BindAddress:          ResolveValue(os.Getenv("CHARLY_BIND_ADDRESS"), cfg.BindAddress, "127.0.0.1"),
		EncryptedStoragePath: ResolveEncryptedStoragePath(os.Getenv("CHARLY_ENCRYPTED_STORAGE_PATH"), cfg.EncryptedStoragePath),
		VolumesPath:          ResolveVolumesPath(os.Getenv("CHARLY_VOLUMES_PATH"), cfg.VolumesPath),
		ForwardGpgAgent:      ResolveAutoEnable(os.Getenv("CHARLY_FORWARD_GPG_AGENT"), cfg.ForwardGpgAgent),
		ForwardSshAgent:      ResolveAutoEnable(os.Getenv("CHARLY_FORWARD_SSH_AGENT"), cfg.ForwardSshAgent),
		VmBackend:            ResolveValue(os.Getenv("CHARLY_VM_BACKEND"), cfg.Vm.Backend, "auto"),
	}

	// Auto-detect engines
	var detectErr error
	if rt.BuildEngine == "auto" {
		rt.BuildEngine, detectErr = container.DetectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.build: %w", detectErr)
		}
	}
	if rt.RunEngine == "auto" {
		rt.RunEngine, detectErr = container.DetectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.run: %w", detectErr)
		}
	}

	// Auto-detect run mode: default to quadlet when podman + systemd are present
	if rt.RunMode == "auto" {
		rt.RunMode = DetectRunMode(rt.RunEngine)
	}

	if err := ValidateEngine(rt.BuildEngine, "engine.build"); err != nil {
		return nil, err
	}
	if err := ValidateEngine(rt.RunEngine, "engine.run"); err != nil {
		return nil, err
	}
	if err := ValidateRunMode(rt.RunMode); err != nil {
		return nil, err
	}
	if err := ValidateBindAddress(rt.BindAddress); err != nil {
		return nil, err
	}

	if rt.RunMode == "quadlet" && rt.RunEngine != "podman" {
		fmt.Fprintf(os.Stderr, "Warning: run_mode=quadlet requires podman; engine.run=%s\n", rt.RunEngine)
	}

	return rt, nil
}

// ResolveValue returns the first non-empty value from the chain.
func ResolveValue(envVal, cfgVal, defaultVal string) string {
	if envVal != "" {
		return envVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	return defaultVal
}

func ValidateEngine(value, field string) error {
	if value != "docker" && value != "podman" {
		return fmt.Errorf("%s must be \"docker\" or \"podman\", got %q", field, value)
	}
	return nil
}

func ValidateRunMode(value string) error {
	if value != "auto" && value != "direct" && value != "quadlet" {
		return fmt.Errorf("run_mode must be \"auto\", \"direct\", or \"quadlet\", got %q", value)
	}
	return nil
}

// DetectRunMode returns "quadlet" when podman is present AND a functional
// systemd-user session is reachable (systemctl binary + XDG_RUNTIME_DIR +
// /run/user/<uid>/systemd directory). Otherwise returns "direct".
//
// The functional-systemd-user check (added 2026-04-27) catches nested
// environments — harness sandbox pods, supervisord-only containers, sysvinit hosts —
// that have the systemctl binary present but no running `systemd --user`
// session. Without this check, `charly fleet add <name> <ref>` would silently
// pick run_mode=quadlet, write the .container file, and fail at
// `systemctl --user daemon-reload` time. With the check, run_mode=direct
// is auto-selected on those hosts and `runConfigDirect()` (in
// config_image.go) emits a `podman run -d` invocation instead.
func DetectRunMode(runEngine string) string {
	if runEngine != "podman" {
		return "direct"
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "direct"
	}
	if !SystemdUserAvailable() {
		return "direct"
	}
	return "quadlet"
}

// SystemdUserRuntimeDir returns the path the directory check probes —
// `/run/user/<uid>/systemd`. Exposed as a package-level SEAM var so tests
// can redirect to a t.TempDir(). Lives here ONLY (kit does not re-export it).
var SystemdUserRuntimeDir = func() string {
	return filepath.Join("/run/user", strconv.Itoa(os.Geteuid()), "systemd")
}

// SystemdUserAvailable reports whether a functional `systemd --user`
// session is reachable for the current process. Both signals must hold:
//
//   - $XDG_RUNTIME_DIR is set (the bus address resolves against it)
//   - /run/user/<uid>/systemd exists as a directory (systemd-user has
//     populated its runtime dir, i.e. the user-instance has actually
//     started)
//
// Either alone is insufficient: $XDG_RUNTIME_DIR can be set in stale
// environments where systemd-user never came up, and the runtime dir
// can exist on systems where the env var got dropped (sudo without -E,
// container entrypoints).
func SystemdUserAvailable() bool {
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return false
	}
	info, err := os.Stat(SystemdUserRuntimeDir())
	return err == nil && info.IsDir()
}

func ValidateBindAddress(value string) error {
	if value != "127.0.0.1" && value != "0.0.0.0" {
		return fmt.Errorf("bind_address must be \"127.0.0.1\" or \"0.0.0.0\", got %q", value)
	}
	return nil
}

func ResolveEncryptedStoragePath(envVal, cfgVal string) string {
	if envVal != "" {
		return ExpandHostHome(envVal)
	}
	if cfgVal != "" {
		return ExpandHostHome(cfgVal)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "charly", "encrypted")
	}
	return filepath.Join(home, ".local", "share", "charly", "encrypted")
}

func ResolveVolumesPath(envVal, cfgVal string) string {
	if envVal != "" {
		return ExpandHostHome(envVal)
	}
	if cfgVal != "" {
		return ExpandHostHome(cfgVal)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "charly", "volumes")
	}
	return filepath.Join(home, ".local", "share", "charly", "volumes")
}

// ExpandHostHome expands ~ and $HOME in a path using the actual user's home directory.
func ExpandHostHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	if path == "~" {
		return home
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	return path
}

func ResolveAutoEnable(envVal string, cfgVal *bool) bool {
	if envVal != "" {
		return envVal == "true" || envVal == "1"
	}
	if cfgVal != nil {
		return *cfgVal
	}
	return true
}
