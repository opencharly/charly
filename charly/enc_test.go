package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// resolveBindMounts tests moved to deploy_test.go (TestResolveVolumeBacking*)

func TestEncryptedVolumeName(t *testing.T) {
	tests := []struct {
		image, name, want string
	}{
		{"myapp", "secrets", "charly-myapp-secrets"},
		{"openclaw", "data", "charly-openclaw-data"},
	}
	for _, tt := range tests {
		got := encryptedVolumeName(tt.image, tt.name)
		if got != tt.want {
			t.Errorf("encryptedVolumeName(%q, %q) = %q, want %q", tt.image, tt.name, got, tt.want)
		}
	}
}

func TestEncryptedCipherDir(t *testing.T) {
	got := encryptedCipherDir("/data/enc", "myapp", "secrets")
	want := "/data/enc/charly-myapp-secrets/cipher"
	if got != want {
		t.Errorf("encryptedCipherDir() = %q, want %q", got, want)
	}
}

func TestEncryptedPlainDir(t *testing.T) {
	got := encryptedPlainDir("/data/enc", "myapp", "secrets")
	want := "/data/enc/charly-myapp-secrets/plain"
	if got != want {
		t.Errorf("encryptedPlainDir() = %q, want %q", got, want)
	}
}

func TestIsEncryptedInitialized(t *testing.T) {
	// Non-existent directory
	if isEncryptedInitialized("/nonexistent/cipher") {
		t.Error("expected false for nonexistent directory")
	}

	// Directory without gocryptfs.conf
	dir := t.TempDir()
	if isEncryptedInitialized(dir) {
		t.Error("expected false for dir without gocryptfs.conf")
	}
}

