package main

import (
	"testing"
)

// TestSecretsCLI_ConfigBackendRoundTrip exercises the `charly secrets set` path against the
// config-file backend and confirms the value is retrievable through the active store and
// enumerable via the list helper. Moved here from charly/ with the secrets CLI it drives.
func TestSecretsCLI_ConfigBackendRoundTrip(t *testing.T) {
	withConfigBackend(t)

	set := &SecretsSetCmd{Service: "charly/secret", Key: "R10_PROBE", Value: "hello"}
	if err := set.Run(); err != nil {
		t.Fatalf("set: %v", err)
	}

	val, err := DefaultCredentialStore().Get("charly/secret", "R10_PROBE")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "hello" {
		t.Errorf("round-trip value = %q, want %q", val, "hello")
	}

	names, err := collectCredentialNames()
	if err != nil {
		t.Fatalf("collectCredentialNames: %v", err)
	}
	found := false
	for _, n := range names {
		if n.Service == "charly/secret" && n.Key == "R10_PROBE" {
			found = true
		}
	}
	if !found {
		t.Errorf("collectCredentialNames did not include the stored credential: %+v", names)
	}
}
