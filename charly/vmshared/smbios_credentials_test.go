package vmshared

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestKeyToRootTmpfilesD(t *testing.T) {
	pubkey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC..."
	result := KeyToRootTmpfilesD(pubkey)

	// Must create /root/.ssh directory
	if !strings.Contains(result, "d /root/.ssh 0750 - - -") {
		t.Errorf("missing /root/.ssh directory line, got: %s", result)
	}

	// Must create authorized_keys with base64-encoded key
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	expected := "f+~ /root/.ssh/authorized_keys 700 - - - " + b64Key
	if !strings.Contains(result, expected) {
		t.Errorf("missing authorized_keys line\nwant: %s\ngot:  %s", expected, result)
	}
}

func TestSmbiosCredForRootSSH(t *testing.T) {
	pubkey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC..."
	result := SmbiosCredForRootSSH(pubkey)

	// Must start with the credential prefix
	prefix := "io.systemd.credential.binary:tmpfiles.extra="
	if !strings.HasPrefix(result, prefix) {
		t.Errorf("wrong prefix, got: %s", result)
	}

	// Decode and verify the tmpfiles.d content round-trips
	b64Part := strings.TrimPrefix(result, prefix)
	decoded, err := base64.StdEncoding.DecodeString(b64Part)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}

	expected := KeyToRootTmpfilesD(pubkey)
	if string(decoded) != expected {
		t.Errorf("round-trip mismatch\nwant: %q\ngot:  %q", expected, string(decoded))
	}
}

// TestKeyToUserTmpfilesD_SmbiosPriority asserts the SMBIOS credential gives the
// per-VM key a root-owned, cloud-init-proof home (/etc/ssh/authorized_keys.d/<user>)
// plus the sshd_config.d drop-in that makes sshd honor it, while still writing the
// user's own authorized_keys as a fallback. This is what guarantees the SMBIOS key
// stays authoritative even when cloud-init later rewrites ~/.ssh/authorized_keys.
func TestKeyToUserTmpfilesD_SmbiosPriority(t *testing.T) {
	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEXAMPLEKEYDATA user@host"
	result := KeyToUserTmpfilesD("cachy", "/home/cachy", pubkey)
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))

	cfg := "AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/authorized_keys.d/%u\n"
	b64Cfg := base64.StdEncoding.EncodeToString([]byte(cfg))
	if !strings.Contains(result, "f+~ /etc/ssh/sshd_config.d/00-charly-smbios-ssh.conf 0644 - - - "+b64Cfg) {
		t.Errorf("missing sshd_config.d AuthorizedKeysFile drop-in, got:\n%s", result)
	}
	if !strings.Contains(result, "f+~ /etc/ssh/authorized_keys.d/cachy 0644 - - - "+b64Key) {
		t.Errorf("missing root-owned /etc/ssh/authorized_keys.d/cachy entry, got:\n%s", result)
	}
	if !strings.Contains(result, "f+~ /home/cachy/.ssh/authorized_keys 0600 cachy cachy - "+b64Key) {
		t.Errorf("missing ~/.ssh/authorized_keys fallback, got:\n%s", result)
	}
}
