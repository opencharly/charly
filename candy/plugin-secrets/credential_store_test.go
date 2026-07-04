package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCredentialEnvOverride(t *testing.T) {
	t.Setenv("TEST_CRED_ENV", "from-env")

	val, source := ResolveCredential("TEST_CRED_ENV", "charly/test", "key1", "fallback")
	if val != "from-env" || source != "env" {
		t.Errorf("expected (from-env, env), got (%s, %s)", val, source)
	}
}

func TestResolveCredentialDefault(t *testing.T) {
	// Ensure no env var set
	_ = os.Unsetenv("TEST_CRED_NONE")

	// Use config backend (no keyring in tests) with empty config
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
	resetDefaultStore()
	defer resetDefaultStore()

	t.Setenv("CHARLY_SECRET_BACKEND", "config")

	val, source := ResolveCredential("TEST_CRED_NONE", "charly/test", "nonexistent", "default-val")
	if val != "default-val" || source != "default" {
		t.Errorf("expected (default-val, default), got (%s, %s)", val, source)
	}
}

func TestConfigFileStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	store := &ConfigFileStore{}

	// Set and Get for each service type
	tests := []struct {
		service string
		key     string
		value   string
	}{
		{CredServiceVNC, "my-image", "vncpass123"},
	}

	for _, tt := range tests {
		if err := store.Set(tt.service, tt.key, tt.value); err != nil {
			t.Fatalf("Set(%s, %s): %v", tt.service, tt.key, err)
		}
		got, err := store.Get(tt.service, tt.key)
		if err != nil {
			t.Fatalf("Get(%s, %s): %v", tt.service, tt.key, err)
		}
		if got != tt.value {
			t.Errorf("Get(%s, %s) = %q, want %q", tt.service, tt.key, got, tt.value)
		}
	}

	// List
	keys, err := store.List(CredServiceVNC)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "my-image" {
		t.Errorf("List(charly/vnc) = %v, want [my-image]", keys)
	}

	// Delete
	if err := store.Delete(CredServiceVNC, "my-image"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := store.Get(CredServiceVNC, "my-image")
	if got != "" {
		t.Errorf("Get after Delete = %q, want empty", got)
	}
}

func TestConfigFileStoreName(t *testing.T) {
	store := &ConfigFileStore{}
	if store.Name() != "config" {
		t.Errorf("Name() = %q, want %q", store.Name(), "config")
	}
}

func TestHasPlaintextCredentials(t *testing.T) {
	cfg := &RuntimeConfig{
		VncPasswords: map[string]string{"img1": "pw1"},
	}
	if n := HasPlaintextCredentials(cfg); n != 1 {
		t.Errorf("HasPlaintextCredentials = %d, want 1", n)
	}
}

func TestPlaintextCredentialEntries(t *testing.T) {
	cfg := &RuntimeConfig{
		VncPasswords: map[string]string{"img1": "pw1"},
	}
	entries := PlaintextCredentialEntries(cfg)
	if len(entries) != 1 {
		t.Fatalf("PlaintextCredentialEntries len = %d, want 1", len(entries))
	}
	if entries[0].Service != CredServiceVNC || entries[0].Key != "img1" || entries[0].Value != "pw1" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestResolveSecretBackendEnv(t *testing.T) {
	t.Setenv("CHARLY_SECRET_BACKEND", "keyring")
	if got := resolveSecretBackend(); got != "keyring" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "keyring")
	}
}

func TestResolveSecretBackendConfig(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	// Write config with secret_backend
	cfg := &RuntimeConfig{SecretBackend: "config"}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig: %v", err)
	}

	_ = os.Unsetenv("CHARLY_SECRET_BACKEND")
	if got := resolveSecretBackend(); got != "config" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "config")
	}
}

func TestResolveSecretBackendDefault(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	_ = os.Unsetenv("CHARLY_SECRET_BACKEND")
	if got := resolveSecretBackend(); got != "auto" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "auto")
	}
}

