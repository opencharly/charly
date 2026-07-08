package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPopulateSDKSubmodule_NoSDKRepoIsNoOp: a repo dir that declares no `sdk`
// submodule (no .gitmodules, or no submodule.sdk.path) is a clean no-op — a
// box/<distro> plugin repo has no sdk submodule and must not error.
func TestPopulateSDKSubmodule_NoSDKRepoIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := populateSDKSubmodule(dir); err != nil { // no .gitmodules at all
		t.Fatalf("no-.gitmodules dir must be a no-op, got %v", err)
	}
	// .gitmodules present but no sdk entry → still a no-op.
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"),
		[]byte("[submodule \"box/arch\"]\n\tpath = box/arch\n\turl = git@github.com:opencharly/distro-arch.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := populateSDKSubmodule(dir); err != nil {
		t.Fatalf("no sdk submodule declared must be a no-op, got %v", err)
	}
}

// TestGitClone_PopulatesSDKSubmodule is the end-to-end integration gate: a fresh
// GitClone of the charly repo populates the sdk submodule (go.work `use ./sdk`),
// so plugin builds from the @main cache resolve. Network-gated: skipped offline.
func TestGitClone_PopulatesSDKSubmodule(t *testing.T) {
	if testing.Short() {
		t.Skip("network integration test")
	}
	if exec.Command("git", "ls-remote", "https://github.com/opencharly/charly.git", "HEAD").Run() != nil {
		t.Skip("no network / github unreachable")
	}
	dir := filepath.Join(t.TempDir(), "clone")
	if err := GitClone("https://github.com/opencharly/charly.git", "main", "", dir); err != nil {
		t.Fatalf("GitClone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sdk", "go.mod")); err != nil {
		t.Fatalf("sdk submodule not populated (plugin builds would fail 'cannot load module ../../sdk'): %v", err)
	}
}

// TestDebugNotice_CarriesRepoOverride: the failed-bed inspect hint must carry
// the active CHARLY_REPO_OVERRIDE so `charly check live` reproduces the bed's
// LOCAL-checkout state (a bare command would resolve @main-published plugins —
// a different deployment).
func TestDebugNotice_CarriesRepoOverride(t *testing.T) {
	t.Setenv("CHARLY_REPO_OVERRIDE", "github.com/opencharly/charly=/home/x/charly")
	var b strings.Builder
	printDebugRetentionNotice(&b, "check-foo", BundleNode{Target: "pod"})
	out := b.String()
	if !strings.Contains(out, "CHARLY_REPO_OVERRIDE='github.com/opencharly/charly=/home/x/charly' charly check live check-foo") {
		t.Fatalf("pod hint must carry the override:\n%s", out)
	}
	// Without an override, the bare command (no empty prefix).
	t.Setenv("CHARLY_REPO_OVERRIDE", "")
	b.Reset()
	printDebugRetentionNotice(&b, "check-bar", BundleNode{Target: "vm", From: "vm-tpl"})
	if out := b.String(); !strings.Contains(out, "inspect: charly check live check-bar |") || strings.Contains(out, "CHARLY_REPO_OVERRIDE=''") {
		t.Fatalf("no-override hint must be the bare command:\n%s", out)
	}
}
