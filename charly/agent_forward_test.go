package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

func TestResolveSSHAgentForward(t *testing.T) {
	// Create a temporary socket path (just a regular file for testing)
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "agent.sock")
	if err := os.WriteFile(sock, nil, 0600); err != nil {
		t.Fatal(err)
	}

	// Set SSH_AUTH_SOCK to the temp path
	t.Setenv("SSH_AUTH_SOCK", sock)

	vol, env, ok := resolveSSHAgentForward()
	if !ok {
		t.Fatal("expected ok=true with valid SSH_AUTH_SOCK")
	}
	if !strings.HasPrefix(vol, sock+":") {
		t.Errorf("volume should start with socket path, got %q", vol)
	}
	if !strings.Contains(vol, "/run/host-ssh-auth.sock") {
		t.Errorf("volume should map to /run/host-ssh-auth.sock, got %q", vol)
	}
	if env != "SSH_AUTH_SOCK=/run/host-ssh-auth.sock" {
		t.Errorf("unexpected env: %q", env)
	}
}

func TestResolveSSHAgentForward_Missing(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	_, _, ok := resolveSSHAgentForward()
	if ok {
		t.Error("expected ok=false with empty SSH_AUTH_SOCK")
	}
}

func TestResolveSSHAgentForward_NonexistentSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/agent.sock")
	var ok bool
	stderr := captureStderr(t, func() {
		_, _, ok = resolveSSHAgentForward()
	})
	if ok {
		t.Error("expected ok=false with nonexistent socket path")
	}
	if !strings.Contains(stderr, "does not exist, skipping SSH agent forwarding") {
		t.Fatalf("stderr = %q, want missing-socket diagnostic", stderr)
	}
}

func TestResolveAgentForwarding_Disabled(t *testing.T) {
	rt := &kit.ResolvedRuntime{
		ForwardGpgAgent: false,
		ForwardSshAgent: false,
	}
	result := ResolveAgentForwarding(rt, nil, "/home/testuser")
	if len(result.Volumes) != 0 {
		t.Errorf("expected no volumes when forwarding disabled, got %v", result.Volumes)
	}
	if len(result.Env) != 0 {
		t.Errorf("expected no env when forwarding disabled, got %v", result.Env)
	}
}

func TestResolveAgentForwarding_DeployOverride(t *testing.T) {
	// Global: enabled. Deploy: disabled.
	rt := &kit.ResolvedRuntime{
		ForwardGpgAgent: true,
		ForwardSshAgent: true,
	}
	f := false
	deploy := &spec.BundleNode{
		ForwardGpgAgent: &f,
		ForwardSshAgent: &f,
	}

	// Even with SSH_AUTH_SOCK set, deploy override should suppress forwarding
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "agent.sock")
	if err := os.WriteFile(sock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", sock)

	result := ResolveAgentForwarding(rt, deploy, "/home/testuser")
	if len(result.Volumes) != 0 {
		t.Errorf("expected no volumes when deploy override disables forwarding, got %v", result.Volumes)
	}
	if len(result.Env) != 0 {
		t.Errorf("expected no env when deploy override disables forwarding, got %v", result.Env)
	}
}

func TestResolveGPGAgentForward_ContainerHome(t *testing.T) {
	// Test that the container socket path uses the provided home directory
	// We can't easily test the full function without a real socket,
	// but we can verify the path construction logic
	vol, ok := resolveGPGAgentForward("/root")
	if ok {
		if !strings.Contains(vol, "/root/.gnupg/S.gpg-agent") {
			t.Errorf("expected /root/.gnupg/S.gpg-agent in volume, got %q", vol)
		}
	}
	// ok=false is acceptable if no GPG agent is running on the test system
}
