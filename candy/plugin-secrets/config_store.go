package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// config_store.go is the plugin's view of ~/.config/charly/config.yml — the SUBSET of the
// core's RuntimeConfig the credential store owns (vnc_passwords + the keyring shadow index +
// keyring_collection_label + secret_backend). An out-of-process plugin module cannot import the
// core's RuntimeConfig, so it reads/writes the file directly; the file is the cross-process
// contract. Load preserves EVERY other top-level key in a raw map so Save round-trips the whole
// document — the plugin only ever mutates the four credential-storage keys, never a host setting.
//
// The type + the LoadRuntimeConfig/SaveRuntimeConfig names mirror the core's API verbatim so the
// moved backend files (credential_config.go, credential_keyring.go, command_secrets.go,
// secrets_gpg.go) compile unchanged against them.

// CredServiceVNC is the bare-key credential service (mirrors the core const). Entries under it
// use a bare key in vnc_passwords; every other service uses a composite "service/key" map key.
const CredServiceVNC = "charly/vnc"

// RuntimeConfig is the credential-relevant subset of the core RuntimeConfig. `raw` holds every
// other top-level key so Save never drops an unrelated host setting.
type RuntimeConfig struct {
	SecretBackend          string            `yaml:"secret_backend,omitempty"`
	VncPasswords           map[string]string `yaml:"vnc_passwords,omitempty"`
	KeyringKeys            []string          `yaml:"keyring_keys,omitempty"`
	KeyringCollectionLabel string            `yaml:"keyring_collection_label,omitempty"`

	raw map[string]any
}

// RuntimeConfigPath returns the path to the user's runtime config file — resolved the SAME way
// the core's defaultRuntimeConfigPath does (os.UserConfigDir honours $XDG_CONFIG_HOME), so the
// host and this out-of-process plugin agree on the file (LocalTransport passes the host env).
// A package var so plugin tests can point it at a temp file.
var RuntimeConfigPath = defaultRuntimeConfigPath

func defaultRuntimeConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "config.yml"), nil
}

var configPermWarningOnce sync.Once

// LoadRuntimeConfig reads config.yml. A missing file yields a zero-value config (no error),
// matching the core. Every top-level key is retained in `raw` for a lossless Save.
func LoadRuntimeConfig() (*RuntimeConfig, error) {
	path, err := RuntimeConfigPath()
	if err != nil {
		return &RuntimeConfig{raw: map[string]any{}}, nil //nolint:nilerr // no config dir → empty store, mirrors core
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuntimeConfig{raw: map[string]any{}}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		configPermWarningOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "WARNING: %s has permissions %04o (accessible by other users).\n", path, info.Mode().Perm())
			fmt.Fprintf(os.Stderr, "This file may contain plaintext credentials. Run: chmod 600 %s\n", path)
		})
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	cfg := &RuntimeConfig{raw: raw}
	cfg.SecretBackend = rawString(raw, "secret_backend")
	cfg.KeyringCollectionLabel = rawString(raw, "keyring_collection_label")
	cfg.VncPasswords = rawStringMap(raw, "vnc_passwords")
	cfg.KeyringKeys = rawStringSlice(raw, "keyring_keys")
	return cfg, nil
}

// SaveRuntimeConfig writes config.yml at 0600 (it may carry plaintext credentials), merging the
// four credential keys back into the preserved raw document so no host setting is lost.
func SaveRuntimeConfig(cfg *RuntimeConfig) error {
	path, err := RuntimeConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	raw := cfg.raw
	if raw == nil {
		raw = map[string]any{}
	}
	setRawString(raw, "secret_backend", cfg.SecretBackend)
	setRawString(raw, "keyring_collection_label", cfg.KeyringCollectionLabel)
	setRawStringMap(raw, "vnc_passwords", cfg.VncPasswords)
	setRawStringSlice(raw, "keyring_keys", cfg.KeyringKeys)
	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func rawString(raw map[string]any, key string) string {
	s, _ := raw[key].(string)
	return s
}

func rawStringMap(raw map[string]any, key string) map[string]string {
	m, ok := raw[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rawStringSlice(raw map[string]any, key string) []string {
	s, ok := raw[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, v := range s {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func setRawString(raw map[string]any, key, val string) {
	if val == "" {
		delete(raw, key)
		return
	}
	raw[key] = val
}

func setRawStringMap(raw map[string]any, key string, m map[string]string) {
	if len(m) == 0 {
		delete(raw, key)
		return
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	raw[key] = out
}

func setRawStringSlice(raw map[string]any, key string, s []string) {
	if len(s) == 0 {
		delete(raw, key)
		return
	}
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	raw[key] = out
}
