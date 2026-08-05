package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// credential_cleartext_warning_test.go — coverage for the cleartext-storage warning.
//
// Half of this file asserts the warning FIRES. The other half asserts it stays SILENT on the
// operations that do not put a new secret in the clear, which is the more important half: a
// warning that also fires on an index write or on the migration that removes plaintext is a
// warning operators learn to skip, and then it protects nobody.

// captureStderrDuring runs fn with os.Stderr redirected to a pipe and returns what was written.
// The warning writes to os.Stderr directly, in common with every other diagnostic in this
// package, so the test redirects the real thing rather than adding a production-side seam whose
// only purpose would be testability.
func captureStderrDuring(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()

	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// withIsolatedConfig points the store at a temp config.yml and resets the once-per-process latch
// so each test observes the warning's first-fire behaviour independently.
func withIsolatedConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	RuntimeConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { RuntimeConfigPath = defaultRuntimeConfigPath })
	cleartextWarnOnce = sync.Once{}
	t.Cleanup(func() { cleartextWarnOnce = sync.Once{} })
	return path
}

// TestCleartextWarning_FiresOnSet is the primary assertion: storing a secret through the
// config-file store says so, names the credential and names the file the operator now has to
// protect.
func TestCleartextWarning_FiresOnSet(t *testing.T) {
	path := withIsolatedConfig(t)
	store := &ConfigFileStore{}

	out := captureStderrDuring(t, func() {
		if err := store.Set("charly/enc", "myapp", "s3cret"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})

	if !strings.Contains(out, "CLEARTEXT") {
		t.Errorf("warning must say the secret is in cleartext, got:\n%s", out)
	}
	if !strings.Contains(out, "charly/enc/myapp") {
		t.Errorf("warning must name the credential stored, got:\n%s", out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("warning must name the file the operator has to protect (%s), got:\n%s", path, out)
	}
	// The remedy has to be actionable, not just an admonition.
	if !strings.Contains(out, "Secret Service") {
		t.Errorf("warning must say how to store secrets encrypted instead, got:\n%s", out)
	}
	// And the secret itself must never be echoed while warning about its exposure.
	if strings.Contains(out, "s3cret") {
		t.Errorf("warning must NOT echo the secret value, got:\n%s", out)
	}
}

// TestCleartextWarning_OncePerProcess: a single `charly config` run stores several secrets.
// Repeating the paragraph for each is what teaches operators to scroll past it.
func TestCleartextWarning_OncePerProcess(t *testing.T) {
	withIsolatedConfig(t)
	store := &ConfigFileStore{}

	out := captureStderrDuring(t, func() {
		for _, k := range []string{"one", "two", "three"} {
			if err := store.Set("charly/secret", k, "v"); err != nil {
				t.Fatalf("Set(%s): %v", k, err)
			}
		}
	})

	if got := strings.Count(out, "CLEARTEXT"); got != 1 {
		t.Errorf("warning count = %d, want exactly 1 for three stores, got:\n%s", got, out)
	}
}

// TestCleartextWarning_SilentOnNonWrites guards the false-positive half. Reading, listing and
// deleting put no new secret in the clear, so none of them may warn.
func TestCleartextWarning_SilentOnNonWrites(t *testing.T) {
	withIsolatedConfig(t)
	store := &ConfigFileStore{}

	// Seed a credential with the latch reset afterwards, so the seeding write's own warning
	// does not leak into the assertion below.
	if err := store.Set(CredServiceVNC, "seeded", "v"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cleartextWarnOnce = sync.Once{}

	out := captureStderrDuring(t, func() {
		if _, err := store.Get(CredServiceVNC, "seeded"); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if _, err := store.List(CredServiceVNC); err != nil {
			t.Fatalf("List: %v", err)
		}
		if err := store.Delete(CredServiceVNC, "seeded"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	if strings.Contains(out, "CLEARTEXT") {
		t.Errorf("Get/List/Delete write no secret and must NOT warn, got:\n%s", out)
	}
}

// TestCleartextWarning_SilentOnKeyringIndexWrite guards the subtlest false positive. The keyring
// shadow index is written to the SAME config.yml by the same SaveRuntimeConfig, but it carries
// service/key NAMES and no values — so a warning keyed on "config.yml was written" rather than on
// "a secret went in unencrypted" would fire here, on the keyring path, which is the encrypted one.
func TestCleartextWarning_SilentOnKeyringIndexWrite(t *testing.T) {
	withIsolatedConfig(t)

	out := captureStderrDuring(t, func() {
		if err := addKeyringIndex("charly/enc", "myapp"); err != nil {
			t.Fatalf("addKeyringIndex: %v", err)
		}
		if err := removeKeyringIndex("charly/enc", "myapp"); err != nil {
			t.Fatalf("removeKeyringIndex: %v", err)
		}
	})

	if strings.Contains(out, "CLEARTEXT") {
		t.Errorf("the keyring shadow index stores names, not values, and must NOT warn, got:\n%s", out)
	}
}

// TestCleartextWarning_NotOnFailedWrite: the warning asserts a secret is now on disk. If the save
// failed, that claim is false — an unwritable config path must produce the error, not the warning.
func TestCleartextWarning_NotOnFailedWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions would not block the write")
	}
	// The LOAD must succeed and only the SAVE fail, or this test passes for the wrong reason:
	// ConfigFileStore.Set bails at its first error check, so a path that breaks LoadRuntimeConfig
	// never reaches the save at all and the ordering under test is never exercised. A read-only
	// PARENT gives exactly the split — a missing file loads as an empty config (IsNotExist is not
	// an error there), while WriteFile into the unwritable directory fails.
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let TempDir cleanup remove it
	cfgPath := filepath.Join(dir, "config.yml")
	RuntimeConfigPath = func() (string, error) { return cfgPath, nil }
	t.Cleanup(func() { RuntimeConfigPath = defaultRuntimeConfigPath })
	cleartextWarnOnce = sync.Once{}
	t.Cleanup(func() { cleartextWarnOnce = sync.Once{} })

	// Assert the precondition rather than assuming it: the load must genuinely succeed, so that
	// the failure this test observes is the SAVE.
	if _, err := LoadRuntimeConfig(); err != nil {
		t.Fatalf("precondition: load must succeed so the save is what fails, got: %v", err)
	}

	store := &ConfigFileStore{}
	var setErr error
	out := captureStderrDuring(t, func() {
		setErr = store.Set("charly/enc", "myapp", "s3cret")
	})

	if setErr == nil {
		t.Fatal("Set must fail when the config file cannot be written")
	}
	if strings.Contains(out, "CLEARTEXT") {
		t.Errorf("must NOT claim a secret was stored when the write failed, got:\n%s", out)
	}
}

// TestNoBrokenSecretBackendVerb is a regression gate on the fold that accompanied this warning.
//
// Seven messages told the operator to run `charly config set secret_backend …`, which silently
// does nothing: `config` is the DEPLOY verb, so `set` is parsed as a box name and the operator
// lands in `config setup` usage. The working verb is `charly settings set`. That was merely
// wrong before; printed three lines above a warning about cleartext secrets, it became an
// instruction to ignore the warning — using a command that would not have worked anyway.
//
// Asserted over the package's own source because these strings are scattered across error paths
// that no unit test drives, so a behavioural test would leave most of them uncovered.
func TestNoBrokenSecretBackendVerb(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		checked++
		if strings.Contains(string(body), "charly config set ") {
			t.Errorf("%s tells the operator to run `charly config set …`, which does nothing — "+
				"the runtime-config verb is `charly settings set`", name)
		}
	}
	// Guard the guard: if the walk stops finding sources, this test passes vacuously.
	if checked < 5 {
		t.Fatalf("only %d source files scanned — the check is not looking at the package", checked)
	}
}
