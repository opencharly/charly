package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// host_infra_test.go — relocated (in part) from charly/host_infra_test.go (#55 decoupling,
// Batch A): the ledger (install_ledger.go) + managed-block (profile.go) tests, all asserting
// kit functions directly with zero charly coupling. The distro-detection tests are Batch C's
// concern (plugin-vm) and the builder-run tests are Batch B's (plugin-build) — both left
// untouched in charly's host_infra_test.go by this batch.

// ---------------- install_ledger.go ----------------

func withTempLedger(t *testing.T) *kit.LedgerPaths {
	t.Helper()
	root := t.TempDir()
	return &kit.LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	paths := withTempLedger(t)
	rec := &kit.DeployRecord{
		DeployID:   "abc123",
		Image:      "fedora-coder",
		Target:     "host",
		Candy:      []string{"ripgrep", "uv"},
		DeployedAt: "2026-04-21T00:00:00Z",
	}
	if err := kit.WriteDeployRecord(paths, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := kit.ReadDeployRecord(paths, "abc123")
	if err != nil || got == nil {
		t.Fatalf("read: %v / %+v", err, got)
	}
	if got.Image != "fedora-coder" || len(got.Candy) != 2 {
		t.Errorf("round-trip broken: %+v", got)
	}
}

func TestLedgerRefcount(t *testing.T) {
	paths := withTempLedger(t)
	// Deploy A and B both include ripgrep.
	if err := kit.AddCandyDeployment(paths, "ripgrep", "deploy-A", nil); err != nil {
		t.Fatal(err)
	}
	if err := kit.AddCandyDeployment(paths, "ripgrep", "deploy-B", nil); err != nil {
		t.Fatal(err)
	}
	rec, _ := kit.ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 2 {
		t.Errorf("DeployedBy = %v, want 2 entries", rec.DeployedBy)
	}

	// Remove A — ripgrep stays.
	_, shouldRemove, err := kit.RemoveCandyDeployment(paths, "ripgrep", "deploy-A")
	if err != nil {
		t.Fatal(err)
	}
	if shouldRemove {
		t.Errorf("shouldRemove=true after removing one of two deployers")
	}
	rec, _ = kit.ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 1 || rec.DeployedBy[0] != "deploy-B" {
		t.Errorf("after decrement: %v", rec.DeployedBy)
	}

	// Remove B — ripgrep should fully teardown.
	_, shouldRemove, err = kit.RemoveCandyDeployment(paths, "ripgrep", "deploy-B")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldRemove {
		t.Errorf("shouldRemove=false when DeployedBy drains to empty")
	}
}

func TestLedgerFlock(t *testing.T) {
	paths := withTempLedger(t)
	lock, err := kit.AcquireLedgerLock(paths)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Can't easily test contention without a second process — at least
	// verify release succeeds and the lock file exists.
	if _, err := os.Stat(paths.LockFile); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

// ---------------- shell_profile.go ----------------

// TestRemoveManagedBlockAt proves the LOCAL per-candy teardown strip (the live path
// reverseRemoveManaged takes when runner==nil): a candy's fenced shell-snippet block is
// removed from an rc file the user also owns, leaving the user's own content intact.
func TestRemoveManagedBlockAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	begin, end := kit.MarkersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := kit.RemoveManagedBlockAt(path, "mycandy"); err != nil {
		t.Fatalf("RemoveManagedBlockAt: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "CANDY_VAR") || strings.Contains(string(got), begin) {
		t.Errorf("per-candy managed block not stripped:\n%s", got)
	}
	if !strings.Contains(string(got), "USER_VAR") || !strings.Contains(string(got), "alias ll") {
		t.Errorf("user content lost during strip:\n%s", got)
	}
}

// TestRenderManagedBlockStrip proves the REMOTE per-candy teardown strip (the live path
// reverseRemoveManaged takes when runner!=nil): the rendered POSIX-sh script, run through
// a real shell, strips exactly the candy's fence pair in place and preserves the rest.
func TestRenderManagedBlockStrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	begin, end := kit.MarkersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", kit.RenderManagedBlockStrip(path, "mycandy")).CombinedOutput(); err != nil {
		t.Fatalf("strip script failed: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "CANDY_VAR") || strings.Contains(string(got), begin) {
		t.Errorf("remote strip left the managed block:\n%s", got)
	}
	if !strings.Contains(string(got), "USER_VAR") || !strings.Contains(string(got), "alias ll") {
		t.Errorf("remote strip lost user content:\n%s", got)
	}
}
