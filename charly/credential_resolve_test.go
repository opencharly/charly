package main

import "testing"

// TestResolveCredential_EnvPrecedence exercises ResolveCredential's own env-var-override path
// directly. Env precedence is owned by this function (its doc comment: "The env precedence is
// owned here"), but until now it was only exercised INDIRECTLY — via a deploykit.CredentialResolver
// callback pass-through in a test that asserted deploykit.ResolveSecretValue's own merge logic, not
// ResolveCredential's. That test has relocated to candy/plugin-fleet (#55 decoupling, Batch A)
// alongside the pure deploykit behavior it actually asserted, using its own local fake resolver —
// leaving ResolveCredential's env-classification behavior genuinely untested in charly. This closes
// that gap: an env var override must win over the (unreachable, in this fast unit test) plugin
// store, and its source classification must read back as "env".
func TestResolveCredential_EnvPrecedence(t *testing.T) {
	t.Setenv("TEST_RESOLVE_CREDENTIAL_ENV_PRECEDENCE", "from-env")
	value, source := ResolveCredential("TEST_RESOLVE_CREDENTIAL_ENV_PRECEDENCE", "charly/test", "key1", "fallback")
	if value != "from-env" {
		t.Errorf("value = %q, want %q", value, "from-env")
	}
	if source != "env" {
		t.Errorf("source = %q, want %q", source, "env")
	}
}