// TestConfigFileStoreSecretRoundtrip verifies that "charly/secret" service
// credentials stored via ConfigFileStore can be read back. This is a
// regression test: before the fix, setConfigCredential stored these as
// composite keys in VncPasswords, but lookupConfigCredential returned
// nil for non-VNC services (only VNC was handled).
func TestConfigFileStoreSecretRoundtrip(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	store := &ConfigFileStore{}

	// Store a secret (as charly config does for DB passwords)
	secretName := "charly-immich-ml-db-password"
	secretValue := "abc123def456"
	if err := store.Set("charly/secret", secretName, secretValue); err != nil {
		t.Fatalf("Set(charly/secret, %s): %v", secretName, err)
	}

	// Read it back (this was broken before the fix — returned empty string)
	got, err := store.Get("charly/secret", secretName)
	if err != nil {
		t.Fatalf("Get(charly/secret, %s): %v", secretName, err)
	}
	if got != secretValue {
		t.Errorf("Get(charly/secret, %s) = %q, want %q", secretName, got, secretValue)
	}

	// List should return the secret name
	keys, err := store.List("charly/secret")
	if err != nil {
		t.Fatalf("List(charly/secret): %v", err)
	}
	if len(keys) != 1 || keys[0] != secretName {
		t.Errorf("List(charly/secret) = %v, want [%s]", keys, secretName)
	}

	// Delete and verify gone
	if err := store.Delete("charly/secret", secretName); err != nil {
		t.Fatalf("Delete(charly/secret, %s): %v", secretName, err)
	}
	got, _ = store.Get("charly/secret", secretName)
	if got != "" {
		t.Errorf("Get after Delete = %q, want empty", got)
	}
}

// TestConfigFileStoreVNCAndSecretIsolation verifies that VNC and charly/secret
// credentials stored in the same underlying map don't interfere.
func TestConfigFileStoreVNCAndSecretIsolation(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	store := &ConfigFileStore{}

	// Store both a VNC password and a secret
	if err := store.Set(CredServiceVNC, "my-image", "vncpass"); err != nil {
		t.Fatalf("Set VNC: %v", err)
	}
	if err := store.Set("charly/secret", "charly-my-image-db-password", "dbpass"); err != nil {
		t.Fatalf("Set secret: %v", err)
	}

	// VNC List should only show VNC keys (not composite secret keys)
	vncKeys, _ := store.List(CredServiceVNC)
	if len(vncKeys) != 1 || vncKeys[0] != "my-image" {
		t.Errorf("List(charly/vnc) = %v, want [my-image]", vncKeys)
	}

	// Secret List should only show secret keys
	secretKeys, _ := store.List("charly/secret")
	if len(secretKeys) != 1 || secretKeys[0] != "charly-my-image-db-password" {
		t.Errorf("List(charly/secret) = %v, want [charly-my-image-db-password]", secretKeys)
	}

	// Values should be independently retrievable
	vncVal, _ := store.Get(CredServiceVNC, "my-image")
	if vncVal != "vncpass" {
		t.Errorf("VNC Get = %q, want vncpass", vncVal)
	}
	secretVal, _ := store.Get("charly/secret", "charly-my-image-db-password")
	if secretVal != "dbpass" {
		t.Errorf("Secret Get = %q, want dbpass", secretVal)
	}
}

// TestPlaintextCredentialEntriesCompositeKeys verifies that composite keys
// (used for non-VNC services) are correctly parsed back into service/key pairs.
func TestPlaintextCredentialEntriesCompositeKeys(t *testing.T) {
	cfg := &RuntimeConfig{
		VncPasswords: map[string]string{
			"my-image": "vncpass",
			"charly/secret/charly-immich-db-password": "dbpass",
		},
	}
	entries := PlaintextCredentialEntries(cfg)
	if len(entries) != 2 {
		t.Fatalf("PlaintextCredentialEntries len = %d, want 2", len(entries))
	}
	// Find each entry by value (map iteration order is non-deterministic)
	foundVNC, foundSecret := false, false
	for _, e := range entries {
		switch e.Value {
		case "vncpass":
			if e.Service != CredServiceVNC || e.Key != "my-image" {
				t.Errorf("VNC entry: service=%q key=%q, want charly/vnc/my-image", e.Service, e.Key)
			}
			foundVNC = true
		case "dbpass":
			if e.Service != "charly/secret" || e.Key != "charly-immich-db-password" {
				t.Errorf("Secret entry: service=%q key=%q, want charly/secret/charly-immich-db-password", e.Service, e.Key)
			}
			foundSecret = true
		}
	}
	if !foundVNC {
		t.Error("VNC entry not found")
	}
	if !foundSecret {
		t.Error("Secret entry not found")
	}
}
