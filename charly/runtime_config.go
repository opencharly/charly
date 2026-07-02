package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

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

// RuntimeConfigPath returns the path to the user's runtime config file.
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
		BuildEngine:          resolveValue(os.Getenv("CHARLY_BUILD_ENGINE"), cfg.Engine.Build, "auto"),
		RunEngine:            resolveValue(os.Getenv("CHARLY_RUN_ENGINE"), cfg.Engine.Run, "auto"),
		Rootful:              resolveValue(os.Getenv("CHARLY_ENGINE_ROOTFUL"), cfg.Engine.Rootful, "auto"),
		RunMode:              resolveValue(os.Getenv("CHARLY_RUN_MODE"), cfg.RunMode, "auto"),
		AutoEnable:           resolveAutoEnable(os.Getenv("CHARLY_AUTO_ENABLE"), cfg.AutoEnable),
		BindAddress:          resolveValue(os.Getenv("CHARLY_BIND_ADDRESS"), cfg.BindAddress, "127.0.0.1"),
		EncryptedStoragePath: resolveEncryptedStoragePath(os.Getenv("CHARLY_ENCRYPTED_STORAGE_PATH"), cfg.EncryptedStoragePath),
		VolumesPath:          resolveVolumesPath(os.Getenv("CHARLY_VOLUMES_PATH"), cfg.VolumesPath),
		ForwardGpgAgent:      resolveAutoEnable(os.Getenv("CHARLY_FORWARD_GPG_AGENT"), cfg.ForwardGpgAgent),
		ForwardSshAgent:      resolveAutoEnable(os.Getenv("CHARLY_FORWARD_SSH_AGENT"), cfg.ForwardSshAgent),
		VmBackend:            resolveValue(os.Getenv("CHARLY_VM_BACKEND"), cfg.Vm.Backend, "auto"),
	}

	// Auto-detect engines
	var detectErr error
	if rt.BuildEngine == "auto" {
		rt.BuildEngine, detectErr = detectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.build: %w", detectErr)
		}
	}
	if rt.RunEngine == "auto" {
		rt.RunEngine, detectErr = detectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.run: %w", detectErr)
		}
	}

	// Auto-detect run mode: default to quadlet when podman + systemd are present
	if rt.RunMode == "auto" {
		rt.RunMode = detectRunMode(rt.RunEngine)
	}

	if err := validateEngine(rt.BuildEngine, "engine.build"); err != nil {
		return nil, err
	}
	if err := validateEngine(rt.RunEngine, "engine.run"); err != nil {
		return nil, err
	}
	if err := validateRunMode(rt.RunMode); err != nil {
		return nil, err
	}
	if err := validateBindAddress(rt.BindAddress); err != nil {
		return nil, err
	}

	if rt.RunMode == "quadlet" && rt.RunEngine != "podman" {
		fmt.Fprintf(os.Stderr, "Warning: run_mode=quadlet requires podman; engine.run=%s\n", rt.RunEngine)
	}

	return rt, nil
}

// resolveValue returns the first non-empty value from the chain.
func resolveValue(envVal, cfgVal, defaultVal string) string {
	if envVal != "" {
		return envVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	return defaultVal
}

// detectEngine auto-detects the container engine: prefers podman, falls back to docker.
func detectEngine() (string, error) {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman", nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker", nil
	}
	return "", fmt.Errorf("no container engine found (install podman or docker)")
}

func validateEngine(value, field string) error {
	if value != "docker" && value != "podman" {
		return fmt.Errorf("%s must be \"docker\" or \"podman\", got %q", field, value)
	}
	return nil
}

func validateRunMode(value string) error {
	if value != "auto" && value != "direct" && value != "quadlet" {
		return fmt.Errorf("run_mode must be \"auto\", \"direct\", or \"quadlet\", got %q", value)
	}
	return nil
}

