package main

// testwrite_testhelper_test.go — shared test-file-writing helpers for package main.
//
// `mustWrite` used to live in plugin_build_stamp_test.go, which was DELETED WHOLE with the loader's
// content-stamp sidecar (the phase-1 store cutover, charly#896). It is not a stamp concern — it is
// the one write-with-assertion helper several unrelated test files reach for (refs_sweep_test.go
// among them), so deleting the file that happened to carry it left a package that did not compile.
// It lives here now, on its own, so no future file's deletion can take it down with it.
//
// Its contract is unchanged: a test fixture write that FAILS SILENTLY is worse than a failing test
// (refs_sweep_test.go's own comment names the failure mode: a write that silently failed would make
// the walker terminate on an empty tree and pass for the wrong reason), so every fixture write goes
// through this and a write error is fatal.

import (
	"os"
	"testing"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
