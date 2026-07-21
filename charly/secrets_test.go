package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// TestCollectSecretsFromLabels / TestSecretArgs relocated to
// sdk/deploykit/secret_probe_test.go (Cutover B-1 fix round — the functions
// themselves moved there and are no longer duplicated in this package).

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

// TestCredServiceForSecret / TestCredKeyForSecret relocated to
// sdk/deploykit/secret_probe_test.go alongside CredServiceForSecret/
// CredKeyForSecret themselves.

// ---------------------------------------------------------------------------
// Step 4 tests: credential resolution for secret_accepts / secret_requires.
// These exercise resolveSecretValue's new Service/Key override path and
// CollectCandySecretAccepts against an in-memory ConfigFileStore backed by a
// temp directory. They do not touch podman (which would require a live
// daemon); the RotateOnConfig short-circuit bypass is validated by the live
// integration tests in plan §8.3 (rotation test).
// ---------------------------------------------------------------------------

// withIsolatedCredentialStore sets up the ConfigFileStore backend in a temp
// directory and forces the credential store singleton to re-probe so tests
// start from a clean slate. Returns the ConfigFileStore for direct seeding.
//
// SECURITY: this helper also unsets common credential env vars (OPENROUTER_API_KEY,
// OLLAMA_API_KEY, IMMICH_API_KEY, WEBUI_ADMIN_PASSWORD) so the test process cannot
// accidentally resolve — and print in a failure diff — a real user credential
// that happens to be set in the outer shell. All Step 4 tests below use
// synthetic env var names (TEST_OV_*) that can never match a real credential,
// but these defensive unsets are belt-and-braces for future test additions.
func withIsolatedCredentialStore(t *testing.T) *fakeCredentialStore {
	t.Helper()
	// Defensive unsets: prevent any real credential in the outer shell from
	// leaking into test assertions (which may print the resolved value).
	for _, name := range []string{
		"OPENROUTER_API_KEY", "OLLAMA_API_KEY", "IMMICH_API_KEY",
		"WEBUI_ADMIN_PASSWORD", "TELEGRAM_BOT_TOKEN", "SLACK_BOT_TOKEN",
		"DISCORD_BOT_TOKEN", "OPENAI_API_KEY",
	} {
		t.Setenv(name, "")
	}
	return installFakeCredentialStore(t)
}

// TestResolveSecretValueServiceKeyOverride — the new Service/Key override
// path on resolveSecretValue queries the credential store at the exact path
// the candy author requested (via `key: charly/api-key/routea`) and returns the
// value verbatim. The default fallback chain is NOT used when both Service
// and Key are set.
//
// Uses synthetic env var name (TEST_CHARLY_CRED_ROUTEA_KEY) so an accidental
// assertion-diff cannot print a real user credential from the outer shell.
func TestResolveSecretValueServiceKeyOverride(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	// Seed two distinct synthetic values at two different paths. The override
	// path must win over the default path.
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
	val, src := resolveSecretValue(cs, "openwebui", "")
	if val != "test-from-override" {
		t.Errorf("resolveSecretValue value mismatch, source=%q", src)
	}
	if src != "config" {
		t.Errorf("resolveSecretValue source = %q, want %q", src, "config")
	}
}

// TestResolveSecretValueServiceKeyOverrideMissing — when the override path is
// set but the credential store has no value there, resolveSecretValue returns
// ("", "default") immediately without falling back to the legacy chain. This
// matters for the secret_requires hard-fail path: we want the failure to be
// visible at the exact key the candy author specified, not masked by a
// fallback lookup that happens to find something elsewhere.
func TestResolveSecretValueServiceKeyOverrideMissing(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	// Seed only the default-chain path — the override path is empty. If the
	// override branch falls through to the legacy chain, the test catches it
	// by getting a non-empty value.
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
	val, src := resolveSecretValue(cs, "openwebui", "")
	if val != "" {
		t.Errorf("resolveSecretValue returned a non-empty value (source=%q) — the override branch must not fall through to the legacy chain", src)
	}
	if src != "default" {
		t.Errorf("resolveSecretValue source = %q, want %q", src, "default")
	}
}

