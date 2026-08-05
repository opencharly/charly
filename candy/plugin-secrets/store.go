package secrets

import (
	"fmt"
	"os"
	"sync"
)

// store.go is the credential-store backend SELECTION + the store-chain resolution, ported out
// of the core's credential_store.go (the externalization). The host's pluginCredentialStore
// forwards every CredentialStore method + `resolve` to verb:credential, which dispatches to the
// store selected here. The env-var precedence stays in the CORE's ResolveCredential (it owns the
// process env); the keyring/config selection + the source classification live HERE now.

// CredentialStore abstracts secret storage backends (keyring, config file). Plugin-local mirror
// of the core interface — the two stores (ConfigFileStore, KeyringStore) satisfy it.
type CredentialStore interface {
	Get(service, key string) (string, error)
	Set(service, key, value string) error
	Delete(service, key string) error
	List(service string) ([]string, error)
	Name() string
}

var (
	defaultStoreOnce     sync.Once
	defaultStoreVal      CredentialStore
	defaultStoreProbeErr error // non-nil when the configured (keyring) backend failed and we fell back
	storeInfoOnce        sync.Once
)

// resetDefaultCredentialStore clears the cached store singleton, forcing a re-probe on the next
// DefaultCredentialStore() call (the keyring wait loop resets between unlock attempts).
func resetDefaultCredentialStore() {
	defaultStoreOnce = sync.Once{}
	defaultStoreVal = nil
	defaultStoreProbeErr = nil
	resetKeyringState()
}

// DefaultCredentialStore returns the active store per the secret_backend setting, probing the
// keyring once when backend is "auto". Ported verbatim from the core's DefaultCredentialStore.
func DefaultCredentialStore() CredentialStore {
	defaultStoreOnce.Do(func() {
		backend := resolveSecretBackend()
		switch backend {
		case "keyring":
			store := &KeyringStore{}
			if err := store.Probe(); err != nil {
				if GetKeyringState() == KeyringLocked {
					fmt.Fprintf(os.Stderr, "WARNING: System keyring is locked. Credentials are unavailable until unlocked.\n")
					fmt.Fprintf(os.Stderr, "  Unlock your keyring, or switch backend: charly config set secret_backend config\n")
					defaultStoreVal = store
				} else {
					fmt.Fprintf(os.Stderr, "ERROR: secret_backend is 'keyring' but system keyring is not available: %v\n", err)
					fmt.Fprintf(os.Stderr, "Falling back to config file. Fix the keyring or run: charly config set secret_backend config\n")
					defaultStoreVal = &ConfigFileStore{}
					defaultStoreProbeErr = err
				}
				return
			}
			defaultStoreVal = store
		case "config":
			defaultStoreVal = &ConfigFileStore{}
		default: // "auto" or ""
			store := &KeyringStore{}
			if err := store.Probe(); err == nil {
				defaultStoreVal = store
				return
			} else {
				defaultStoreProbeErr = err
			}
			if GetKeyringState() == KeyringLocked {
				fmt.Fprintf(os.Stderr, "WARNING: System keyring is locked. Using config file for credentials.\n")
				fmt.Fprintf(os.Stderr, "  Unlock your keyring, or run: charly config set secret_backend config\n")
			}
			defaultStoreVal = &ConfigFileStore{}
		}
	})
	return defaultStoreVal
}

// PrintStoreInfo prints a one-time informational message about the active backend (used by the
// `charly secrets` set/import commands). Ported from the core.
func PrintStoreInfo() {
	storeInfoOnce.Do(func() {
		store := DefaultCredentialStore()
		switch store.Name() {
		case "keyring (locked)":
			// Warning already printed by DefaultCredentialStore()
		case "keyring":
			fmt.Fprintf(os.Stderr, "Using system keyring for credential storage.\n")
			fmt.Fprintf(os.Stderr, "To force a specific backend: charly config set secret_backend keyring|config\n")
		case "config":
			if resolveSecretBackend() == "config" {
				return
			}
			fmt.Fprintf(os.Stderr, "System keyring not available (no D-Bus session bus).\n")
			fmt.Fprintf(os.Stderr, "Credentials will be stored in ~/.config/charly/config.yml (permissions: 0600).\n")
			fmt.Fprintf(os.Stderr, "To suppress this message: charly config set secret_backend config\n")
			fmt.Fprintf(os.Stderr, "For Secret Service storage, run a keyring provider (gnome-keyring, kwalletd, KeePassXC with FdoSecrets).\n")
		}
	})
}

// resolveStoreChain is the env-LESS store resolution (the part of the core's ResolveCredential
// AFTER the env-var check, which stays in the core): query the active store, fall back to the
// config file when the keyring is locked/active-but-missing, and classify the source —
// keyring/config/locked/unavailable/default. The host's verb:credential `resolve` returns this.
func resolveStoreChain(service, key string) (value, source string) {
	store := DefaultCredentialStore()
	if v, err := store.Get(service, key); err == nil && v != "" {
		return v, store.Name()
	} else if IsKeyringLocked(err) {
		fallback := &ConfigFileStore{}
		if v, err := fallback.Get(service, key); err == nil && v != "" {
			return v, "config"
		}
		return "", "locked"
	}

	storeName := store.Name()
	if storeName == "keyring" || storeName == "keyring (locked)" {
		fallback := &ConfigFileStore{}
		if v, err := fallback.Get(service, key); err == nil && v != "" {
			return v, "config"
		}
	}
	if defaultStoreProbeErr != nil && storeName == "config" {
		return "", "unavailable"
	}
	return "", "default"
}

// resolveSecretBackend reads the secret_backend setting from env or config (the plugin's copy of
// the core config reader — used for the store selection above and the doctor health report).
func resolveSecretBackend() string {
	if v := os.Getenv("CHARLY_SECRET_BACKEND"); v != "" {
		return v
	}
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "auto"
	}
	if cfg.SecretBackend != "" {
		return cfg.SecretBackend
	}
	return "auto"
}