// detectRunMode returns "quadlet" when podman is present AND a functional
// systemd-user session is reachable (systemctl binary + XDG_RUNTIME_DIR +
// /run/user/<uid>/systemd directory). Otherwise returns "direct".
//
// The functional-systemd-user check (added 2026-04-27) catches nested
// environments — harness sandbox pods, supervisord-only containers, sysvinit hosts —
// that have the systemctl binary present but no running `systemd --user`
// session. Without this check, `charly bundle add <name> <ref>` would silently
// pick run_mode=quadlet, write the .container file, and fail at
// `systemctl --user daemon-reload` time. With the check, run_mode=direct
// is auto-selected on those hosts and `runConfigDirect()` (in
// config_image.go) emits a `podman run -d` invocation instead.
func detectRunMode(runEngine string) string {
	if runEngine != "podman" {
		return "direct"
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "direct"
	}
	if !systemdUserAvailable() {
		return "direct"
	}
	return "quadlet"
}

// systemdUserRuntimeDir returns the path the directory check probes —
// `/run/user/<uid>/systemd`. Exposed as a package-level var so tests
// can redirect to a t.TempDir().
var systemdUserRuntimeDir = func() string {
	return filepath.Join("/run/user", strconv.Itoa(os.Geteuid()), "systemd")
}

// systemdUserAvailable reports whether a functional `systemd --user`
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
func systemdUserAvailable() bool {
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return false
	}
	info, err := os.Stat(systemdUserRuntimeDir())
	return err == nil && info.IsDir()
}

func validateBindAddress(value string) error {
	if value != "127.0.0.1" && value != "0.0.0.0" {
		return fmt.Errorf("bind_address must be \"127.0.0.1\" or \"0.0.0.0\", got %q", value)
	}
	return nil
}

func resolveEncryptedStoragePath(envVal, cfgVal string) string {
	if envVal != "" {
		return expandHostHome(envVal)
	}
	if cfgVal != "" {
		return expandHostHome(cfgVal)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "charly", "encrypted")
	}
	return filepath.Join(home, ".local", "share", "charly", "encrypted")
}

func resolveVolumesPath(envVal, cfgVal string) string {
	if envVal != "" {
		return expandHostHome(envVal)
	}
	if cfgVal != "" {
		return expandHostHome(cfgVal)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "charly", "volumes")
	}
	return filepath.Join(home, ".local", "share", "charly", "volumes")
}

