package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-secrets/params"
)

// verb_credential.go dispatches the verb:credential operation (the host's pluginCredentialStore
// + ResolveCredential `resolve` + doctor's `health` probe) to the selected backend (store.go).

// credentialReply is the wire form verb:credential returns — byte-compatible with the core's
// credentialReply (charly/credential_plugin.go), the cross-module credential contract.
type credentialReply struct {
	Value  string            `json:"value,omitempty"`
	Source string            `json:"source,omitempty"`
	Keys   []string          `json:"keys,omitempty"`
	Name   string            `json:"name,omitempty"`
	Error  string            `json:"error,omitempty"`
	Health *credentialHealth `json:"health,omitempty"`
}

// credentialHealth is the keyring/secret-storage diagnostic the `health` method returns; the
// core's doctor (secretStorageChecks) renders DoctorCheckResults from it (the plugin owns the
// Secret Service, so it does the probe). Byte-compatible with the core credentialHealth.
type credentialHealth struct {
	BackendName       string   `json:"backend_name"`       // active store Name(): "keyring" | "keyring (locked)" | "config"
	ConfiguredBackend string   `json:"configured_backend"` // resolveSecretBackend(): "auto" | "keyring" | "config"
	KeyringAvailable  bool     `json:"keyring_available"`
	KeyringLocked     bool     `json:"keyring_locked"`
	PlaintextCount    int      `json:"plaintext_count"`
	NoSession         bool     `json:"no_session"` // no session bus — collection checks skipped
	CollErr           string   `json:"coll_err,omitempty"`
	HealthyColls      []string `json:"healthy_colls,omitempty"`
	BrokenColls       []string `json:"broken_colls,omitempty"`
	IndexTotal        int      `json:"index_total"`
	IndexMissing      []string `json:"index_missing,omitempty"`
}

// dispatchCredential routes one credential-store operation. Any backend error is captured on
// reply.Error (never panics) so the host always decodes a reply. ctx is the host's Invoke ctx;
// only the blocking `await-unlock` method consults it (the core cancels on SIGINT/SIGTERM).
func dispatchCredential(ctx context.Context, in params.CredentialInput) credentialReply {
	var reply credentialReply
	errStr := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	// `reset` re-probes the keyring; it must run BEFORE DefaultCredentialStore caches
	// the (possibly locked) store for this op (the core's enc.go unlock-wait drives it
	// between attempts — the cached store lives in THIS process now, so the host's
	// resetDefaultCredentialStore propagates here as a `reset` RPC).
	if in.Method == "reset" {
		resetDefaultCredentialStore()
		return reply
	}
	// `await-unlock` BLOCKS until the keyring unlocks (the externalized enc.go mount
	// waiter — godbus PropertiesChanged subscription on the Secret Service collections),
	// or until the host cancels ctx (SIGINT/SIGTERM). It owns its own store re-probing
	// (keyring_unlock_wait.go), so it bypasses the cached-store fetch below.
	if in.Method == "await-unlock" {
		reply.Value, reply.Source = awaitUnlock(ctx, in.Service, in.Key)
		return reply
	}
	store := DefaultCredentialStore()
	switch in.Method {
	case "get":
		v, err := store.Get(in.Service, in.Key)
		reply.Value, reply.Error = v, errStr(err)
	case "set":
		reply.Error = errStr(store.Set(in.Service, in.Key, in.Value))
	case "delete":
		reply.Error = errStr(store.Delete(in.Service, in.Key))
	case "list":
		ks, err := store.List(in.Service)
		reply.Keys, reply.Error = ks, errStr(err)
	case "name":
		reply.Name = store.Name()
	case "resolve":
		reply.Value, reply.Source = resolveStoreChain(in.Service, in.Key)
	case "health":
		h := probeCredentialHealth()
		reply.Health = &h
	default:
		reply.Error = fmt.Sprintf("plugin-secrets: unknown credential method %q", in.Method)
	}
	return reply
}

// probeCredentialHealth runs the secret-storage diagnostic the core's doctor renders: backend
// availability/lock, plaintext-credential count, Secret Service collection health, and shadow
// index consistency. Ported from the core's doctor.go (checkKeyringHealth +
// checkKeyringIndexConsistency) since the plugin now owns the Secret Service.
func probeCredentialHealth() credentialHealth {
	h := credentialHealth{
		ConfiguredBackend: resolveSecretBackend(),
		BackendName:       DefaultCredentialStore().Name(),
	}
	kr := &KeyringStore{}
	if err := kr.Probe(); err == nil {
		h.KeyringAvailable = true
	}
	h.KeyringLocked = GetKeyringState() == KeyringLocked

	if cfg, err := LoadRuntimeConfig(); err == nil {
		h.PlaintextCount = HasPlaintextCredentials(cfg)
	}

	// Secret Service collection health (mirror doctor.checkKeyringHealth).
	c, err := newSSClient()
	if err != nil {
		h.NoSession = true
		return h
	}
	defer c.close()
	paths, err := c.collections()
	if err != nil {
		h.CollErr = err.Error()
		return h
	}
	if len(paths) == 0 {
		h.NoSession = true // no collections — handled by the backend check upstream
	}
	for _, p := range paths {
		if herr := c.isCollectionHealthy(p); herr != nil {
			h.BrokenColls = append(h.BrokenColls, string(p))
			continue
		}
		if label := c.collectionLabel(p); label != "" {
			h.HealthyColls = append(h.HealthyColls, fmt.Sprintf("%q", label))
		} else {
			h.HealthyColls = append(h.HealthyColls, string(p))
		}
	}

	// Shadow index consistency (mirror doctor.checkKeyringIndexConsistency).
	cfg, err := LoadRuntimeConfig()
	if err == nil && len(cfg.KeyringKeys) > 0 {
		h.IndexTotal = len(cfg.KeyringKeys)
		for _, entry := range cfg.KeyringKeys {
			service, key := parseCompositeKey(entry)
			if service == "" || key == "" {
				continue
			}
			if _, _, ferr := c.findItemByAttrsAnyCollection(map[string]string{"service": service, "username": key}, cfg.KeyringCollectionLabel); ferr != nil && errors.Is(ferr, ErrSSNotFound) {
				h.IndexMissing = append(h.IndexMissing, entry)
			}
		}
	}
	return h
}
