package vmshared

import (
	"encoding/base64"
	"fmt"
)

// KeyToRootTmpfilesD converts an SSH public key to a systemd tmpfiles.d config
// that creates /root/.ssh/authorized_keys. Matches bcvk's key_to_root_tmpfiles_d.
func KeyToRootTmpfilesD(pubkey string) string {
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	return fmt.Sprintf("d /root/.ssh 0750 - - -\nf+~ /root/.ssh/authorized_keys 700 - - - %s\n", b64Key)
}

// KeyToUserTmpfilesD generates a systemd tmpfiles.d config that delivers a
// per-VM SSH key for the named user. The user account itself must already
// exist in the rootfs (created at build time by the bootloader install
// template OR by cloud-init); this function only delivers the *key*, never
// bakes one into the image.
//
// SMBIOS-vs-cloud-init priority: the key is written to a ROOT-owned, sshd-
// checked file at /etc/ssh/authorized_keys.d/<user>, and a sshd_config.d
// drop-in widens AuthorizedKeysFile to check BOTH ~/.ssh/authorized_keys
// (cloud-init's domain) AND that file. systemd-tmpfiles applies this BEFORE
// sshd starts, so the SMBIOS key is ALWAYS accepted even if cloud-init later
// rewrites the user's own authorized_keys — SMBIOS owns the drop-in location,
// cloud-init owns ~/.ssh, and sshd honors both. The key is ALSO written to
// ~/.ssh/authorized_keys as a fallback for any guest sshd that ignores the
// drop-in (so the key works whether or not the widened path takes effect).
//
// The home path defaults to /home/<user> when empty.
func KeyToUserTmpfilesD(user, home, pubkey string) string {
	if home == "" {
		home = fmt.Sprintf("/home/%s", user)
	}
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	// sshd reads /etc/ssh/sshd_config.d/*.conf via the stock Include at the
	// top of sshd_config; the 00- prefix makes this the FIRST AuthorizedKeysFile
	// directive, which sshd honors (first value wins).
	authKeysCfg := "AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/authorized_keys.d/%u\n"
	b64Cfg := base64.StdEncoding.EncodeToString([]byte(authKeysCfg))
	// `~` decodes the base64 argument; `-` owner/group on /etc paths defaults
	// to root (root-owned files pass sshd StrictModes for an absolute
	// AuthorizedKeysFile). The home authorized_keys stays user-owned.
	return fmt.Sprintf(
		"d /etc/ssh/sshd_config.d 0755 - - -\n"+
			"f+~ /etc/ssh/sshd_config.d/00-charly-smbios-ssh.conf 0644 - - - %s\n"+
			"d /etc/ssh/authorized_keys.d 0755 - - -\n"+
			"f+~ /etc/ssh/authorized_keys.d/%s 0644 - - - %s\n"+
			"d %s/.ssh 0700 %s %s -\n"+
			"f+~ %s/.ssh/authorized_keys 0600 %s %s - %s\n",
		b64Cfg, user, b64Key, home, user, user, home, user, user, b64Key,
	)
}

// SmbiosCredForSSH generates the SMBIOS type 11 credential string that
// delivers a per-VM SSH key to the named user via systemd-tmpfiles. When
// user == "" or user == "root", the legacy /root/.ssh path is used.
// Returns: "io.systemd.credential.binary:tmpfiles.extra=<base64>"
func SmbiosCredForSSH(user, home, pubkey string) string {
	var tmpfiles string
	if user == "" || user == "root" {
		tmpfiles = KeyToRootTmpfilesD(pubkey)
	} else {
		tmpfiles = KeyToUserTmpfilesD(user, home, pubkey)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(tmpfiles))
	return fmt.Sprintf("io.systemd.credential.binary:tmpfiles.extra=%s", encoded)
}

// SmbiosCredForRootSSH is preserved as a compatibility wrapper for the
// existing call sites in vm.go (legacy bootc paths).
func SmbiosCredForRootSSH(pubkey string) string {
	return SmbiosCredForSSH("root", "", pubkey)
}
