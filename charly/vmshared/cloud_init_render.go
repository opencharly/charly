package vmshared

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloudInitRuntimeParams carries the runtime-resolved state needed to
// render cloud-init user-data: the SSH public key to inject, the
// instance-id (stable UUIDv4 persisted in VmDeployState), the hostname,
// and whether cloud-init should inject the SSH key at all (computed
// from D13 auto-defaults + explicit VmKeyInjection overrides).
type CloudInitRuntimeParams struct {
	// SSHPublicKey is the OpenSSH authorized_keys-format public key
	// line (e.g. "ssh-ed25519 AAAA..."). Empty when key injection is
	// disabled or when VmSSH.KeySource == "none".
	SSHPublicKey string

	// InstanceID is the stable UUIDv4 cloud-init instance-id.
	// Pinned at first VM create and persisted in VmDeployState.
	InstanceID string

	// Hostname for the guest. Defaults to the VM name when empty.
	Hostname string

	// InjectKeyViaCloudInit is the resolved D13 key_injection.cloud_init
	// channel state. When false the renderer emits no
	// ssh_authorized_keys entries even if SSHPublicKey is populated.
	InjectKeyViaCloudInit bool
}

// RenderCloudInit produces the three NoCloud seed ISO payloads from a
// VmSpec plus runtime parameters. Pure function — no filesystem or
// network calls.
//
// - userData   → written to cidata/user-data (prefixed with #cloud-config)
// - metaData   → written to cidata/meta-data (instance-id + hostname)
// - networkCfg → written to cidata/network-config (optional; empty if unset)
//
// Defaults applied automatically (D15):
//  1. VmSSH.User added to users: with sudo + ssh_authorized_keys
//     (if the key-injection channel is enabled AND SSHPublicKey != "")
//  2. Minimum packages: {openssh, curl, tar} unioned with user's Packages
//  3. Minimum runcmd: {systemctl enable --now sshd} prepended
//  4. charly_install: NOT a cloud-init concern — the vm deploy's PrepareVenue delivers charly
//     post-boot (auto/scp stage it; skip verifies). No charly download runcmd.
//  5. VmCloudInit.Extra: raw cloud-config YAML appended as a second
//     document (separated by ---) if non-empty
func RenderCloudInit(spec *VmSpec, rt CloudInitRuntimeParams) (userData, metaData, networkConfig string, err error) {
	ci := spec.CloudInit
	if ci == nil {
		ci = &VmCloudInit{}
	}

	// --- meta-data ---

	hostname := rt.Hostname
	if ci.Hostname != "" {
		hostname = ci.Hostname
	}
	metaMap := map[string]any{}
	if rt.InstanceID != "" {
		metaMap["instance-id"] = rt.InstanceID
	}
	if hostname != "" {
		metaMap["local-hostname"] = hostname
	}
	metaBytes, err := yaml.Marshal(metaMap)
	if err != nil {
		return "", "", "", fmt.Errorf("render meta-data: %w", err)
	}
	metaData = string(metaBytes)
	if err := ValidateEgress("cloud_init_meta", "cloud-init meta-data", metaBytes); err != nil {
		return "", "", "", err
	}

	// --- network-config ---

	if ci.Network != nil && len(ci.Network.Ethernets) > 0 {
		netMap := map[string]any{
			"version": 2,
		}
		if ci.Network.Version > 0 {
			netMap["version"] = ci.Network.Version
		}
		netMap["ethernets"] = ci.Network.Ethernets
		netBytes, err := yaml.Marshal(netMap)
		if err != nil {
			return "", "", "", fmt.Errorf("render network-config: %w", err)
		}
		networkConfig = string(netBytes)
		if err := ValidateEgress("cloud_init_net", "cloud-init network-config", netBytes); err != nil {
			return "", "", "", err
		}
	}

	// --- user-data ---

	userMap := map[string]any{}

	if hostname != "" {
		userMap["hostname"] = hostname
	}
	if ci.Timezone != "" {
		userMap["timezone"] = ci.Timezone
	}
	if ci.Locale != "" {
		userMap["locale"] = ci.Locale
	}

	userMap["users"] = composeUsers(spec, ci, rt)

	packages := composePackages(ci.Package, spec.Source.Distro)
	if len(packages) > 0 {
		userMap["packages"] = packages
	}

	if len(ci.BootCmd) > 0 {
		userMap["bootcmd"] = ci.BootCmd
	}

	runcmd := composeRunCmd(spec, ci)
	if len(runcmd) > 0 {
		userMap["runcmd"] = runcmd
	}

	if len(ci.WriteFiles) > 0 {
		userMap["write_files"] = composeWriteFiles(ci.WriteFiles)
	}

	if ci.Mirrors != nil && len(ci.Mirrors.APT) > 0 {
		// cloud-init apt config (Ubuntu/Debian guests).
		userMap["apt"] = map[string]any{
			"preserve_sources_list": false,
			"primary": []map[string]any{
				{"arches": []string{"default"}, "uri": ci.Mirrors.APT[0]},
			},
		}
	}

	userBytes, err := yaml.Marshal(userMap)
	if err != nil {
		return "", "", "", fmt.Errorf("render user-data: %w", err)
	}
	// Egress gate: the rendered cloud-config (the structured user-data document,
	// before the #cloud-config header + any raw Extra passthrough) must validate
	// against Canonical's vendored schema before it reaches the seed ISO.
	if err := ValidateEgress("cloud_config", "cloud-init user-data", userBytes); err != nil {
		return "", "", "", err
	}

	var b strings.Builder
	b.WriteString("#cloud-config\n")
	b.Write(userBytes)

	if ci.Extra != "" {
		b.WriteString("---\n")
		extra := strings.TrimPrefix(ci.Extra, "#cloud-config\n")
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteString("\n")
		}
	}

	userData = b.String()
	return userData, metaData, networkConfig, nil
}