// expandHostHome expands ~ and $HOME in a path using the actual user's home directory.
func expandHostHome(path string) string {
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

func resolveAutoEnable(envVal string, cfgVal *bool) bool {
	if envVal != "" {
		return envVal == "true" || envVal == "1"
	}
	if cfgVal != nil {
		return *cfgVal
	}
	return true
}

// GetConfigValue returns the value for a dot-notation key from the config file.
//
//nolint:gocyclo // flat dispatch over config keys + dynamic hosts./vnc.password subkeys; uniform getter
func GetConfigValue(key string) (string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "", err
	}

	switch key {
	case "engine.build":
		return cfg.Engine.Build, nil
	case "engine.run":
		return cfg.Engine.Run, nil
	case "engine.rootful":
		return cfg.Engine.Rootful, nil
	case "run_mode":
		return cfg.RunMode, nil
	case "auto_enable":
		if cfg.AutoEnable != nil {
			if *cfg.AutoEnable {
				return "true", nil
			}
			return "false", nil
		}
		return "", nil
	case "bind_address":
		return cfg.BindAddress, nil
	case "encrypted_storage_path":
		return cfg.EncryptedStoragePath, nil
	case "volumes_path":
		return cfg.VolumesPath, nil
	case "secret_backend":
		return cfg.SecretBackend, nil
	case "keyring_collection_label":
		return cfg.KeyringCollectionLabel, nil
	case "forward_gpg_agent":
		if cfg.ForwardGpgAgent != nil {
			if *cfg.ForwardGpgAgent {
				return "true", nil
			}
			return "false", nil
		}
		return "", nil
	case "forward_ssh_agent":
		if cfg.ForwardSshAgent != nil {
			if *cfg.ForwardSshAgent {
				return "true", nil
			}
			return "false", nil
		}
		return "", nil
	case "vm.backend":
		return cfg.Vm.Backend, nil
	case "vm.disk_size":
		return cfg.Vm.DiskSize, nil
	case "vm.ram":
		return cfg.Vm.Ram, nil
	case "vm.cpus":
		if cfg.Vm.Cpus > 0 {
			return fmt.Sprintf("%d", cfg.Vm.Cpus), nil
		}
		return "", nil
	case "vm.rootfs":
		return cfg.Vm.Rootfs, nil
	case "vm.root_size":
		return cfg.Vm.RootSize, nil
	case "vm.transport":
		return cfg.Vm.Transport, nil
	default:
		if after, ok := strings.CutPrefix(key, "hosts."); ok {
			alias := after
			if alias == "" {
				return "", fmt.Errorf("hosts. key requires an alias name")
			}
			if cfg.HostAliases == nil {
				return "", nil
			}
			return cfg.HostAliases[alias], nil
		}
		if after, ok := strings.CutPrefix(key, "vnc.password."); ok {
			name := after
			val, source := ResolveCredential("", CredServiceVNC, name, "")
			if source == "locked" {
				return "<LOCKED>", nil
			}
			// The keyring backend can be unavailable (e.g. locked) while the value lives
			// in the keyring; the config.yml shadow index (KeyringKeys, core-owned) still
			// lists it. Report <LOCKED> rather than empty so the operator knows it exists.
			if val == "" && source == "unavailable" && slices.Contains(cfg.KeyringKeys, CredServiceVNC+"/"+name) {
				return "<LOCKED>", nil
			}
			return val, nil
		}
		return "", fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, volumes_path, secret_backend, keyring_collection_label, forward_gpg_agent, forward_ssh_agent, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
	}
}

