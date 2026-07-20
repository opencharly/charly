package main

import (
	"errors"
	"testing"
)

// Host-side runtime plans must invoke the binary that started the check. In
// particular, they must not fall back to a potentially stale `charly` found on
// PATH (the R10 local-command failure this guards). stampCharlyBin /
// newRuntimeCheckVarResolver stamp CHARLY_BIN with the active executable so a
// plan step referencing ${CHARLY_BIN} re-enters through the active binary.
func TestRuntimeCheckVarResolver_UsesActiveCharlyBinary(t *testing.T) {
	orig := currentCharlyExecutable
	t.Cleanup(func() { currentCharlyExecutable = orig })
	currentCharlyExecutable = func() (string, error) { return "/worktree/bin/charly", nil }

	r := newRuntimeCheckVarResolver(map[string]string{"IMAGE": "test"})
	if got := r.Env["CHARLY_BIN"]; got != "/worktree/bin/charly" {
		t.Fatalf("CHARLY_BIN = %q, want active worktree binary", got)
	}
	if !r.HasRuntime {
		t.Fatal("runtime resolver lost runtime state")
	}
}

// CHARLY_BIN is deliberately never synthesized from PATH: an unavailable active
// executable leaves the variable unresolved rather than silently selecting an
// unrelated installed Charly.
func TestRuntimeCheckVarResolver_DoesNotFallBackToPath(t *testing.T) {
	orig := currentCharlyExecutable
	t.Cleanup(func() { currentCharlyExecutable = orig })
	currentCharlyExecutable = func() (string, error) { return "", errors.New("unavailable") }

	r := newRuntimeCheckVarResolver(nil)
	if _, ok := r.Env["CHARLY_BIN"]; ok {
		t.Fatal("CHARLY_BIN must remain unresolved when the active executable is unavailable")
	}
}

// stampCharlyBin is nil-safe and idempotent — the kit.ResolveCheckVarsRuntime call
// sites wrap their result through it.
func TestStampCharlyBin_NilSafe(t *testing.T) {
	if got := stampCharlyBin(nil); got != nil {
		t.Fatalf("stampCharlyBin(nil) = %v, want nil", got)
	}
}
