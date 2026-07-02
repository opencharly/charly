package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile_WriteOverwriteMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Containerfile")

	if err := atomicWriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Fatalf("read after first write: got %q", got)
	}
	// Overwrite atomically (the concurrent-shared-base case writes identical
	// content; here we prove a content change also lands cleanly).
	if err := atomicWriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "second" {
		t.Fatalf("read after overwrite: got %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: want 0600, got %o", info.Mode().Perm())
	}
	// No leftover temp files in the dir (rename consumed it).
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected only the target file, got %d entries", len(entries))
	}
}

func TestInstallDirAtomic_CreateThenSwap(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "_layers")

	// CREATE: final absent → plain rename installs tmp1.
	tmp1 := filepath.Join(root, ".tmp1")
	mustMkdirWith(t, tmp1, "candyA", "v1")
	if err := installDirAtomic(tmp1, final); err != nil {
		t.Fatalf("create install: %v", err)
	}
	if got := readFileIn(t, final, "candyA"); got != "v1" {
		t.Fatalf("after create: candyA = %q", got)
	}
	if _, err := os.Stat(tmp1); !os.IsNotExist(err) {
		t.Fatalf("tmp1 should be gone after create install")
	}

	// SWAP: final present → renameat2(RENAME_EXCHANGE) installs new content,
	// the old content (now under tmp2) is removed.
	tmp2 := filepath.Join(root, ".tmp2")
	mustMkdirWith(t, tmp2, "candyA", "v2")
	if err := installDirAtomic(tmp2, final); err != nil {
		t.Fatalf("swap install: %v", err)
	}
	if got := readFileIn(t, final, "candyA"); got != "v2" {
		t.Fatalf("after swap: candyA = %q (stale content survived the swap)", got)
	}
	if _, err := os.Stat(tmp2); !os.IsNotExist(err) {
		t.Fatalf("tmp2 (holding old content) should be removed after swap")
	}
}

func mustMkdirWith(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s/%s: %v", dir, name, err)
	}
}

func readFileIn(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s/%s: %v", dir, name, err)
	}
	return string(b)
}
