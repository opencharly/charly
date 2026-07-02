package main

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeCredentialStore is an in-memory CredentialStore for CONSUMER tests — the
// credential-store implementation lives out-of-process in candy/plugin-secrets now (the
// go-keyring dep-shed), so core tests that exercise credential-consuming code paths
// (secrets.go, layer_secrets.go, enc.go, the migrate cutover) inject this fake via
// setDefaultCredentialStoreForTest instead of building/connecting the real plugin.
type fakeCredentialStore struct {
	mu sync.Mutex
	m  map[string]string // "service\x00key" → value
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{m: map[string]string{}}
}

func fakeKey(service, key string) string { return service + "\x00" + key }

func (f *fakeCredentialStore) Get(service, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[fakeKey(service, key)], nil
}

func (f *fakeCredentialStore) Set(service, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[fakeKey(service, key)] = value
	return nil
}

func (f *fakeCredentialStore) Delete(service, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, fakeKey(service, key))
	return nil
}

func (f *fakeCredentialStore) List(service string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := service + "\x00"
	var keys []string
	for k := range f.m {
		if after, ok := strings.CutPrefix(k, prefix); ok {
			keys = append(keys, after)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (f *fakeCredentialStore) Name() string { return "config" }

// resolve mirrors the env-LESS store chain classification a config backend returns.
func (f *fakeCredentialStore) resolve(service, key string) (value, source string) {
	if v, _ := f.Get(service, key); v != "" {
		return v, "config"
	}
	return "", "default"
}

// reset is a no-op for the in-memory fake (no keyring to re-probe).
func (f *fakeCredentialStore) reset() {}

// installFakeCredentialStore injects a fresh in-memory store as the active credential
// store and clears it on cleanup. Returns the store for direct seeding.
func installFakeCredentialStore(t *testing.T) *fakeCredentialStore {
	t.Helper()
	f := newFakeCredentialStore()
	setDefaultCredentialStoreForTest(f)
	t.Cleanup(resetDefaultCredentialStoreForTest)
	return f
}
