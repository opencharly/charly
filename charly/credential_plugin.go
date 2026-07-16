package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// credential_plugin.go is the CORE adapter for the externalized credential subsystem.
// The ENTIRE store implementation (keyring + config backends, the Secret Service client,
// the `charly secrets` CLI, the GPG `.secrets` surface) lives OUT-OF-PROCESS in
// candy/plugin-secrets — the C2 dep-shed removed github.com/zalando/go-keyring from
// charly/go.mod. Every core credential consumer (enc.go, secrets.go, layer_secrets.go,
// config_secret_migration.go, runtime_config.go, vnc_helpers.go, android_deploy_cmd.go,
// migrate_charly_cutover4.go) keeps using the SAME CredentialStore interface + the SAME
// ResolveCredential entry point; pluginCredentialStore forwards every call to
// verb:credential over the provider registry (built from candy source on a dev host, or
// the baked /usr/lib/charly/plugins binary on an installed host / in a deployed container).

// CredServiceVNC is the bare-key credential service (VNC passwords use a bare key; every
// other service uses a composite "service/key" map key). Kept in core because core
// consumers (runtime_config.go, secrets.go, vnc_helpers.go) name it directly.
const CredServiceVNC = "charly/vnc"

// CredentialStore abstracts secret storage backends. The implementation is the
// out-of-process pluginCredentialStore; tests inject an in-memory fake via
// setDefaultCredentialStoreForTest.
type CredentialStore interface {
	Get(service, key string) (string, error)
	Set(service, key, value string) error
	Delete(service, key string) error
	List(service string) ([]string, error)
	Name() string
}

// credentialResolver is the richer-resolution seam: a store that can classify a lookup's
// source (env/keyring/config/locked/unavailable/default) implements it, so
// ResolveCredential surfaces "locked"/"unavailable" to enc.go's keyring-wait loop.
type credentialResolver interface {
	resolve(service, key string) (value, source string)
}

// credentialHealther is the doctor seam: a store that can report keyring / secret-storage
// health implements it (candy/plugin-doctor renders its checks from CredentialHealth,
// fetched host-side via the hostprobe host-build seam).
type credentialHealther interface {
	health() (*CredentialHealth, error)
}

// credentialResetter is the keyring-re-probe seam (enc.go's unlock-wait drives it between
// attempts — the cached store lives in the plugin process now, so the reset propagates).
type credentialResetter interface {
	reset()
}

// credentialAwaiter is the event-driven keyring-unlock seam: a store that can BLOCK until its
// keyring unlocks implements it (enc.go's encrypted-volume mount path drives it when a resolve
// returns source="locked"). The out-of-process pluginCredentialStore satisfies it by RPCing
// verb:credential `await-unlock` — the godbus PropertiesChanged subscription runs IN the plugin
// (the Secret Service owner), which is what sheds godbus from charly's core.
type credentialAwaiter interface {
	awaitUnlock(ctx context.Context, service, key string) (value, source string, err error)
}

// credentialInput / credentialReply / CredentialHealth are the verb:credential wire forms,
// byte-compatible with candy/plugin-secrets (verb_credential.go).
type credentialInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
}

type credentialReply struct {
	Value  string            `json:"value,omitempty"`
	Source string            `json:"source,omitempty"`
	Keys   []string          `json:"keys,omitempty"`
	Name   string            `json:"name,omitempty"`
	Error  string            `json:"error,omitempty"`
	Health *CredentialHealth `json:"health,omitempty"`
}

// CredentialHealth is the keyring/secret-storage diagnostic verb:credential `health` returns; the
// externalized candy/plugin-doctor renders it. Now homed in sdk/spec (core + plugin-doctor consumers, R3).
type CredentialHealth = spec.CredentialHealth

// pluginCredentialStore dispatches every credential operation to verb:credential.
type pluginCredentialStore struct{}

// call invokes one credential operation with a background context (the non-blocking store
// methods — get/set/delete/list/name/resolve/health/reset all complete promptly).
func (s pluginCredentialStore) call(in credentialInput) (credentialReply, error) {
	return s.callCtx(context.Background(), in)
}

// callCtx resolves the verb:credential provider (lazy-connecting a baked binary or building
// from the project's candy source on first use — both cached by the registry) and invokes
// one credential operation through the standard Invoke envelope, propagating ctx to the
// gRPC call. The blocking `await-unlock` method passes a SIGINT/SIGTERM-cancellable ctx so
// `systemctl stop` ends an unbounded keyring wait cleanly (gRPC cancels the plugin's Invoke).
func (pluginCredentialStore) callCtx(ctx context.Context, in credentialInput) (credentialReply, error) {
	prov, ok := connectPluginByWord(ClassVerb, "credential")
	if !ok {
		return credentialReply{}, fmt.Errorf(
			"credential plugin (verb:credential) did not connect — install candy/plugin-secrets " +
				"alongside charly (/usr/lib/charly/plugins) or run from a project composing it")
	}
	return invokeTyped[credentialInput, credentialReply](ctx, prov, "credential", OpRun, in)
}

func (s pluginCredentialStore) Get(service, key string) (string, error) {
	r, err := s.call(credentialInput{Method: "get", Service: service, Key: key})
	if err != nil {
		return "", err
	}
	if r.Error != "" {
		return "", errors.New(r.Error)
	}
	return r.Value, nil
}

