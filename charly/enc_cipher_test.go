package main

// enc_cipher_test.go — TestCipherPopulatedPlainEmpty (it exercises
// cipherPopulatedPlainEmpty in enc.go, package-main — a core gocryptfs-safety helper).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCipherPopulatedPlainEmpty(t *testing.T) {
	mk := func(t *testing.T, cipherFiles, plainFiles []string) (cipher, plain string) {
		t.Helper()
		dir := t.TempDir()
		cipher = filepath.Join(dir, "cipher")
		plain = filepath.Join(dir, "plain")
		if err := os.MkdirAll(cipher, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(plain, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range cipherFiles {
			if err := os.WriteFile(filepath.Join(cipher, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, f := range plainFiles {
			if err := os.WriteFile(filepath.Join(plain, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return cipher, plain
	}

	t.Run("dangerous: cipher populated, plain empty", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "gocryptfs.diriv", "AbCdEfGh", "QrStUvWx"},
			nil,
		)
		if !cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected true (cipher has user data, plain empty)")
		}
	})

	t.Run("benign: cipher metadata-only, plain empty (fresh init)", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "gocryptfs.diriv"},
			nil,
		)
		if cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected false (cipher only has metadata files)")
		}
	})

	t.Run("benign: plain non-empty (FUSE was mounted then containerwrote, OR plain has stale plaintext drift)", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "AbCdEfGh"},
			[]string{"some-file"},
		)
		if cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected false (plain not empty — different failure class)")
		}
	})

	t.Run("missing cipher dir", func(t *testing.T) {
		dir := t.TempDir()
		plain := filepath.Join(dir, "plain")
		if err := os.MkdirAll(plain, 0o700); err != nil {
			t.Fatal(err)
		}
		if cipherPopulatedPlainEmpty(filepath.Join(dir, "missing-cipher"), plain) {
			t.Error("expected false (cipher dir does not exist)")
		}
	})

	t.Run("missing plain dir", func(t *testing.T) {
		dir := t.TempDir()
		cipher := filepath.Join(dir, "cipher")
		if err := os.MkdirAll(cipher, 0o700); err != nil {
			t.Fatal(err)
		}
		if cipherPopulatedPlainEmpty(cipher, filepath.Join(dir, "missing-plain")) {
			t.Error("expected false (plain dir does not exist)")
		}
	})
}