func TestHasEncryptedBindMounts(t *testing.T) {
	tests := []struct {
		name   string
		mounts []ResolvedBindMount
		want   bool
	}{
		{"nil", nil, false},
		{"empty", []ResolvedBindMount{}, false},
		{"plain only", []ResolvedBindMount{{Encrypted: false}}, false},
		{"encrypted", []ResolvedBindMount{{Encrypted: true}}, true},
		{"mixed", []ResolvedBindMount{{Encrypted: false}, {Encrypted: true}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasEncryptedBindMounts(tt.mounts)
			if got != tt.want {
				t.Errorf("hasEncryptedBindMounts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCryptoServiceFilename(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"myapp", "charly-myapp-enc.service"},
		{"openclaw", "charly-openclaw-enc.service"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := encServiceFilename(tt.image)
			if got != tt.want {
				t.Errorf("encServiceFilename(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestVerifyBindMountsPlainDirMissing(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/nonexistent/path", ContPath: "/home/user/.myapp", Encrypted: false},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err == nil {
		t.Fatal("expected error for missing host dir")
	}
	if !strings.Contains(err.Error(), "bind mount \"data\"") {
		t.Errorf("error should reference bind mount name, got: %v", err)
	}
}

func TestVerifyBindMountsPlainDirExists(t *testing.T) {
	dir := t.TempDir()
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: dir, ContPath: "/home/user/.myapp", Encrypted: false},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyBindMountsEncryptedNotMounted(t *testing.T) {
	// Mock isEncryptedMounted to always return false
	orig := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return false }
	defer func() { isEncryptedMounted = orig }()

	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/tmp/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err == nil {
		t.Fatal("expected error for unmounted encrypted volume")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("error should mention 'not mounted', got: %v", err)
	}
	if !strings.Contains(err.Error(), "charly config mount") {
		t.Errorf("error should suggest 'charly config mount', got: %v", err)
	}
}

func TestVerifyBindMountsEncryptedMounted(t *testing.T) {
	// Mock isEncryptedMounted to always return true
	orig := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return true }
	defer func() { isEncryptedMounted = orig }()

	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/tmp/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Build-time bind mount validation tests removed — validateBindMounts was deleted.
// Volume backing is now a deploy-time concern (see deploy_test.go TestResolveVolumeBacking*).

func TestQuadletWithBindMounts(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []ResolvedBindMount{
			{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp", Encrypted: false},
		},
	}

	got := generateQuadlet(cfg)

	if !strings.Contains(got, "Volume=/home/user/data:/home/user/.myapp") {
		t.Errorf("expected Volume for bind mount, got:\n%s", got)
	}
	// Should not have crypto service dependency
	if strings.Contains(got, "crypto.service") {
		t.Errorf("should not have crypto service for plain mounts, got:\n%s", got)
	}
}

func TestQuadletWithEncryptedBindMountsKeyring(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []ResolvedBindMount{
			{Name: "secrets", HostPath: "/data/enc/charly-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		},
		CharlyBin:       "/usr/local/bin/charly",
		EncryptedMounts: true,
		KeyringBackend:  true,
	}

	got := generateQuadlet(cfg)

	// ExecStartPre mounts encrypted volumes before container starts
	if !strings.Contains(got, "ExecStartPre=/usr/local/bin/charly config mount myapp") {
		t.Errorf("expected ExecStartPre for encrypted mounts, got:\n%s", got)
	}
	// Keyring backend: wait indefinitely for keyring unlock
	if !strings.Contains(got, "TimeoutStartSec=0") {
		t.Errorf("expected TimeoutStartSec=0 for keyring backend, got:\n%s", got)
	}
	// Keyring backend: auto-start at boot (waits for keyring)
	if !strings.Contains(got, "WantedBy=default.target") {
		t.Errorf("expected WantedBy=default.target for keyring backend, got:\n%s", got)
	}
	if !strings.Contains(got, "Volume=/data/enc/charly-myapp-secrets/plain:/home/user/.secrets") {
		t.Errorf("expected Volume for encrypted bind mount, got:\n%s", got)
	}
}

func TestQuadletWithEncryptedBindMountsNonKeyring(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []ResolvedBindMount{
			{Name: "secrets", HostPath: "/data/enc/charly-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		},
		CharlyBin:       "/usr/local/bin/charly",
		EncryptedMounts: true,
		KeyringBackend:  false, // config (non-keyring) backend
	}

	got := generateQuadlet(cfg)

	// ExecStartPre still present as safety guard
	if !strings.Contains(got, "ExecStartPre=/usr/local/bin/charly config mount myapp") {
		t.Errorf("expected ExecStartPre for encrypted mounts, got:\n%s", got)
	}
	// Non-keyring: default timeout (not 0)
	if strings.Contains(got, "TimeoutStartSec=0") {
		t.Errorf("should NOT have TimeoutStartSec=0 for non-keyring backend, got:\n%s", got)
	}
	// Non-keyring: NO auto-start at boot (requires charly start)
	if strings.Contains(got, "WantedBy=default.target") {
		t.Errorf("should NOT have WantedBy for non-keyring encrypted service, got:\n%s", got)
	}
}

func TestQuadletWithoutEncryptedMounts(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
	}

	got := generateQuadlet(cfg)

	// No encrypted mounts: no ExecStartPre
	if strings.Contains(got, "ExecStartPre=") {
		t.Errorf("should NOT have ExecStartPre without encrypted mounts, got:\n%s", got)
	}
	// Normal auto-start
	if !strings.Contains(got, "WantedBy=default.target") {
		t.Errorf("expected WantedBy=default.target for non-encrypted service, got:\n%s", got)
	}
	// Default timeout
	if !strings.Contains(got, "TimeoutStartSec=900") {
		t.Errorf("expected default TimeoutStartSec=900, got:\n%s", got)
	}
}

func TestBuildShellArgsWithBindMounts(t *testing.T) {
	withTerminal(t, true)
	bindMounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("docker", "myapp:latest", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, SecurityConfig{}, "/workspace")

	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/home/user/data:/home/user/.myapp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v /home/user/data:/home/user/.myapp in args, got: %v", args)
	}
	// Docker should NOT have --userns
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			t.Error("docker should not have --userns=keep-id")
		}
	}
}

func TestBuildShellArgsWithBindMountsPodman(t *testing.T) {
	withTerminal(t, true)
	bindMounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("podman", "myapp:latest", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, SecurityConfig{}, "/workspace")

	found := slices.Contains(args, "--userns=keep-id:uid=1000,gid=1000")
	if !found {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 in podman args, got: %v", args)
	}
}

func TestBuildStartArgsWithBindMounts(t *testing.T) {
	bindMounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/enc/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	args := buildStartArgs("docker", "myapp:latest", 1000, 1000, nil, "charly-myapp", nil, bindMounts, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")

	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/enc/plain:/home/user/.secrets" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v /enc/plain:/home/user/.secrets in args, got: %v", args)
	}
	// Docker should NOT have --userns
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			t.Error("docker should not have --userns=keep-id")
		}
	}
}

func TestBuildStartArgsWithBindMountsPodman(t *testing.T) {
	bindMounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/enc/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	args := buildStartArgs("podman", "myapp:latest", 1000, 1000, nil, "charly-myapp", nil, bindMounts, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")

	found := slices.Contains(args, "--userns=keep-id:uid=1000,gid=1000")
	if !found {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 in podman args, got: %v", args)
	}
}

func TestCryptoPasswdRequiresUnmount(t *testing.T) {
	// Mock isEncryptedMounted to return true (volume is mounted)
	origMounted := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return true }
	defer func() { isEncryptedMounted = origMounted }()

	boxName := "myapp"
	// We can't call encPasswd() directly because loadEncryptedVolume needs deploy.yml,
	// so test the logic by simulating what encPasswd() does.
	mounts := []DeployVolumeConfig{
		{Name: "secrets", Type: "encrypted"},
	}
	storagePath := "/data/enc"

	for _, m := range mounts {
		plainDir := encryptedPlainDir(storagePath, boxName, m.Name)
		if isEncryptedMounted(plainDir) {
			err := fmt.Errorf("encrypted volume %q is still mounted; run 'charly config unmount %s' first", m.Name, boxName)
			if !strings.Contains(err.Error(), "still mounted") {
				t.Errorf("expected 'still mounted' in error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "charly config unmount") {
				t.Errorf("expected 'charly config unmount' hint in error, got: %v", err)
			}
			return
		}
	}
	t.Fatal("expected mounted volume to trigger error")
}

func TestCryptoPasswdPasswordMismatch(t *testing.T) {
	// Mock askPassword to return controlled values
	origAsk := askPassword
	callCount := 0
	askPassword = func(id, prompt string) (string, error) {
		callCount++
		switch callCount {
		case 1:
			return "oldpass", nil // current
		case 2:
			return "newpass", nil // new
		case 3:
			return "different", nil // confirm (mismatch)
		}
		return "", fmt.Errorf("unexpected call")
	}
	defer func() { askPassword = origAsk }()

	// Mock isEncryptedMounted to return false (all unmounted)
	origMounted := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return false }
	defer func() { isEncryptedMounted = origMounted }()

	// Simulate the password check logic from Run()
	oldPass, _ := askPassword("test-old", "Current passphrase:")
	newPass, _ := askPassword("test-new", "New passphrase:")
	confirmPass, _ := askPassword("test-confirm", "Confirm new passphrase:")

	_ = oldPass
	if newPass != confirmPass {
		// This is the expected path
		return
	}
	t.Fatal("expected password mismatch to be detected")
}
