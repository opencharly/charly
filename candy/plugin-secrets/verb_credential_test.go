package secrets

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opencharly/charly/candy/plugin-secrets/params"
)

// withConfigBackend points the plugin at an isolated config-file backend in a temp dir,
// so dispatchCredential round-trips against a clean ConfigFileStore without a keyring.
func withConfigBackend(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
	t.Cleanup(func() { RuntimeConfigPath = defaultRuntimeConfigPath; resetDefaultStore() })
	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	resetDefaultStore()
}

// TestDispatchCredential_RoundTrip exercises the verb:credential operation dispatch the
// core's pluginCredentialStore drives over gRPC: name → set → get → resolve → delete →
// list against the config backend.
func TestDispatchCredential_RoundTrip(t *testing.T) {
	withConfigBackend(t)

	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "name"}); r.Name != "config" {
		t.Fatalf("name: got %q, want config", r.Name)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "set", Service: "charly/secret", Key: "TOK", Value: "v1"}); r.Error != "" {
		t.Fatalf("set: %v", r.Error)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "get", Service: "charly/secret", Key: "TOK"}); r.Value != "v1" {
		t.Fatalf("get: got %q, want v1 (err=%q)", r.Value, r.Error)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "resolve", Service: "charly/secret", Key: "TOK"}); r.Value != "v1" || r.Source != "config" {
		t.Fatalf("resolve: got value=%q source=%q, want v1/config", r.Value, r.Source)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "list", Service: "charly/secret"}); len(r.Keys) != 1 || r.Keys[0] != "TOK" {
		t.Fatalf("list: got %v, want [TOK]", r.Keys)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "delete", Service: "charly/secret", Key: "TOK"}); r.Error != "" {
		t.Fatalf("delete: %v", r.Error)
	}
	if r := dispatchCredential(context.Background(), params.CredentialInput{Method: "get", Service: "charly/secret", Key: "TOK"}); r.Value != "" {
		t.Fatalf("get after delete: got %q, want empty", r.Value)
	}
}

// TestDispatchCredential_UnknownMethod proves an unrecognized method is a loud error.
func TestDispatchCredential_UnknownMethod(t *testing.T) {
	withConfigBackend(t)
	r := dispatchCredential(context.Background(), params.CredentialInput{Method: "bogus"})
	if r.Error == "" {
		t.Fatal("expected an error for an unknown credential method")
	}
}

// TestProbeCredentialHealth_NoBus verifies the health probe degrades gracefully when no
// session bus / keyring is reachable (the doctor renders it from this struct).
func TestProbeCredentialHealth_NoBus(t *testing.T) {
	withConfigBackend(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent-charly-test-bus")
	h := dispatchCredential(context.Background(), params.CredentialInput{Method: "health"}).Health
	if h == nil {
		t.Fatal("health probe returned nil")
	}
	if h.ConfiguredBackend != "config" {
		t.Errorf("ConfiguredBackend = %q, want config", h.ConfiguredBackend)
	}
}
