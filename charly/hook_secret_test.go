package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveHookSecretEnv verifies that credential-backed secret_accept /
// secret_require values are surfaced as NAME=value entries for lifecycle hooks
// (the explicit-`-e` path that delivers e.g. github-runner's RUNNER_TOKEN to the
// post_enable hook, since the secret is scrubbed from c.Env and not reliably
// inherited from a podman type=env secret by `podman exec`).
func TestResolveHookSecretEnv(t *testing.T) {
	if got := resolveHookSecretEnv("img", "", nil); got != nil {
		t.Fatalf("nil meta: want nil, got %v", got)
	}

	// A secret whose value resolves (here via the env-first chain) is surfaced.
	t.Setenv("CHARLY_TEST_HOOK_TOKEN", "tok-abc-123")
	meta := &spec.BoxMetadata{SecretAccept: []spec.EnvDependency{{Name: "CHARLY_TEST_HOOK_TOKEN"}}}
	got := resolveHookSecretEnv("img", "", meta)
	want := "CHARLY_TEST_HOOK_TOKEN=tok-abc-123"
	found := false
	for _, e := range got {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("want %q in hook env, got %v", want, got)
	}

	// An unresolved secret is omitted entirely (never a leaked empty NAME=).
	meta2 := &spec.BoxMetadata{SecretAccept: []spec.EnvDependency{{Name: "CHARLY_TEST_HOOK_UNSET_XYZZY"}}}
	if got := resolveHookSecretEnv("img", "", meta2); len(got) != 0 {
		t.Fatalf("unresolved secret must be omitted, got %v", got)
	}
}