// TestResolveSecretValueLegacyChainUnchanged — when Service/Key are both
// empty, the legacy chain (used by candy-owned db-password secrets) still
// works: env var → charly/secret/<podman-name> → charly/secret/<bare-name>.
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
	val, _ := resolveSecretValue(cs, "immich", "")
	if val != "legacy-value" {
		t.Errorf("legacy chain value = %q, want %q", val, "legacy-value")
	}
}

// TestCollectCandySecretAcceptsHappyPath — given a meta with both
// secret_requires and secret_accepts, and a credential store that has all
// values, CollectCandySecretAccepts returns one CollectedSecret per entry with
// correct naming, Service/Key parsed from the optional `key:` field, and
// RotateOnConfig=true on every entry.
//
// Uses synthetic env var names (TEST_CHARLY_CRED_*) to guarantee the test can
// never resolve — and print in a failure diff — a real user credential.
func TestCollectCandySecretAcceptsHappyPath(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	// Required: default key path (charly/secret/TEST_CHARLY_CRED_REQUIRED)
	if err := store.Set("charly/secret", "TEST_CHARLY_CRED_REQUIRED", "required-val"); err != nil {
		t.Fatalf("seed required: %v", err)
	}
	// Accepts with explicit key override
	if err := store.Set("charly/api-key", "routea", "override-val"); err != nil {
		t.Fatalf("seed routea: %v", err)
	}
	// Accepts with default path
	if err := store.Set("charly/secret", "TEST_CHARLY_CRED_ROUTEB", "default-val"); err != nil {
		t.Fatalf("seed routeb: %v", err)
	}

	meta := &spec.BoxMetadata{
		SecretRequire: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_REQUIRED", Description: "required"},
		},
		SecretAccept: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_ROUTEA", Description: "override", Key: "charly/api-key/routea"},
			{Name: "TEST_CHARLY_CRED_ROUTEB", Description: "default"},
		},
	}

	collected, resolutions := CollectCandySecretAccepts("openwebui", "", meta)

	if len(collected) != 3 {
		t.Fatalf("collected has %d entries, want 3", len(collected))
	}

	// All entries must be RotateOnConfig=true
	for _, cs := range collected {
		if !cs.RotateOnConfig {
			t.Errorf("CollectedSecret %q has RotateOnConfig=false, want true", cs.Name)
		}
	}

	// Find the override entry and verify Service/Key were parsed from the
	// `key: charly/api-key/routea` override.
	var routea *deploykit.CollectedSecret
	for i, cs := range collected {
		if cs.Env == "TEST_CHARLY_CRED_ROUTEA" {
			routea = &collected[i]
			break
		}
	}
	if routea == nil {
		t.Fatal("TEST_CHARLY_CRED_ROUTEA not in collected")
	}
	if routea.Service != "charly/api-key" {
		t.Errorf("routea.Service = %q, want %q", routea.Service, "charly/api-key")
	}
	if routea.Key != "routea" {
		t.Errorf("routea.Key = %q, want %q", routea.Key, "routea")
	}
	if routea.Name != "charly-openwebui-test-charly-cred-routea" {
		t.Errorf("routea.Name = %q, want %q", routea.Name, "charly-openwebui-test-charly-cred-routea")
	}

	// Find the default-path entry and verify default Service/Key applied.
	var routeb *deploykit.CollectedSecret
	for i, cs := range collected {
		if cs.Env == "TEST_CHARLY_CRED_ROUTEB" {
			routeb = &collected[i]
			break
		}
	}
	if routeb == nil {
		t.Fatal("TEST_CHARLY_CRED_ROUTEB not in collected")
	}
	if routeb.Service != "charly/secret" || routeb.Key != "TEST_CHARLY_CRED_ROUTEB" {
		t.Errorf("routeb default routing = (%q, %q), want (charly/secret, TEST_CHARLY_CRED_ROUTEB)", routeb.Service, routeb.Key)
	}

	// All three resolutions must be Resolved=true
	if len(resolutions) != 3 {
		t.Fatalf("resolutions has %d entries, want 3", len(resolutions))
	}
	for _, r := range resolutions {
		if !r.Resolved {
			t.Errorf("resolution for %s is Resolved=false", r.Name)
		}
	}
	// TEST_CHARLY_CRED_REQUIRED must have Required=true
	var req *SecretResolution
	for i, r := range resolutions {
		if r.Name == "TEST_CHARLY_CRED_REQUIRED" {
			req = &resolutions[i]
			break
		}
	}
	if req == nil || !req.Required {
		t.Errorf("required resolution missing or Required=false: %+v", req)
	}
}

