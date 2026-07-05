package main

import "testing"

// TestMarkRepoAutoMigrating_GuardsReentry verifies the remote-cache
// auto-migration cycle-guard. A migration that re-enters LoadUnified resolves
// @github refs and re-enters EnsureRepoDownloaded → the command:migrate Invoke.
// With a self/mutual import (the main ↔ cachyos cycle) — and especially right
// after a LatestSchemaVersion bump, when every cache reads as behind-head — that
// recursed without bound (observed: 65 GB RSS before the fix). markRepoAutoMigrating
// must admit each cache path for migration exactly once per process so the cycle terminates.
func TestMarkRepoAutoMigrating_GuardsReentry(t *testing.T) {
	const a, b = "/tmp/charly-test-repo-A", "/tmp/charly-test-repo-B"
	autoMigratedReposMu.Lock()
	delete(autoMigratedRepos, a)
	delete(autoMigratedRepos, b)
	autoMigratedReposMu.Unlock()

	if !markRepoAutoMigrating(a) {
		t.Fatal("first call for repo-A must return true (admit migration)")
	}
	if markRepoAutoMigrating(a) {
		t.Fatal("second call for repo-A must return false (guard re-entry) — without this the auto-migration recurses without bound")
	}
	if markRepoAutoMigrating(a) {
		t.Fatal("third call for repo-A must still return false (idempotent guard)")
	}
	if !markRepoAutoMigrating(b) {
		t.Fatal("first call for a DIFFERENT repo must return true (guard is per-path, not global)")
	}
}
