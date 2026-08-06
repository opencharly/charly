package fleet

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// secrets_test.go — relocated from charly/secrets_test.go (#55 decoupling, Batch A):
// TestQuadletSecretDirectives/TestQuadletSecretEnvDirectives assert deploykit.GenerateQuadlet
// directly, zero charly coupling; the Step-4 ResolveSecretValue tests need a
// CredentialResolver-shaped callback — charly's own version is backed by its
// package-main-internal credential adapter (credential_plugin.go), so this ports a standalone
// in-memory fake + a local closure replicating ResolveCredential's env-var-then-store
// precedence, rather than reaching into charly core.

func TestQuadletSecretDirectives(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:  "test-img",
		ImageRef: "ghcr.io/test/test-img:latest",
		Home:     "/tmp",
		Secrets: []deploykit.CollectedSecret{
			{Name: "charly-test-img-api-key", Target: "/run/secrets/api_key"},
			{Name: "charly-test-img-db-pass", Target: "/run/secrets/db_pass"},
		},
	}

	content := deploykit.GenerateQuadlet(cfg)
	if !strings.Contains(content, "Secret=charly-test-img-api-key,target=/run/secrets/api_key") {
		t.Error("missing Secret= directive for api-key")
	}
	if !strings.Contains(content, "Secret=charly-test-img-db-pass,target=/run/secrets/db_pass") {
		t.Error("missing Secret= directive for db-pass")
	}
}

// TestQuadletSecretEnvDirectives — Step 9 confirmation test for
// credential-backed secrets (secret_accepts / secret_requires). Asserts that
// a CollectedSecret with Env set (the shape produced by
// CollectCandySecretAccepts) emits Secret=<name>,type=env,target=<var> and
// that the generated quadlet does NOT contain:
//
//   - an Environment=<var>=... line for the same env var (which would leak
//     a plaintext value), or
//   - an ExecStartPre=charly config resolve-secrets %N line (which plan §2.2
//     explicitly decided against — podman secrets are self-sufficient at
//     runtime, no re-query is needed).
//
// This locks in architectural decision 2.2: credential-backed secrets flow
// through the existing Secret=<name>,type=env,... emission in
// sdk/deploykit/quadlet.go's emitContainerSection with zero changes to that
// function itself. Any future refactor that adds an ExecStartPre or
// rehydrates the value as an Environment= line will fail this test.
func TestQuadletSecretEnvDirectives(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:  "openwebui",
		ImageRef: "ghcr.io/opencharly/openwebui:latest",
		UID:      1000,
		GID:      1000,
		Env:      []string{"WEBUI_URL=http://localhost:8080"},
		Secrets: []deploykit.CollectedSecret{
			{
				Name:           "charly-openwebui-openrouter-api-key",
				Env:            "OPENROUTER_API_KEY",
				SecretName:     "OPENROUTER_API_KEY",
				Service:        "charly/api-key",
				Key:            "openrouter",
				RotateOnConfig: true,
			},
			{
				Name:           "charly-openwebui-webui-admin-password",
				Env:            "WEBUI_ADMIN_PASSWORD",
				SecretName:     "WEBUI_ADMIN_PASSWORD",
				Service:        "charly/secret",
				Key:            "WEBUI_ADMIN_PASSWORD",
				RotateOnConfig: true,
			},
		},
	}

	content := deploykit.GenerateQuadlet(cfg)

	// Positive: the Secret= directives for both credential-backed secrets
	// must be present. These are what podman uses to inject the decrypted
	// value as an env var at container start.
	wantDirectives := []string{
		"Secret=charly-openwebui-openrouter-api-key,type=env,target=OPENROUTER_API_KEY",
		"Secret=charly-openwebui-webui-admin-password,type=env,target=WEBUI_ADMIN_PASSWORD",
	}
	for _, want := range wantDirectives {
		if !strings.Contains(content, want) {
			t.Errorf("quadlet missing expected Secret= directive:\n  %s\n\nfull content:\n%s", want, content)
		}
	}

	// Negative: a plaintext Environment= line for any of the credential env
	// var names would mean the pipeline is carrying the value inline — that
	// must never happen for secret_accepts/secret_requires entries.
	forbiddenLines := []string{
		"Environment=OPENROUTER_API_KEY=",
		"Environment=WEBUI_ADMIN_PASSWORD=",
	}
	for _, forbidden := range forbiddenLines {
		if strings.Contains(content, forbidden) {
			t.Errorf("quadlet contains forbidden plaintext line %q — credential-backed env vars must flow via Secret=, not Environment=", forbidden)
		}
	}

	// Negative: the plan explicitly does NOT add an ExecStartPre for
	// re-resolving credentials at runtime. Podman secrets live in
	// podman's own on-disk store after `charly config` writes them, so no
	// boot-time credential-store access is needed. A future refactor that
	// adds such a line would defeat the simplification and reintroduce
	// the "keyring locked at boot" failure modes that this design
	// deliberately avoids.
	if strings.Contains(content, "ExecStartPre=") && strings.Contains(content, "config resolve-secrets") {
		t.Errorf("quadlet contains ExecStartPre=... config resolve-secrets — plan §2.2 explicitly does not add this line")
	}

	// Positive: the unrelated plaintext env var (WEBUI_URL) passes through
	// normally as an Environment= directive. This confirms we haven't
	// overscrubbed the env list.
	if !strings.Contains(content, "Environment=WEBUI_URL=http://localhost:8080") {
		t.Errorf("plaintext env var WEBUI_URL was dropped from quadlet — overscrub")
	}
}

// ---------------------------------------------------------------------------
// Step 4 tests: credential resolution for secret_accepts / secret_requires.
// These exercise deploykit.ResolveSecretValue's Service/Key override path against
// an in-memory fake credential store. They do not touch podman or a real
// credential-store plugin.
// ---------------------------------------------------------------------------