// TestCollectCandySecretAcceptsMissingRequired — when a secret_requires entry
// is not stored in the credential store, CollectCandySecretAccepts omits it
// from the collected list and records Resolved=false / Required=true in the
// resolutions list. The checkMissingSecretRequires helper (Step 6) is the one
// that turns this into a user-facing hard fail.
//
// Uses synthetic env var names so the test cannot accidentally pick up a
// real credential from the outer shell.
func TestCollectCandySecretAcceptsMissingRequired(t *testing.T) {
	withIsolatedCredentialStore(t) // empty store

	meta := &spec.BoxMetadata{
		SecretRequire: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_REQUIRED", Description: "required"},
		},
		SecretAccept: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_OPT", Description: "optional"},
		},
	}

	collected, resolutions := CollectCandySecretAccepts("openwebui", "", meta)

	if len(collected) != 0 {
		t.Errorf("collected has %d entries, want 0 (empty credential store)", len(collected))
	}
	if len(resolutions) != 2 {
		t.Fatalf("resolutions = %d entries, want 2", len(resolutions))
	}

	want := []SecretResolution{
		{Name: "TEST_CHARLY_CRED_REQUIRED", Source: "default", Resolved: false, Required: true},
		{Name: "TEST_CHARLY_CRED_OPT", Source: "default", Resolved: false, Required: false},
	}
	if !reflect.DeepEqual(resolutions, want) {
		t.Errorf("resolutions mismatch\n got %+v\nwant %+v", resolutions, want)
	}
}

// TestCollectCandySecretAcceptsNilMeta — defensive: nil metadata must not
// panic and must return empty slices.
func TestCollectCandySecretAcceptsNilMeta(t *testing.T) {
	withIsolatedCredentialStore(t)
	collected, resolutions := CollectCandySecretAccepts("anything", "", nil)
	if collected != nil || resolutions != nil {
		t.Errorf("nil meta should return (nil, nil), got (%+v, %+v)", collected, resolutions)
	}
}

// TestCollectCandySecretAcceptsEnvOverride — plan §2.5 one-shot import via -e:
// when the env var is already set in the process environment, ResolveCredential
// returns source "env" and CollectCandySecretAccepts picks it up without
// touching the credential store. This is what makes `charly config -e FOO=val`
// work for secret_accepts entries.
//
// Uses a synthetic env var name (TEST_CHARLY_CRED_IMPORTED) — the credential
// store is empty but t.Setenv seeds the process env, so the "env" source
// wins. Importantly this test assertion never prints the resolved value
// itself — the value is a test-controlled string ("from-env-synthetic") so
// even if an assertion diff were accidentally printed, no real credential
// could leak.
func TestCollectCandySecretAcceptsEnvOverride(t *testing.T) {
	withIsolatedCredentialStore(t) // empty store

	t.Setenv("TEST_CHARLY_CRED_IMPORTED", "from-env-synthetic")

	meta := &spec.BoxMetadata{
		SecretAccept: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_IMPORTED", Description: "opt", Key: "charly/api-key/imported"},
		},
	}

	collected, resolutions := CollectCandySecretAccepts("openwebui", "", meta)

	if len(collected) != 1 {
		t.Fatalf("collected has %d, want 1", len(collected))
	}
	if len(resolutions) != 1 || !resolutions[0].Resolved || resolutions[0].Source != "env" {
		t.Errorf("resolution[0] source = %q, Resolved=%t, want (env, true)", resolutions[0].Source, resolutions[0].Resolved)
	}
}

