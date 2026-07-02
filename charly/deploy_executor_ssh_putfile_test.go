package main

import (
	"strings"
	"testing"
)

// TestSSHInstallScript_PrivilegeByOwnerRoot locks the SSHExecutor.PutFile privilege contract
// (R3 parity with ShellExecutor.PutFile): a USER-scoped placement (ownerRoot=false) runs AS
// THE GUEST USER (asRoot=false, NO sudo) so the file + the dirs `install -D` creates are
// user-owned; a SYSTEM-scoped placement (ownerRoot=true) runs as root (sudo + -o root -g
// root). The regression this guards: env.d / shell-rc / ledger writes (ownerRoot=false) were
// run under sudo, creating root-owned ~/.config/opencharly in the guest and blocking the
// user-scoped ledger write ("mkdir … Permission denied"), failing the 3 guest-ledger probes.
func TestSSHInstallScript_PrivilegeByOwnerRoot(t *testing.T) {
	// User-scoped (env.d, shell rc, the guest ledger): NO sudo, runs as the user.
	userScript, userAsRoot := sshInstallScript("/tmp/stage", "/home/arch/.config/opencharly/env.d/nodejs.env", "0644", false)
	if userAsRoot {
		t.Error("ownerRoot=false MUST NOT run as root (sudo) — that creates root-owned ~/.config in the guest")
	}
	if strings.Contains(userScript, "sudo") {
		t.Errorf("user-scoped install script must not invoke sudo:\n%s", userScript)
	}
	if strings.Contains(userScript, "-o root") {
		t.Errorf("user-scoped install must not force root ownership:\n%s", userScript)
	}
	if !strings.Contains(userScript, "install -D -m 0644") {
		t.Errorf("user-scoped script missing the install:\n%s", userScript)
	}

	// System-scoped (a /etc file, a packaged unit): sudo + explicit root ownership.
	sysScript, sysAsRoot := sshInstallScript("/tmp/stage", "/etc/yum.repos.d/x.repo", "0644", true)
	if !sysAsRoot {
		t.Error("ownerRoot=true MUST run as root (sudo)")
	}
	if !strings.Contains(sysScript, "sudo install") || !strings.Contains(sysScript, "-o root -g root") {
		t.Errorf("system-scoped script must sudo-install root-owned:\n%s", sysScript)
	}
}