func (s pluginCredentialStore) Set(service, key, value string) error {
	r, err := s.call(credentialInput{Method: "set", Service: service, Key: key, Value: value})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

func (s pluginCredentialStore) Delete(service, key string) error {
	r, err := s.call(credentialInput{Method: "delete", Service: service, Key: key})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

func (s pluginCredentialStore) List(service string) ([]string, error) {
	r, err := s.call(credentialInput{Method: "list", Service: service})
	if err != nil {
		return nil, err
	}
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	return r.Keys, nil
}

func (s pluginCredentialStore) Name() string {
	r, err := s.call(credentialInput{Method: "name"})
	if err != nil {
		return "config" // best-effort fallback name when the plugin is unreachable
	}
	return r.Name
}

func (s pluginCredentialStore) resolve(service, key string) (value, source string) {
	r, err := s.call(credentialInput{Method: "resolve", Service: service, Key: key})
	if err != nil {
		return "", "unavailable"
	}
	return r.Value, r.Source
}

// awaitUnlock BLOCKS until the credential at service/key is resolvable (the plugin's
// resolveStoreChain no longer returns source="locked") or ctx is cancelled. It RPCs
// verb:credential `await-unlock`, which runs the godbus PropertiesChanged subscription IN
// the plugin process (the Secret Service owner). The blocking gRPC call survives an unbounded
// wait (go-plugin sets no keepalive/idle timeout on the local Unix-socket connection) and is
// cancelled by ctx — the host cancels on SIGINT/SIGTERM, gRPC propagates the cancellation to
// the plugin's Invoke ctx, and the wait loop returns.
func (s pluginCredentialStore) awaitUnlock(ctx context.Context, service, key string) (string, string, error) {
	r, err := s.callCtx(ctx, credentialInput{Method: "await-unlock", Service: service, Key: key})
	if err != nil {
		return "", "", err
	}
	if r.Error != "" {
		return "", "", errors.New(r.Error)
	}
	return r.Value, r.Source, nil
}

func (s pluginCredentialStore) health() (*CredentialHealth, error) {
	r, err := s.call(credentialInput{Method: "health"})
	if err != nil {
		return nil, err
	}
	return r.Health, nil
}

// reset re-probes the keyring in the plugin process — but ONLY when verb:credential is
// already connected, so a reset never pays a build/connect just to clear a cache that was
// never populated (the no-keyring / no-project path stays a no-op).
func (s pluginCredentialStore) reset() {
	if _, ok := providerRegistry.ResolveVerb("credential"); !ok {
		return
	}
	_, _ = s.call(credentialInput{Method: "reset"})
}

var (
	defaultStoreMu       sync.Mutex
	defaultStoreVal      CredentialStore
	defaultStoreOverride CredentialStore // test seam (setDefaultCredentialStoreForTest)
)

// DefaultCredentialStore returns the active credential store — the out-of-process
// pluginCredentialStore, or a test-injected fake.
func DefaultCredentialStore() CredentialStore {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	if defaultStoreOverride != nil {
		return defaultStoreOverride
	}
	if defaultStoreVal == nil {
		defaultStoreVal = pluginCredentialStore{}
	}
	return defaultStoreVal
}

// setDefaultCredentialStoreForTest injects a fake store so consumer tests exercise the
// credential-consuming code paths WITHOUT building/connecting the out-of-process plugin.
func setDefaultCredentialStoreForTest(s CredentialStore) {
	defaultStoreMu.Lock()
	defaultStoreOverride = s
	defaultStoreMu.Unlock()
}

// resetDefaultCredentialStoreForTest clears the test override.
func resetDefaultCredentialStoreForTest() {
	defaultStoreMu.Lock()
	defaultStoreOverride = nil
	defaultStoreMu.Unlock()
}

// resetDefaultStore forces the active store to re-probe (the secret_backend-change path in
// runtime_config.go + tests). For the plugin store it propagates as a `reset` RPC; for a
// test fake implementing credentialResetter it calls its reset.
func resetDefaultStore() {
	if r, ok := DefaultCredentialStore().(credentialResetter); ok {
		r.reset()
	}
}

// resetDefaultCredentialStore is the keyring-wait subset alias (enc.go drives it between
// unlock attempts). Same propagation as resetDefaultStore.
func resetDefaultCredentialStore() { resetDefaultStore() }

// ResolveCredential checks an env var override, then the active store. Returns the value
// and its source: "env" | "keyring" | "config" | "locked" | "unavailable" | "default".
// The env precedence is owned here (the core owns the process env); the store/source
// classification comes from the plugin's resolve (or a test fake).
func ResolveCredential(envVar, service, key, defaultVal string) (value, source string) {
	if envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			return v, "env"
		}
	}
	store := DefaultCredentialStore()
	if r, ok := store.(credentialResolver); ok {
		v, src := r.resolve(service, key)
		if v != "" {
			return v, src
		}
		if src == "" {
			src = "default"
		}
		return defaultVal, src
	}
	if v, err := store.Get(service, key); err == nil && v != "" {
		return v, store.Name()
	}
	return defaultVal, "default"
}

// resolveSecretBackend reads the secret_backend setting from env or the core runtime
// config (enc.go + config_image.go consult it to pick their keyring-wait strategy /
// quadlet emission). The plugin keeps its OWN copy for its store selection — two modules
// each reading one config key across the process boundary, not in-module duplication.
func resolveSecretBackend() string {
	if v := os.Getenv("CHARLY_SECRET_BACKEND"); v != "" {
		return v
	}
	cfg, err := kit.LoadRuntimeConfig()
	if err != nil {
		return "auto"
	}
	if cfg.SecretBackend != "" {
		return cfg.SecretBackend
	}
	return "auto"
}

// credentialHealth runs the doctor keyring/secret-storage probe via verb:credential
// `health` (the plugin owns the Secret Service now).
func credentialHealth() (*CredentialHealth, error) {
	store := DefaultCredentialStore()
	if h, ok := store.(credentialHealther); ok {
		return h.health()
	}
	return nil, fmt.Errorf("active credential store does not support a health probe")
}
