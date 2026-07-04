package secrets

import (
	"fmt"
	"slices"
	"strings"
)

// knownServicePrefixes lists non-VNC service prefixes that use composite
// keys in VncPasswords. Order matters: longer prefixes first for correct
// matching when services share a common prefix.
var knownServicePrefixes = []string{
	"charly/secret/",
	"charly/enc/",
}

// ConfigFileStore implements CredentialStore using the existing plaintext
// credential maps in ~/.config/charly/config.yml. This is the fallback backend
// for headless environments without a system keyring.
type ConfigFileStore struct{}

func (c *ConfigFileStore) Get(service, key string) (string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "", err
	}
	return lookupConfigCredential(cfg, service, key), nil
}

func (c *ConfigFileStore) Set(service, key, value string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	setConfigCredential(cfg, service, key, value)
	return SaveRuntimeConfig(cfg)
}

func (c *ConfigFileStore) Delete(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	deleteConfigCredential(cfg, service, key)
	return SaveRuntimeConfig(cfg)
}

func (c *ConfigFileStore) List(service string) ([]string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	switch service {
	case CredServiceVNC:
		if cfg.VncPasswords == nil {
			return nil, nil
		}
		keys := make([]string, 0, len(cfg.VncPasswords))
		for k := range cfg.VncPasswords {
			// Skip composite keys (belong to other services)
			if strings.Contains(k, "/") {
				continue
			}
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return keys, nil
	default:
		// Non-VNC services use composite keys "service/key" in VncPasswords
		if cfg.VncPasswords == nil {
			return nil, nil
		}
		prefix := service + "/"
		keys := make([]string, 0)
		for k := range cfg.VncPasswords {
			if after, ok := strings.CutPrefix(k, prefix); ok {
				keys = append(keys, after)
			}
		}
		if len(keys) == 0 {
			return nil, nil
		}
		slices.Sort(keys)
		return keys, nil
	}
}

func (c *ConfigFileStore) Name() string {
	return "config"
}

// lookupConfigCredential reads a credential from the appropriate config map.
func lookupConfigCredential(cfg *RuntimeConfig, service, key string) string {
	if cfg.VncPasswords == nil {
		return ""
	}
	switch service {
	case CredServiceVNC:
		return cfg.VncPasswords[key]
	default:
		// Non-VNC services are stored with composite key "service/key"
		return cfg.VncPasswords[fmt.Sprintf("%s/%s", service, key)]
	}
}

// setConfigCredential writes a credential to the appropriate config map.
func setConfigCredential(cfg *RuntimeConfig, service, key, value string) {
	if cfg.VncPasswords == nil {
		cfg.VncPasswords = make(map[string]string)
	}
	switch service {
	case CredServiceVNC:
		cfg.VncPasswords[key] = value
	default:
		// Non-VNC services use composite key "service/key"
		cfg.VncPasswords[fmt.Sprintf("%s/%s", service, key)] = value
	}
}

// deleteConfigCredential removes a credential from the appropriate config map.
func deleteConfigCredential(cfg *RuntimeConfig, service, key string) {
	if cfg.VncPasswords == nil {
		return
	}
	switch service {
	case CredServiceVNC:
		delete(cfg.VncPasswords, key)
	default:
		delete(cfg.VncPasswords, fmt.Sprintf("%s/%s", service, key))
	}
}

// HasPlaintextCredentials returns the number of plaintext credentials
// currently stored in config.yml credential maps.
func HasPlaintextCredentials(cfg *RuntimeConfig) int {
	return len(cfg.VncPasswords)
}

// PlaintextCredentialEntries returns all plaintext credential entries as
// service/key pairs for migration or audit purposes.
func PlaintextCredentialEntries(cfg *RuntimeConfig) []struct{ Service, Key, Value string } {
	entries := make([]struct{ Service, Key, Value string }, 0, len(cfg.VncPasswords))
	for k, v := range cfg.VncPasswords {
		service, key := parseCompositeKey(k)
		entries = append(entries, struct{ Service, Key, Value string }{service, key, v})
	}
	return entries
}

// parseCompositeKey splits a VncPasswords map key into (service, key).
// Composite keys are "service/key" where service may contain slashes
// (e.g., "charly/secret/my-key" -> service="charly/secret", key="my-key").
// Non-composite keys are VNC passwords (e.g., "my-image" -> service=CredServiceVNC).
func parseCompositeKey(compositeKey string) (service, key string) {
	// Check known multi-slash service prefixes first
	for _, prefix := range knownServicePrefixes {
		if after, ok := strings.CutPrefix(compositeKey, prefix); ok {
			return strings.TrimSuffix(prefix, "/"), after
		}
	}
	// No known prefix matched: if it contains a slash, treat as single-slash
	// service/key (for future unknown services).
	if before, after, ok := strings.Cut(compositeKey, "/"); ok {
		return before, after
	}
	// No slash at all: it's a bare VNC key
	return CredServiceVNC, compositeKey
}
