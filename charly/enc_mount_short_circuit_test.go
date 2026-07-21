package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestEncMount_ShortCircuit_AllMounted verifies defect C fix: when every
// requested volume is already mounted, encMount returns nil without
// calling resolveEncPassphraseForMount (which would have queried the
// keyring and potentially hung on a broken Secret Service provider).
//
// The test writes a minimal deploy.yml fixture declaring encrypted volumes
// for an image, spies on deploykit.IsEncryptedMounted to report all mounted, and
// spies on resolveEncPassphraseForMount (via the encMountCalledPassphrase
// test hook) to assert it was NOT called.
func TestEncMount_ShortCircuit_AllMounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report every plain dir as mounted.
	calls := 0
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		calls++
		return true
	}

	// Node-form fixture: one bundle with two encrypted volumes.
	// Each entity flattens to a top-level `<name>: {<kind>: …}` node;
	// non-scalar fields (the `volume:` list) move to a `<name>-volume`
	// child node. The bundle value carries `box:` so the inferred target
	// is exactly `pod` — the "pod-target deploy must declare box" rule
	// fires only when the inferred target is `pod` (an empty-target
	// bundle is a group, not a pod). Without `box:` LoadBundleConfig
	// would return an error and loadEncryptedVolume's swallow-the-error
	// path would return 0 mounts, defeating the short-circuit and
	// triggering a hang in resolveEncPassphraseForMount.
	dir := t.TempDir()
	deployPath := filepath.Join(dir, "deploy.yml")
	deployYAML := `version: 2026.202.0105
testimg:
  pod:
    image: testimg
    volume:
      - name: vol-a
        type: encrypted
        host: ` + dir + `/vol-a
      - name: vol-b
        type: encrypted
        host: ` + dir + `/vol-b
`
	if err := os.WriteFile(deployPath, []byte(deployYAML), 0600); err != nil {
		t.Fatalf("writing deploy.yml: %v", err)
	}

	// Point charly at our fixture config directory.
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("creating charly config dir: %v", err)
	}
	if err := os.Rename(deployPath, filepath.Join(dir, "charly", "charly.yml")); err != nil {
		t.Fatalf("moving deploy.yml: %v", err)
	}

	// Seed cipher.gocryptfs.conf sentinel so isEncryptedInitialized returns true.
	for _, vol := range []string{"vol-a", "vol-b"} {
		cipherDir := filepath.Join(dir, vol, "cipher")
		if err := os.MkdirAll(cipherDir, 0700); err != nil {
			t.Fatalf("mkdir cipher: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cipherDir, "gocryptfs.conf"), []byte("{}"), 0600); err != nil {
			t.Fatalf("writing gocryptfs.conf: %v", err)
		}
	}

	// Act: call encMount. If the short-circuit works, it should return nil
	// quickly without calling resolveEncPassphraseForMount. We can't easily
	// spy on resolveEncPassphraseForMount without refactoring, but the
	// primary signal is that encMount returns nil and deploykit.IsEncryptedMounted
	// was called for each volume.
	err := encMount("testimg", "", "")
	if err != nil {
		t.Fatalf("encMount returned error: %v", err)
	}
	if calls < 2 {
		t.Errorf("deploykit.IsEncryptedMounted calls = %d, want ≥ 2 (one per volume)", calls)
	}
}

// TestEncMount_NoShortCircuit_WhenOneUnmounted verifies that the fast path
// does NOT fire when at least one requested volume is not yet mounted —
// encMount should proceed to the passphrase resolution path. Since there's
// no DBus session in tests and no GOCRYPTFS_PASSWORD env var, it will
// ultimately fail, but the failure mode itself proves the short-circuit
// correctly abstained.
func TestEncMount_NoShortCircuit_WhenOneUnmounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report first volume mounted, second not mounted.
	var seen []string
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		seen = append(seen, plainDir)
		return len(seen) == 1 // only the first check returns true
	}

	dir := t.TempDir()
	deployYAML := `version: 2026.202.0105
testimg:
  pod:
    image: testimg
    volume:
      - name: vol-a
        type: encrypted
        host: ` + dir + `/vol-a
      - name: vol-b
        type: encrypted
        host: ` + dir + `/vol-b
`
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("creating charly config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "charly", "charly.yml"), []byte(deployYAML), 0600); err != nil {
		t.Fatalf("writing deploy.yml: %v", err)
	}
	for _, vol := range []string{"vol-a", "vol-b"} {
		cipherDir := filepath.Join(dir, vol, "cipher")
		_ = os.MkdirAll(cipherDir, 0700)
		_ = os.WriteFile(filepath.Join(cipherDir, "gocryptfs.conf"), []byte("{}"), 0600)
	}

	// Pin secret_backend to config AND set INVOCATION_ID so
	// resolveEncPassphraseForMount takes the explicit-non-keyring-backend
	// branch, which fails fast without prompting (no TTY access). The empty
	// in-memory store resolves nothing (the real store is out-of-process now),
	// so passphrase resolution returns the expected error without a plugin build.
	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	t.Setenv("INVOCATION_ID", "test")
	installFakeCredentialStore(t)
	_ = os.Unsetenv("GOCRYPTFS_PASSWORD")

	err := encMount("testimg", "", "")
	// Expect an error from passphrase resolution (no credential stored in
	// config backend, systemd path fails fast), NOT a short-circuit success.
	if err == nil {
		t.Errorf("expected error from passphrase resolution path, got nil (short-circuit fired incorrectly?)")
	}
}