// composeUsers builds the cloud-init users: list. Mirrors the
// container-side user_policy pattern — adopt the upstream's
// pre-existing account when spec.Source.BaseUser is set (emit
// merge-by-name entry with only ssh_authorized_keys), otherwise
// create a new user with sudo+groups+shell.
//
// The rendered list ALWAYS starts with `- default` so cloud-init
// preserves the distro's default-user semantics (including its
// existing sudoers membership). Merge-by-name semantics mean no user
// is recreated: if an entry's Name matches an already-existing
// account, cloud-init just appends ssh_authorized_keys.
func composeUsers(spec *VmSpec, ci *VmCloudInit, rt CloudInitRuntimeParams) []any {
	sshUser := resolveCloudInitSSHUser(spec)
	baseUser := ""
	if spec != nil {
		baseUser = spec.Source.BaseUser
	}

	// Start with the distro default so cloud-init doesn't disable it.
	out := []any{"default"}

	// User-declared custom users are emitted as-is.
	mergedSSH := false
	for _, u := range ci.Users {
		entry := userEntryToMap(u)
		if u.Name == sshUser {
			applySSHDefaults(entry, rt)
			mergedSSH = true
		}
		out = append(out, entry)
	}

	if mergedSSH || sshUser == "" {
		return out
	}

	// Adopt path: the sshUser matches the upstream's pre-existing
	// account (BaseUser). Emit a minimal merge-by-name entry that only
	// appends ssh_authorized_keys. DO NOT set sudo/groups/shell —
	// trust upstream's sudoers config (Arch's arch user is in wheel
	// with NOPASSWD via /etc/sudoers.d/10-default, Ubuntu's ubuntu
	// user has equivalent, etc.).
	if baseUser != "" && sshUser == baseUser {
		entry := map[string]any{"name": sshUser}
		applySSHDefaults(entry, rt)
		out = append(out, entry)
		return out
	}

	// Create path: the sshUser is a NEW user the upstream didn't
	// ship. Emit full create-user entry with sudo, wheel membership,
	// and bash shell.
	entry := map[string]any{
		"name":   sshUser,
		"sudo":   "ALL=(ALL) NOPASSWD:ALL",
		"groups": []string{"wheel"},
		"shell":  "/bin/bash",
	}
	applySSHDefaults(entry, rt)
	out = append(out, entry)
	return out
}

// resolveCloudInitSSHUser picks the ssh user for cloud-init rendering.
// Precedence:
//  1. Explicit spec.ssh.user
//  2. spec.source.base_user (adopt path — cloud_image sources with a
//     declared upstream account)
//  3. Source-kind fallback ("root" for bootc, "arch" for cloud_image —
//     the latter works for Arch cloud images out of the box; for
//     other distros users MUST set base_user or ssh.user explicitly)
func resolveCloudInitSSHUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	if spec.Source.BaseUser != "" {
		return spec.Source.BaseUser
	}
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return ""
}

