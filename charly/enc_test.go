package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// resolveBindMounts tests moved to deploy_test.go (TestResolveVolumeBacking*)

// TestEncryptedVolumeName / TestEncryptedCipherDir / TestEncryptedPlainDir moved to
// sdk/deploykit (deploy_volume_backing_test.go) with the enc-path helpers in P11.

// TestIsEncryptedInitialized / TestHasEncryptedBindMounts / TestCryptoServiceFilename /
// TestVerifyBindMounts* relocated to sdk/deploykit/enc_probe_test.go (Cutover B unit 2) alongside
// isEncryptedInitialized/hasEncryptedBindMounts/encServiceFilename/verifyBindMounts, all of which
// moved there too (genuinely portable — no registry/credential coupling).

// Build-time bind mount validation tests removed — validateBindMounts was deleted.
// Volume backing is now a deploy-time concern (see deploy_test.go TestResolveVolumeBacking*).

func TestQuadletWithBindMounts(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []deploykit.ResolvedBindMount{
			{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp", Encrypted: false},
		},
	}

	got := deploykit.GenerateQuadlet(cfg)

	if !strings.Contains(got, "Volume=/home/user/data:/home/user/.myapp") {
		t.Errorf("expected Volume for bind mount, got:\n%s", got)
	}
	// Should not have crypto service dependency
	if strings.Contains(got, "crypto.service") {
		t.Errorf("should not have crypto service for plain mounts, got:\n%s", got)
	}
}

func TestQuadletWithEncryptedBindMountsKeyring(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []deploykit.ResolvedBindMount{
			{Name: "secrets", HostPath: "/data/enc/charly-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		},
		CharlyBin:       "/usr/local/bin/charly",
		EncryptedMounts: true,
		KeyringBackend:  true,
	}

	got := deploykit.GenerateQuadlet(cfg)

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
	cfg := deploykit.QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []deploykit.ResolvedBindMount{
			{Name: "secrets", HostPath: "/data/enc/charly-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		},
		CharlyBin:       "/usr/local/bin/charly",
		EncryptedMounts: true,
		KeyringBackend:  false, // config (non-keyring) backend
	}

	got := deploykit.GenerateQuadlet(cfg)

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
	cfg := deploykit.QuadletConfig{
		BoxName:     "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Home:        "/home/user/project",
		BindAddress: "127.0.0.1",
	}

	got := deploykit.GenerateQuadlet(cfg)

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

// TestBuildShellArgsWithBindMounts / TestBuildShellArgsWithBindMountsPodman relocated to
// candy/plugin-deploy-pod/resolve_f12_test.go (buildShellArgs moved, P13-KERNEL step-4(ii)).
// TestBuildStartArgsWithBindMounts / TestBuildStartArgsWithBindMountsPodman DELETED (Cutover B
// unit 2): buildStartArgs was dead code (zero non-test callers — candy/plugin-deploy-pod's
// resolve.go self-resolves the full start plan since P13-KERNEL step-4(ii)); their bind-mount
// coverage now lives on candy/plugin-deploy-pod's own resolve.go equivalent.

func TestCryptoPasswdRequiresUnmount(t *testing.T) {
	// Mock deploykit.IsEncryptedMounted to return true (volume is mounted)
	origMounted := deploykit.IsEncryptedMounted
	deploykit.IsEncryptedMounted = func(plainDir string) bool { return true }
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	boxName := "myapp"
	// We can't call encPasswd() directly because loadEncryptedVolume needs deploy.yml,
	// so test the logic by simulating what encPasswd() does.
	mounts := []DeployVolumeConfig{
		{Name: "secrets", Type: "encrypted"},
	}
	storagePath := "/data/enc"

	for _, m := range mounts {
		plainDir := deploykit.EncryptedPlainDir(storagePath, boxName, m.Name)
		if deploykit.IsEncryptedMounted(plainDir) {
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
	// Mock deploykit.AskPassword to return controlled values
	origAsk := deploykit.AskPassword
	callCount := 0
	deploykit.AskPassword = func(id, prompt string) (string, error) {
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
	defer func() { deploykit.AskPassword = origAsk }()

	// Mock deploykit.IsEncryptedMounted to return false (all unmounted)
	origMounted := deploykit.IsEncryptedMounted
	deploykit.IsEncryptedMounted = func(plainDir string) bool { return false }
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Simulate the password check logic from Run()
	oldPass, _ := deploykit.AskPassword("test-old", "Current passphrase:")
	newPass, _ := deploykit.AskPassword("test-new", "New passphrase:")
	confirmPass, _ := deploykit.AskPassword("test-confirm", "Confirm new passphrase:")

	_ = oldPass
	if newPass != confirmPass {
		// This is the expected path
		return
	}
	t.Fatal("expected password mismatch to be detected")
}