// SetConfigValue sets a value for a dot-notation key in the config file.
//
//nolint:gocyclo // paired validate+persist switch over ~25 config keys; per-key logic is cohesive
func SetConfigValue(key, value string) error {
	// Validate value before writing
	switch key {
	case "engine.build", "engine.run":
		if value != "auto" {
			if err := validateEngine(value, key); err != nil {
				return fmt.Errorf("%s must be \"auto\", \"docker\", or \"podman\", got %q", key, value)
			}
		}
	case "engine.rootful":
		if value != "auto" && value != "machine" && value != "sudo" && value != "native" {
			return fmt.Errorf("engine.rootful must be \"auto\", \"machine\", \"sudo\", or \"native\", got %q", value)
		}
	case "run_mode":
		if err := validateRunMode(value); err != nil {
			return err
		}
	case "auto_enable":
		if value != "true" && value != "false" {
			return fmt.Errorf("auto_enable must be \"true\" or \"false\", got %q", value)
		}
	case "bind_address":
		if err := validateBindAddress(value); err != nil {
			return err
		}
	case "encrypted_storage_path":
		// Any non-empty path is valid
	case "volumes_path":
		// Any non-empty path is valid
	case "secret_backend":
		if value == "kdbx" {
			return fmt.Errorf("secret_backend \"kdbx\" was removed; the direct KeePass .kdbx backend is gone. Use \"auto\", \"keyring\", or \"config\" (KeePassXC still works via Secret Service / the keyring backend)")
		}
		if value != "auto" && value != "keyring" && value != "config" {
			return fmt.Errorf("secret_backend must be \"auto\", \"keyring\", or \"config\", got %q", value)
		}
	case "forward_gpg_agent":
		if value != "true" && value != "false" {
			return fmt.Errorf("forward_gpg_agent must be \"true\" or \"false\", got %q", value)
		}
	case "forward_ssh_agent":
		if value != "true" && value != "false" {
			return fmt.Errorf("forward_ssh_agent must be \"true\" or \"false\", got %q", value)
		}
	case "vm.backend":
		if value != "auto" && value != "libvirt" && value != "qemu" {
			return fmt.Errorf("vm.backend must be \"auto\", \"libvirt\", or \"qemu\", got %q", value)
		}
	case "vm.disk_size":
		// Any non-empty size string is valid (e.g. "10 GiB", "20G")
	case "vm.ram":
		// Any non-empty size string is valid (e.g. "4G", "8192M")
	case "vm.cpus":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("vm.cpus must be an integer, got %q", value)
		}
	case "vm.rootfs":
		if value != "ext4" && value != "xfs" && value != "btrfs" {
			return fmt.Errorf("vm.rootfs must be \"ext4\", \"xfs\", or \"btrfs\", got %q", value)
		}
	case "vm.root_size":
		// Any non-empty size string is valid (e.g. "10G", "5120M")
	case "vm.transport":
		valid := map[string]bool{"registry": true, "containers-storage": true, "oci": true, "oci-archive": true}
		if !valid[value] {
			return fmt.Errorf("vm.transport must be \"registry\", \"containers-storage\", \"oci\", or \"oci-archive\", got %q", value)
		}
	default:
		if strings.HasPrefix(key, "hosts.") {
			// hosts.<alias> — free-form SSH target; no validation
			// (matches openssh's behavior).
			break
		}
		if strings.HasPrefix(key, "vnc.password.") {
			// VNC passwords are free-form strings, no validation needed.
			break
		}
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, secret_backend, forward_gpg_agent, forward_ssh_agent, hosts.<alias>, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}

	switch key {
	case "engine.build":
		cfg.Engine.Build = value
	case "engine.run":
		cfg.Engine.Run = value
	case "engine.rootful":
		cfg.Engine.Rootful = value
	case "run_mode":
		cfg.RunMode = value
	case "auto_enable":
		b := value == "true"
		cfg.AutoEnable = &b
	case "bind_address":
		cfg.BindAddress = value
	case "encrypted_storage_path":
		cfg.EncryptedStoragePath = value
	case "volumes_path":
		cfg.VolumesPath = value
	case "secret_backend":
		cfg.SecretBackend = value
		// Reset cached default store so the new backend takes effect
		resetDefaultStore()
	case "keyring_collection_label":
		cfg.KeyringCollectionLabel = value
	case "forward_gpg_agent":
		b := value == "true"
		cfg.ForwardGpgAgent = &b
	case "forward_ssh_agent":
		b := value == "true"
		cfg.ForwardSshAgent = &b
	case "vm.backend":
		cfg.Vm.Backend = value
	case "vm.disk_size":
		cfg.Vm.DiskSize = value
	case "vm.root_size":
		cfg.Vm.RootSize = value
	case "vm.ram":
		cfg.Vm.Ram = value
	case "vm.cpus":
		cpus, _ := strconv.Atoi(value)
		cfg.Vm.Cpus = cpus
	case "vm.rootfs":
		cfg.Vm.Rootfs = value
	case "vm.transport":
		cfg.Vm.Transport = value
	default:
		if after, ok := strings.CutPrefix(key, "hosts."); ok {
			alias := after
			if alias == "" {
				return fmt.Errorf("hosts. key requires an alias name")
			}
			if cfg.HostAliases == nil {
				cfg.HostAliases = map[string]string{}
			}
			cfg.HostAliases[alias] = value
			break
		}
		// Credential keys go through the credential store
		if after, ok := strings.CutPrefix(key, "vnc.password."); ok {
			name := after
			return DefaultCredentialStore().Set(CredServiceVNC, name, value)
		}
	}

	return SaveRuntimeConfig(cfg)
}

