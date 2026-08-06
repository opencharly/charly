package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/opencharly/spec/ops"
)

// credential_plugin.go is the CORE adapter for the externalized credential subsystem — the
// VNC-password read for check_endpoint_resolve.go's resolveVNCPassword (the ONE remaining
// in-core consumer). The store itself (keyring + config backends, `charly secrets`, the GPG
// `.secrets` surface) lives OUT-OF-PROCESS in candy/plugin-secrets; pluginCredentialStore
// forwards to verb:credential over the registry. K-wave 2 cone CONTESTED THINned 213→~105:
// the CredentialStore interface, Get/Set/Delete/List/Name, awaitUnlock, and the unreachable
// Get-fallback are DELETED (zero production callers — the keyring-unlock wait lives
// plugin-side in candy/plugin-pod's pluginAwaitKeyringUnlock).
const CredServiceVNC = "charly/vnc"

// credentialResolver is the richer-resolution seam: a store that classifies a lookup's
// source (env/keyring/config/locked/unavailable/default) implements it.
type credentialResolver interface {
	resolve(service, key string) (value, source string)
}

// credentialInput / credentialReply are the verb:credential wire forms, byte-compatible with
// candy/plugin-secrets (verb_credential.go). The `health` method is GONE (K5 seam-death).
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

func (s pluginCredentialStore) call(in credentialInput) (credentialReply, error) {
	return s.callCtx(context.Background(), in)
}

// callCtx resolves verb:credential (lazy-connecting a baked binary or building from the
// project's candy source on first use — both cached by the registry) and invokes one
// credential operation through the standard Invoke envelope, propagating ctx.
func (pluginCredentialStore) callCtx(ctx context.Context, in credentialInput) (credentialReply, error) {
	prov, ok := connectPluginByWord(ClassVerb, "credential")
	if !ok {
		return credentialReply{}, fmt.Errorf(
			"credential plugin (verb:credential) did not connect — install candy/plugin-secrets " +
				"alongside charly (/usr/lib/charly/plugins) or run from a project composing it")
	}
	return invokeTyped[credentialInput, credentialReply](ctx, prov, "credential", ops.OpRun, in)
}

// resolve classifies a lookup's source (env/keyring/config/locked/unavailable/default).
func (s pluginCredentialStore) resolve(service, key string) (value, source string) {
	r, err := s.call(credentialInput{Method: "resolve", Service: service, Key: key})
	if err != nil {
		return "", "unavailable"
	}
	return r.Value, r.Source
}

var (
	defaultStoreMu  sync.Mutex
	defaultStoreVal credentialResolver
)

// DefaultCredentialStore returns the active credential store — the out-of-process
// pluginCredentialStore.
func DefaultCredentialStore() credentialResolver {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	if defaultStoreVal == nil {
		defaultStoreVal = pluginCredentialStore{}
	}
	return defaultStoreVal
}

// ResolveCredential checks an env var override, then the active store. Returns the value
// and its source: "env" | "keyring" | "config" | "locked" | "unavailable" | "default".
// The env precedence is owned here (core owns the process env); the store/source
// classification comes from the plugin's resolve (or a test fake).
func ResolveCredential(envVar, service, key, defaultVal string) (value, source string) {
	if envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			return v, "env"
		}
	}
	r := DefaultCredentialStore()
	v, src := r.resolve(service, key)
	if v != "" {
		return v, src
	}
	if src == "" {
		src = "default"
	}
	return defaultVal, src
}
