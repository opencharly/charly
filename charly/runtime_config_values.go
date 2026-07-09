package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

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
			if err := ValidateEngine(value, key); err != nil {
				return fmt.Errorf("%s must be \"auto\", \"docker\", or \"podman\", got %q", key, value)
			}
		}
	case "engine.rootful":
		if value != "auto" && value != "machine" && value != "sudo" && value != "native" {
			return fmt.Errorf("engine.rootful must be \"auto\", \"machine\", \"sudo\", or \"native\", got %q", value)
		}
	case "run_mode":
		if err := ValidateRunMode(value); err != nil {
			return err
		}
	case "auto_enable":
		if value != "true" && value != "false" {
			return fmt.Errorf("auto_enable must be \"true\" or \"false\", got %q", value)
		}
	case "bind_address":
		if err := ValidateBindAddress(value); err != nil {
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