// fakeCredentialStore is a standalone in-memory credential map — the test-fixture twin of
// charly/credential_fake_test.go's fakeCredentialStore (that one is wired into charly's own
// CredentialStore adapter singleton, which lives package-main-side and this out-of-module
// plugin package cannot reach).
type fakeCredentialStore struct {
	mu sync.Mutex
	m  map[string]string // "service\x00key" → value
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{m: map[string]string{}}
}

func fakeCredKey(service, key string) string { return service + "\x00" + key }

func (f *fakeCredentialStore) Set(service, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[fakeCredKey(service, key)] = value
	return nil
}

func (f *fakeCredentialStore) Get(service, key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[fakeCredKey(service, key)]
}

// testResolveCredential mirrors charly's ResolveCredential precedence: an env var override
// first, then the fake store lookup — the CredentialResolver-shaped callback
// deploykit.ResolveSecretValue takes as its 5th parameter.
func testResolveCredential(store *fakeCredentialStore) deploykit.CredentialResolver {
	return func(envVar, service, key, defaultVal string) (value, source string) {
		if envVar != "" {
			if v := os.Getenv(envVar); v != "" {
				return v, "env"
			}
		}
		if v := store.Get(service, key); v != "" {
			return v, "config"
		}
		return defaultVal, "default"
	}
}

// withIsolatedCredentialStore returns a fresh fake store, after defensively unsetting common
// credential env var names so no real user credential in the outer shell can leak into a test
// assertion (belt-and-braces; every test below uses synthetic TEST_* env var names that can
// never match a real credential).
func withIsolatedCredentialStore(t *testing.T) *fakeCredentialStore {
	t.Helper()
	for _, name := range []string{
		"OPENROUTER_API_KEY", "OLLAMA_API_KEY", "IMMICH_API_KEY",
		"WEBUI_ADMIN_PASSWORD", "TELEGRAM_BOT_TOKEN", "SLACK_BOT_TOKEN",
		"DISCORD_BOT_TOKEN", "OPENAI_API_KEY",
	} {
		t.Setenv(name, "")
	}
	return newFakeCredentialStore()
}

// TestResolveSecretValueServiceKeyOverride — the Service/Key override path on
// deploykit.ResolveSecretValue queries the credential store at the exact path the candy
// author requested (via `key: charly/api-key/routea`) and returns the value verbatim. The
// default fallback chain is NOT used when both Service and Key are set.
func TestResolveSecretValueServiceKeyOverride(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	if err := store.Set("charly/api-key", "routea", "test-from-override"); err != nil {
		t.Fatalf("Set charly/api-key/routea: %v", err)
	}
	if err := store.Set("charly/secret", "TEST_CHARLY_CRED_ROUTEA_KEY", "test-from-default"); err != nil {
		t.Fatalf("Set charly/secret/TEST_CHARLY_CRED_ROUTEA_KEY: %v", err)
	}

	cs := deploykit.CollectedSecret{
		Name:           "charly-openwebui-test-charly-cred-routea-key",
		Env:            "TEST_CHARLY_CRED_ROUTEA_KEY",
		SecretName:     "TEST_CHARLY_CRED_ROUTEA_KEY",
		Service:        "charly/api-key",
		Key:            "routea",
		RotateOnConfig: true,
	}
	val, src := deploykit.ResolveSecretValue(cs, "openwebui", "", "charly/vnc", testResolveCredential(store))
	if val != "test-from-override" {
		t.Errorf("resolveSecretValue value mismatch, source=%q", src)
	}
	if src != "config" {
		t.Errorf("resolveSecretValue source = %q, want %q", src, "config")
	}
}

// TestResolveSecretValueServiceKeyOverrideMissing — when the override path is set but the
// credential store has no value there, deploykit.ResolveSecretValue returns ("", "default")
// immediately without falling back to the legacy chain.
func TestResolveSecretValueServiceKeyOverrideMissing(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	if err := store.Set("charly/secret", "TEST_CHARLY_CRED_ROUTEB_KEY", "legacy-chain-value"); err != nil {
		t.Fatalf("Set default path: %v", err)
	}

	cs := deploykit.CollectedSecret{
		Env:            "TEST_CHARLY_CRED_ROUTEB_KEY",
		SecretName:     "TEST_CHARLY_CRED_ROUTEB_KEY",
		Service:        "charly/api-key",
		Key:            "routeb", // override path is empty in the seeded store
		RotateOnConfig: true,
	}
	val, src := deploykit.ResolveSecretValue(cs, "openwebui", "", "charly/vnc", testResolveCredential(store))
	if val != "" {
		t.Errorf("resolveSecretValue returned a non-empty value (source=%q) — the override branch must not fall through to the legacy chain", src)
	}
	if src != "default" {
		t.Errorf("resolveSecretValue source = %q, want %q", src, "default")
	}
}

// TestResolveSecretValueLegacyChainUnchanged — when Service/Key are both empty, the legacy
// chain (used by candy-owned db-password secrets) still works: env var → charly/secret/<name>.
func TestResolveSecretValueLegacyChainUnchanged(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	if err := store.Set("charly/secret", "charly-immich-db-password", "legacy-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cs := deploykit.CollectedSecret{
		Name:       "charly-immich-db-password",
		Env:        "DB_PASSWORD",
		SecretName: "db-password",
		// Service / Key left empty — use legacy chain
	}
	val, _ := deploykit.ResolveSecretValue(cs, "immich", "", "charly/vnc", testResolveCredential(store))
	if val != "legacy-value" {
		t.Errorf("legacy chain value = %q, want %q", val, "legacy-value")
	}
}