// ResetConfigValue removes a key from the config file (reverts to default).
// If key is empty, resets the entire config.
func ResetConfigValue(key string) error {
	if key == "" {
		// Reset entire config
		return SaveRuntimeConfig(&RuntimeConfig{})
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}

	switch key {
	case "engine.build":
		cfg.Engine.Build = ""
	case "engine.run":
		cfg.Engine.Run = ""
	case "engine.rootful":
		cfg.Engine.Rootful = ""
	case "run_mode":
		cfg.RunMode = ""
	case "auto_enable":
		cfg.AutoEnable = nil
	case "bind_address":
		cfg.BindAddress = ""
	case "encrypted_storage_path":
		cfg.EncryptedStoragePath = ""
	case "volumes_path":
		cfg.VolumesPath = ""
	case "secret_backend":
		cfg.SecretBackend = ""
		resetDefaultStore()
	case "keyring_collection_label":
		cfg.KeyringCollectionLabel = ""
	case "forward_gpg_agent":
		cfg.ForwardGpgAgent = nil
	case "forward_ssh_agent":
		cfg.ForwardSshAgent = nil
	case "vm.backend":
		cfg.Vm.Backend = ""
	case "vm.disk_size":
		cfg.Vm.DiskSize = ""
	case "vm.ram":
		cfg.Vm.Ram = ""
	case "vm.cpus":
		cfg.Vm.Cpus = 0
	case "vm.rootfs":
		cfg.Vm.Rootfs = ""
	case "vm.root_size":
		cfg.Vm.RootSize = ""
	case "vm.transport":
		cfg.Vm.Transport = ""
	default:
		if after, ok := strings.CutPrefix(key, "hosts."); ok {
			alias := after
			if cfg.HostAliases != nil {
				delete(cfg.HostAliases, alias)
			}
			break
		}
		// Credential keys: delete from credential store
		if after, ok := strings.CutPrefix(key, "vnc.password."); ok {
			name := after
			return DefaultCredentialStore().Delete(CredServiceVNC, name)
		}
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, secret_backend, forward_gpg_agent, forward_ssh_agent, hosts.<alias>, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
	}

	return SaveRuntimeConfig(cfg)
}

// configKeySource describes where a config value comes from.
type configKeySource struct {
	Key    string
	Value  string
	Source string // "env", "config", "default"
}