func userEntryToMap(u VmCloudInitUser) map[string]any {
	m := map[string]any{"name": u.Name}
	if u.Sudo {
		m["sudo"] = "ALL=(ALL) NOPASSWD:ALL"
	}
	if len(u.Groups) > 0 {
		m["groups"] = u.Groups
	}
	if u.Shell != "" {
		m["shell"] = u.Shell
	}
	if u.LockPasswd != nil {
		m["lock_passwd"] = *u.LockPasswd
	}
	return m
}

// applySSHDefaults adds ssh_authorized_keys to a user entry when
// key injection via cloud-init is enabled and a pubkey exists.
func applySSHDefaults(entry map[string]any, rt CloudInitRuntimeParams) {
	if rt.InjectKeyViaCloudInit && rt.SSHPublicKey != "" {
		existing, _ := entry["ssh_authorized_keys"].([]string)
		entry["ssh_authorized_keys"] = append(existing, rt.SSHPublicKey)
	}
}

// composePackages unions charly's minimum SSH+curl+tar package set with
// the user's declared packages, preserving the user's order for extras.
//
// The minimum SSH package name is distro-aware: `openssh` on
// Arch/Fedora, `openssh-server` on Debian/Ubuntu. Without this, cloud-
// init's package-install module hard-fails on Debian (`E: Unable to
// locate package openssh`), which then fails cloud-init-network →
// cloud-init.target → qemu-guest-agent stays stuck waiting forever.
func composePackages(userPkgs []string, distro string) []string {
	sshPkg := "openssh"
	switch distro {
	case "debian", "ubuntu":
		sshPkg = "openssh-server"
	}
	minimum := []string{sshPkg, "curl", "tar"}
	seen := map[string]bool{}
	var out []string
	for _, p := range minimum {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range userPkgs {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// composeRunCmd prepends charly's minimum boot task (enable sshd) and appends the
// user's runcmd. charly is NOT installed via cloud-init — the vm deploy's PrepareVenue
// delivers it post-boot per charly_install.strategy (auto/scp stage the host binary;
// skip verifies presence).
//
// The sshd unit name is distro-aware: `sshd` on Arch/Fedora,
// `ssh` on Debian/Ubuntu (where the systemd unit is named `ssh.service`
// — `sshd.service` is just a symlink that systemd refuses to enable).
func composeRunCmd(spec *VmSpec, ci *VmCloudInit) []any {
	runcmd := make([]any, 0, 1+len(ci.RunCmd))
	sshUnit := "sshd"
	switch spec.Source.Distro {
	case "debian", "ubuntu":
		sshUnit = "ssh"
	}
	runcmd = append(runcmd, fmt.Sprintf("systemctl enable --now %s", sshUnit))

	for _, cmd := range ci.RunCmd {
		runcmd = append(runcmd, cmd)
	}

	return runcmd
}

// composeWriteFiles turns VmCloudInitFile entries into the
// map-list shape cloud-init expects.
func composeWriteFiles(files []VmCloudInitFile) []map[string]any {
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		m := map[string]any{
			"path":    f.Path,
			"content": f.Content,
		}
		if f.Owner != "" {
			m["owner"] = f.Owner
		}
		if f.Perms != "" {
			m["permissions"] = f.Perms
		}
		if f.Encoding != "" {
			m["encoding"] = f.Encoding
		}
		out = append(out, m)
	}
	return out
}

// ResolveKeyInjectionChannels applies the D13 auto-defaults and
// explicit-wins merging to produce the effective (smbios, cloudInit)
// toggle state for a VmSpec. Returns the booleans persisted into
// VmDeployState.KeyInjectionResolved.
func ResolveKeyInjectionChannels(spec *VmSpec) (smbios, cloudInit bool) {
	// Defaults per D13:
	//   cloud_image → {smbios: true, cloud_init: true}  (additive)
	//   bootc       → {smbios: true, cloud_init: false}
	if spec.Source.Kind == "bootc" {
		smbios, cloudInit = true, false
	} else {
		smbios, cloudInit = true, true
	}

	if spec.SSH == nil || spec.SSH.KeyInjection == nil {
		return smbios, cloudInit
	}
	kj := spec.SSH.KeyInjection
	switch kj.SMBIOS {
	case "enabled":
		smbios = true
	case "disabled":
		smbios = false
	}
	switch kj.CloudInit {
	case "enabled":
		cloudInit = true
	case "disabled":
		cloudInit = false
	}
	return smbios, cloudInit
}
