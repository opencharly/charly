package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/opencharly/spec/ops"
	"os"
	"sync"
)

// credential_plugin.go is the CORE adapter for the externalized credential subsystem.
// The ENTIRE store implementation (keyring + config backends, the Secret Service client,
// the `charly secrets` CLI, the GPG `.secrets` surface) lives OUT-OF-PROCESS in
// candy/plugin-secrets — the C2 dep-shed removed github.com/zalando/go-keyring from
// charly/go.mod. The remaining in-core credential consumer is check_endpoint_resolve.go
// (VNC password resolution for the live check endpoint); every credential-consuming
// capability named in earlier cutovers here (enc.go, secrets.go, layer_secrets.go,
// runtime_config.go, vnc_helpers.go, migrate_charly_cutover4.go) has since relocated
// out-of-process (e.g. enc.go → candy/plugin-enc) or been removed. pluginCredentialStore
// forwards every call to verb:credential over the provider registry (built from candy
// source on a dev host, or the baked /usr/lib/charly/plugins binary on an installed host /
// in a deployed container).

// CredServiceVNC is the bare-key credential service (VNC passwords use a bare key; every
// other service uses a composite "service/key" map key). Kept in core because core
// consumers (runtime_config.go, vnc_helpers.go) name it directly.
const CredServiceVNC = "charly/vnc"

// CredentialStore abstracts secret storage backends. The implementation is the
// out-of-process pluginCredentialStore.
type CredentialStore interface {
	Get(service, key string) (string, error)
	Set(service, key, value string) error
	Delete(service, key string) error
	List(service string) ([]string, error)
	Name() string
}

// credentialResolver is the richer-resolution seam: a store that can classify a lookup's
// source (env/keyring/config/locked/unavailable/default) implements it, so
// ResolveCredential surfaces the full source classification to its callers (the encryption
// keyring-wait loop now lives out-of-process in candy/plugin-enc, reached over verb:credential
// rather than this in-core function directly).
type credentialResolver interface {
	resolve(service, key string) (value, source string)
}

// credentialInput / credentialReply are the verb:credential wire forms, byte-compatible with
// candy/plugin-secrets (verb_credential.go). The `health` method + its spec.CredentialHealth
// payload are GONE from this adapter (K5 seam-death): candy/plugin-doctor, the one former
// consumer of credentialHealth()/health(), now peer-InvokeProviders verb:credential's `health`
// method itself (hostfacts.go), with its own local wire-shape copy — the SAME
// process-boundary-copy convention this file's siblings already use.
type credentialInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
}

type credentialReply struct {
	Value  string   `json:"value,omitempty"`
	Source string   `json:"source,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Name   string   `json:"name,omitempty"`
	Error  string   `json:"error,omitempty"`
}

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
	return invokeTyped[credentialInput, credentialReply](ctx, prov, "credential", ops.OpRun, in)
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

var (
	defaultStoreMu  sync.Mutex
	defaultStoreVal CredentialStore
)

// DefaultCredentialStore returns the active credential store — the out-of-process
// pluginCredentialStore.
func DefaultCredentialStore() CredentialStore {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	if defaultStoreVal == nil {
		defaultStoreVal = pluginCredentialStore{}
	}
	return defaultStoreVal
}

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
