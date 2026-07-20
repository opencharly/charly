package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// setupIsolatedConfigStore wires a test-isolated in-memory credential store (the real
// store is out-of-process in candy/plugin-secrets now). Returns a teardown func; defer it.
func setupIsolatedConfigStore(t *testing.T) (cleanup func()) {
	t.Helper()
	setDefaultCredentialStoreForTest(newFakeCredentialStore())
	return resetDefaultCredentialStoreForTest
}

// TestEnsureCandySecret_PresentInStore verifies that a value already
// stored at (service, key) is returned as-is — no auto-generation,
// no rewrite.
func TestEnsureCandySecret_PresentInStore(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	if err := DefaultCredentialStore().Set("charly/secret", "EXISTING_TOKEN", "preset-value"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dep := spec.EnvDependency{Name: "EXISTING_TOKEN"}
	val, source := ensureCandySecret(dep, true)

	if val != "preset-value" {
		t.Errorf("expected preset-value, got %q", val)
	}
	if source == "auto-generated" {
		t.Errorf("expected source != auto-generated; got %q (regression: pre-existing values must NOT regenerate)", source)
	}
}

// TestEnsureCandySecret_RequiredMissingAutoGenerates is the user's
// primary requested behavior: missing + required → 32-byte hex,
// persisted to the active store.
func TestEnsureCandySecret_RequiredMissingAutoGenerates(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := spec.EnvDependency{Name: "K3S_CLUSTER_TOKEN"}
	val, source := ensureCandySecret(dep, true)

	if source != "auto-generated" {
		t.Errorf("expected source=auto-generated, got %q", source)
	}
	// 32 bytes url-safe base64 = 44 chars (Fernet-key compatible).
	// See generateRandomSecretToken in secrets.go for rationale.
	if len(val) != 44 {
		t.Errorf("expected 44-char url-safe base64 token, got %d chars: %q", len(val), val)
	}
	for _, c := range val {
		isUrlSafeB64 := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') || c == '-' || c == '_' || c == '='
		if !isUrlSafeB64 {
			t.Errorf("expected url-safe base64, found invalid char %q in %q", c, val)
			break
		}
	}

	// Persistence: the value must be retrievable via the same store.
	stored, err := DefaultCredentialStore().Get("charly/secret", "K3S_CLUSTER_TOKEN")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored != val {
		t.Errorf("persistence mismatch: returned %q, store has %q", val, stored)
	}
}

// TestEnsureCandySecret_IdempotentAcrossCalls verifies the race-free
// invariant: a second resolver call (e.g., k3s-agent reading the
// token after k3s-server's first-call auto-gen) returns the SAME
// value, not a fresh regeneration.
func TestEnsureCandySecret_IdempotentAcrossCalls(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := spec.EnvDependency{Name: "SHARED_TOKEN"}

	// First call — auto-generates + persists.
	val1, source1 := ensureCandySecret(dep, true)
	if source1 != "auto-generated" {
		t.Fatalf("first call expected auto-generated, got %q", source1)
	}

	// Second call — must read persisted value.
	val2, source2 := ensureCandySecret(dep, true)
	if val1 != val2 {
		t.Errorf("idempotency broken: first=%q, second=%q (regression: server+agent would mismatch)", val1, val2)
	}
	if source2 == "auto-generated" {
		t.Errorf("second call regenerated instead of reading persisted (source=%q)", source2)
	}
}

// TestEnsureCandySecret_OptionalMissingReturnsEmpty verifies that
// non-required deps (secret_accepts) do NOT auto-generate when missing.
// Caller is responsible for falling back to dep.Default.
func TestEnsureCandySecret_OptionalMissingReturnsEmpty(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := spec.EnvDependency{Name: "OPTIONAL_KEY"}
	val, source := ensureCandySecret(dep, false)

	if val != "" {
		t.Errorf("expected empty value for optional+missing, got %q", val)
	}
	if source == "auto-generated" {
		t.Errorf("optional missing must NOT auto-generate; got source=%q", source)
	}

	// Confirm nothing was written to the store either.
	if stored, _ := DefaultCredentialStore().Get("charly/secret", "OPTIONAL_KEY"); stored != "" {
		t.Errorf("optional missing leaked %q to store", stored)
	}
}

// TestEnsureCandySecret_CustomKeyRoutesToOverride verifies that the
// `key:` override on EnvDependency (e.g., `key: charly/api-key/openrouter`)
// routes the lookup AND the auto-gen persistence to the override
// service/key pair, not the default charly/secret/<name>.
func TestEnsureCandySecret_CustomKeyRoutesToOverride(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := spec.EnvDependency{
		Name: "MY_VAR_NAME",
		Key:  "charly/api-key/openrouter",
	}

	val, source := ensureCandySecret(dep, true)
	if source != "auto-generated" {
		t.Fatalf("expected auto-generated, got %q", source)
	}

	// The auto-gen MUST persist at the override location, not at the default.
	atOverride, _ := DefaultCredentialStore().Get("charly/api-key", "openrouter")
	if atOverride != val {
		t.Errorf("expected persistence at override (charly/api-key, openrouter), got %q (val=%q)", atOverride, val)
	}
	atDefault, _ := DefaultCredentialStore().Get("charly/secret", "MY_VAR_NAME")
	if atDefault != "" {
		t.Errorf("default location should be empty, got %q (key override leaked)", atDefault)
	}
}

// TestResolveCandySecrets_RequiredAutoGen exercises the wrapper that
// the deploy-add path actually calls: a Candy with secret_requires
// must always resolve (auto-gen guarantees non-empty values).
func TestResolveCandySecrets_RequiredAutoGen(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	layer := &Candy{
		secretRequires: []spec.EnvDependency{
			{Name: "K3S_CLUSTER_TOKEN"},
		},
	}
	env := ResolveCandySecret(layer)
	val, ok := env["K3S_CLUSTER_TOKEN"]
	if !ok || val == "" {
		t.Fatalf("expected K3S_CLUSTER_TOKEN to be resolved (auto-gen), got env=%v", env)
	}
	if len(val) != 44 {
		t.Errorf("expected 44-char url-safe base64 token (Fernet-compatible), got %d chars", len(val))
	}
}

// TestResolveCandySecrets_OptionalDefaultFallback exercises the
// secret_accepts path with a Default value: missing + optional →
// dep.Default goes into env (not auto-gen).
func TestResolveCandySecrets_OptionalDefaultFallback(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	layer := &Candy{
		secretAccepts: []spec.EnvDependency{
			{Name: "OPTIONAL_VAR", Default: "fallback-value"},
		},
	}
	env := ResolveCandySecret(layer)
	if env["OPTIONAL_VAR"] != "fallback-value" {
		t.Errorf("expected fallback-value, got %q", env["OPTIONAL_VAR"])
	}
}

// TestResolveSecretsForCandies_TwoCandiesSameSecret verifies the
// race-free invariant at the wrapper level: two candies (think
// k3s-server + k3s-agent) declaring the same secret_requires
// resolve to the SAME value.
func TestResolveSecretsForCandies_TwoCandiesSameSecret(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	server := &Candy{
		secretRequires: []spec.EnvDependency{{Name: "K3S_CLUSTER_TOKEN"}},
	}
	agent := &Candy{
		secretRequires: []spec.EnvDependency{{Name: "K3S_CLUSTER_TOKEN"}},
	}
	env := ResolveSecretForCandy([]*Candy{server, agent})

	val := env["K3S_CLUSTER_TOKEN"]
	if val == "" || len(val) != 44 {
		t.Fatalf("expected 44-char url-safe base64 token (Fernet-compatible), got %q", val)
	}
	// And the persisted store must have exactly that value.
	stored, _ := DefaultCredentialStore().Get("charly/secret", "K3S_CLUSTER_TOKEN")
	if stored != val {
		t.Errorf("server+agent token mismatch: env=%q stored=%q", val, stored)
	}
}
