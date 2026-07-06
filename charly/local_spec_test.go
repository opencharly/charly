package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestLoadUnified_LocalMap_Inline verifies that an charly.yml with
// an inline `local:` map round-trips into UnifiedFile.Local with the
// expected fields.
func TestLoadUnified_LocalMap_Inline(t *testing.T) {
	dir := t.TempDir()
	src := `version: 2026.186.2323
dev-workstation:
  local:
    description: Dev workstation
    candy: [ripgrep, direnv]
    install_opts: {with_service: false, allow_repo_changes: true}
    env:
      EDITOR: vim
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write charly.yml: %v", err)
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !ok || uf == nil {
		t.Fatal("expected unified file to load")
	}
	spec, exists := uf.Local["dev-workstation"]
	if !exists {
		t.Fatalf("expected dev-workstation in uf.Local; got %+v", uf.Local)
	}
	if got := spec.Candy; len(got) != 2 || got[0] != "ripgrep" || got[1] != "direnv" {
		t.Errorf("unexpected layers: %v", got)
	}
	if spec.InstallOpts == nil || spec.InstallOpts.WithServices || !spec.InstallOpts.AllowRepoChanges {
		t.Errorf("install_opts merge failed: %+v", spec.InstallOpts)
	}
	if len(spec.Env) != 1 || spec.Env["EDITOR"] != "vim" {
		t.Errorf("unexpected env: %v", spec.Env)
	}
	if spec.Description != "Dev workstation" {
		t.Errorf("description not preserved: %q", spec.Description)
	}
}

// TestLoadUnified_RejectLegacyTargetHost asserts the loader hard-errors
// on a deployment that still uses the legacy target:host spelling.
func TestLoadUnified_RejectLegacyTargetHost(t *testing.T) {
	dir := t.TempDir()
	src := `version: 2026.186.2323
deploy:
  my-laptop:
    target: host
    add_layers: [ripgrep]
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("expected hard load error for target: host")
	}
	if msg := err.Error(); !containsStr(msg, "no kind discriminator") {
		t.Errorf("legacy `deploy:` kind-keyed shape should be hard-rejected, got: %v", err)
	}
}

// TestVmSshAlias confirms the deterministic alias derivation.
func TestVmSshAlias(t *testing.T) {
	if got := VmSshAlias("arch-vm"); got != "charly-arch-vm" {
		t.Errorf("VmSshAlias(arch-vm) = %q, want charly-arch-vm", got)
	}
	if got := VmSshAlias("k3s-vm"); got != "charly-k3s-vm" {
		t.Errorf("VmSshAlias(k3s-vm) = %q, want charly-k3s-vm", got)
	}
}

// TestSshExecutor_NoCredentials confirms that SSHExecutor's argv
// contains zero credential overrides — no -i, no StrictHostKeyChecking,
// no UserKnownHostsFile. ssh(1) reads ~/.ssh/config + ssh-agent.
func TestSshExecutor_NoCredentials(t *testing.T) {
	e := &SSHExecutor{Host: "charly-arch-vm"}
	args := e.sshBaseArgs()
	for _, a := range args {
		if a == "-i" {
			t.Errorf("unexpected -i flag in sshBaseArgs: %v", args)
		}
		if containsStr(a, "StrictHostKeyChecking") {
			t.Errorf("unexpected StrictHostKeyChecking in sshBaseArgs: %v", args)
		}
		if containsStr(a, "UserKnownHostsFile") {
			t.Errorf("unexpected UserKnownHostsFile in sshBaseArgs: %v", args)
		}
	}
	// And the destination must be the bare alias when User+Port are unset.
	if got := args[len(args)-1]; got != "charly-arch-vm" {
		t.Errorf("destination = %q, want charly-arch-vm", got)
	}
}

// TestSshExecutor_WithUserPortArgs confirms the argv when caller
// pre-parsed user/port from a destination string.
func TestSshExecutor_WithUserPortArgs(t *testing.T) {
	e := &SSHExecutor{
		User: "ubuntu",
		Host: "ci-runner-3.lan",
		Port: 2222,
		Args: []string{"-o", "ProxyJump=bastion"},
	}
	args := e.sshBaseArgs()
	wantParts := []string{"-p", "2222", "-o", "ProxyJump=bastion", "ubuntu@ci-runner-3.lan"}
	for _, want := range wantParts {
		found := slices.Contains(args, want)
		if !found {
			t.Errorf("missing %q in argv: %v", want, args)
		}
	}
	for _, a := range args {
		if a == "-i" {
			t.Errorf("unexpected -i flag: %v", args)
		}
	}
}

// containsStr is a local helper to avoid colliding with the existing
// `contains` helper in registry.go.
func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