// TestMergedSecretsIncludeCredentialBacked — regression test for a bug caught
// during live-system validation of Step 6: `updateAllDeployedQuadlets` at
// `config_image.go` was calling ONLY `CollectSecretsFromLabels` and forgetting
// to also call `CollectCandySecretAccepts`, so consumer quadlets regenerated
// via `charly config <provider> --update-all` dropped their credential-backed
// `Secret=` directives, and `secret_requires` entrypoints crashlooped.
//
// The invariant this test locks in: anywhere the charly code path builds the
// `cfg.Secret` slice that reaches `sdk/deploykit/quadlet.go`'s
// `emitContainerSection` Secret= directive loop, it MUST
// merge both candy-owned (`CollectSecretsFromLabels`) and credential-backed
// (`CollectCandySecretAccepts`) collections. The `Run()` path does this at
// `config_image.go` after the env resolution; the `updateAllDeployedQuadlets`
// path does it where `provisioned` is constructed. Any third path that
// generates a quadlet without merging both sources is a regression.
func TestMergedSecretsIncludeCredentialBacked(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	// A realistic openwebui-style metadata: one candy-owned webui-secret-key
	// AND one credential-backed WEBUI_ADMIN_PASSWORD via secret_requires.
	meta := &spec.BoxMetadata{
		Secret: []LabelSecretEntry{
			{Name: "webui-secret-key", Target: "/run/secrets/webui_secret_key", Env: "WEBUI_SECRET_KEY"},
		},
		SecretRequire: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_ADMIN_PASSWORD", Description: "synthetic admin password"},
		},
		SecretAccept: []spec.EnvDependency{
			{Name: "TEST_CHARLY_CRED_ROUTEA", Description: "synthetic optional", Key: "charly/api-key/routea"},
		},
	}
	// Seed only the credentials (the candy-owned webui-secret-key is
	// auto-generated by ProvisionPodmanSecrets; we skip that path here by
	// working at the collection layer).
	if err := store.Set("charly/secret", "TEST_CHARLY_CRED_ADMIN_PASSWORD", "admin-value"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := store.Set("charly/api-key", "routea", "route-value"); err != nil {
		t.Fatalf("seed routea: %v", err)
	}

	// Mirror the merge that both Run() and updateAllDeployedQuadlets must
	// perform: start with candy-owned, append credential-backed.
	candyOwned := deploykit.CollectSecretsFromLabels("openwebui", meta.Secret)
	credBacked, _ := CollectCandySecretAccepts("openwebui", "", meta)
	merged := append(slices.Clone(candyOwned), credBacked...)

	// Expect 3 entries: 1 candy-owned + 2 credential-backed.
	if len(merged) != 3 {
		t.Fatalf("merged has %d entries, want 3", len(merged))
	}

	byEnv := map[string]deploykit.CollectedSecret{}
	for _, cs := range merged {
		byEnv[cs.Env] = cs
	}

	// Candy-owned entry: RotateOnConfig must be false (never rotate
	// candy-owned secrets, e.g., a live postgres db-password would break).
	webui, ok := byEnv["WEBUI_SECRET_KEY"]
	if !ok {
		t.Fatal("WEBUI_SECRET_KEY (candy-owned) missing from merged slice")
	}
	if webui.RotateOnConfig {
		t.Error("WEBUI_SECRET_KEY.RotateOnConfig = true, want false (candy-owned secrets must not rotate)")
	}

	// Credential-backed entries: RotateOnConfig must be true, so
	// ProvisionPodmanSecrets bypasses the deploykit.PodmanSecretExists short-circuit
	// on every charly config and re-creates the podman secret with the latest
	// credential store value.
	for _, name := range []string{"TEST_CHARLY_CRED_ADMIN_PASSWORD", "TEST_CHARLY_CRED_ROUTEA"} {
		cs, ok := byEnv[name]
		if !ok {
			t.Errorf("%s (credential-backed) missing from merged slice", name)
			continue
		}
		if !cs.RotateOnConfig {
			t.Errorf("%s.RotateOnConfig = false, want true (credential-backed secrets must rotate)", name)
		}
	}

	// Name shape: all merged entries must have the "charly-openwebui-" prefix.
	for _, cs := range merged {
		if !strings.HasPrefix(cs.Name, "charly-openwebui-") {
			t.Errorf("CollectedSecret.Name = %q, want prefix charly-openwebui-", cs.Name)
		}
	}
}

// TestApplySecretRefresh_NamedAllAndUnmatched relocated to
// sdk/deploykit/secret_probe_test.go alongside ApplySecretRefresh itself
// (Cutover B-1 fix round).