// ListConfigValues returns all config keys with their resolved values and sources.
func ListConfigValues() ([]configKeySource, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}

	resolve := func(key, envName, cfgVal, defaultVal string) configKeySource {
		envVal := os.Getenv(envName)
		if envVal != "" {
			source := "env (" + envName + ")"
			if DotenvLoaded(envName) {
				source = "env (.env)"
			}
			return configKeySource{Key: key, Value: envVal, Source: source}
		}
		if cfgVal != "" {
			return configKeySource{Key: key, Value: cfgVal, Source: "config"}
		}
		return configKeySource{Key: key, Value: defaultVal, Source: "default"}
	}

	// Resolve auto_enable separately since it's a bool pointer
	autoEnableEntry := func() configKeySource {
		envVal := os.Getenv("CHARLY_AUTO_ENABLE")
		if envVal != "" {
			resolved := "false"
			if envVal == "true" || envVal == "1" {
				resolved = "true"
			}
			source := "env (CHARLY_AUTO_ENABLE)"
			if DotenvLoaded("CHARLY_AUTO_ENABLE") {
				source = "env (.env)"
			}
			return configKeySource{Key: "auto_enable", Value: resolved, Source: source}
		}
		if cfg.AutoEnable != nil {
			val := "false"
			if *cfg.AutoEnable {
				val = "true"
			}
			return configKeySource{Key: "auto_enable", Value: val, Source: "config"}
		}
		return configKeySource{Key: "auto_enable", Value: "true", Source: "default"}
	}

	// Generic bool pointer entry (reusable for any *bool config key with default "true")
	boolEntry := func(key, envName string, cfgVal *bool, defaultVal string) configKeySource {
		envVal := os.Getenv(envName)
		if envVal != "" {
			val := "false"
			if envVal == "true" || envVal == "1" {
				val = "true"
			}
			source := "env (" + envName + ")"
			if DotenvLoaded(envName) {
				source = "env (.env)"
			}
			return configKeySource{Key: key, Value: val, Source: source}
		}
		if cfgVal != nil {
			val := "false"
			if *cfgVal {
				val = "true"
			}
			return configKeySource{Key: key, Value: val, Source: "config"}
		}
		return configKeySource{Key: key, Value: defaultVal, Source: "default"}
	}

	// Resolve path defaults
	defaultStoragePath := resolveEncryptedStoragePath("", "")
	defaultVolumesPath := resolveVolumesPath("", "")

	// Resolve vm.cpus separately since it's an int
	vmCpusEntry := func() configKeySource {
		envVal := os.Getenv("CHARLY_VM_CPUS")
		if envVal != "" {
			source := "env (CHARLY_VM_CPUS)"
			if DotenvLoaded("CHARLY_VM_CPUS") {
				source = "env (.env)"
			}
			return configKeySource{Key: "vm.cpus", Value: envVal, Source: source}
		}
		if cfg.Vm.Cpus > 0 {
			return configKeySource{Key: "vm.cpus", Value: fmt.Sprintf("%d", cfg.Vm.Cpus), Source: "config"}
		}
		return configKeySource{Key: "vm.cpus", Value: "2", Source: "default"}
	}

	out := []configKeySource{
		resolve("engine.build", "CHARLY_BUILD_ENGINE", cfg.Engine.Build, "auto"),
		resolve("engine.run", "CHARLY_RUN_ENGINE", cfg.Engine.Run, "auto"),
		resolve("engine.rootful", "CHARLY_ENGINE_ROOTFUL", cfg.Engine.Rootful, "auto"),
		resolve("run_mode", "CHARLY_RUN_MODE", cfg.RunMode, "auto"),
		autoEnableEntry(),
		resolve("bind_address", "CHARLY_BIND_ADDRESS", cfg.BindAddress, "127.0.0.1"),
		resolve("encrypted_storage_path", "CHARLY_ENCRYPTED_STORAGE_PATH", cfg.EncryptedStoragePath, defaultStoragePath),
		resolve("volumes_path", "CHARLY_VOLUMES_PATH", cfg.VolumesPath, defaultVolumesPath),
		resolve("secret_backend", "CHARLY_SECRET_BACKEND", cfg.SecretBackend, "auto"),
		resolve("keyring_collection_label", "CHARLY_KEYRING_COLLECTION_LABEL", cfg.KeyringCollectionLabel, ""),
		boolEntry("forward_gpg_agent", "CHARLY_FORWARD_GPG_AGENT", cfg.ForwardGpgAgent, "true"),
		boolEntry("forward_ssh_agent", "CHARLY_FORWARD_SSH_AGENT", cfg.ForwardSshAgent, "true"),
		resolve("vm.backend", "CHARLY_VM_BACKEND", cfg.Vm.Backend, "auto"),
		resolve("vm.disk_size", "CHARLY_VM_DISK_SIZE", cfg.Vm.DiskSize, "10 GiB"),
		resolve("vm.root_size", "CHARLY_VM_ROOT_SIZE", cfg.Vm.RootSize, ""),
		resolve("vm.ram", "CHARLY_VM_RAM", cfg.Vm.Ram, "4G"),
		vmCpusEntry(),
		resolve("vm.rootfs", "CHARLY_VM_ROOTFS", cfg.Vm.Rootfs, "ext4"),
		resolve("vm.transport", "CHARLY_VM_TRANSPORT", cfg.Vm.Transport, ""),
	}
	// Append host aliases (dynamic keys — one per map entry).
	for name, target := range cfg.HostAliases {
		out = append(out, configKeySource{
			Key:    "hosts." + name,
			Value:  target,
			Source: "config",
		})
	}
	return out, nil
}
