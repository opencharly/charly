package main

// ssh_managed_config.go — managed ssh-config fragment for VM aliases.
//
// `charly vm create` writes one Host stanza per VM into a managed file at
// ~/.config/charly/ssh_config, fenced by the same `# opencharly:begin` /
// `# opencharly:end` markers used elsewhere (see shell_profile.go for
// the primitive). The user's ~/.ssh/config gains a single `Include`
// line (also inside a managed block) pointing at the fragment.
//
// After this is in place, `ssh charly-<vmname>` works directly from any
// terminal — and charly's own SSHExecutor constructs
// `&SSHExecutor{Host: "charly-"+vmName}` with no User/Port/Key, letting
// ssh-config supply everything. Idempotent: writing an existing
// stanza is a no-op; removing a non-existent one is a no-op.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// VmSshStanza captures the fields needed to render one ssh-config
// Host stanza for a VM. charly vm create populates this from the VM's
// post-provision state (SSH port forward, generated key path, user).
type VmSshStanza struct {
	// Alias is the ssh-config Host alias (e.g. "charly-arch-vm"). Must be
	// unique within the managed fragment.
	Alias string

	// Hostname is the IP or DNS name ssh actually connects to. For
	// user-mode-networking VMs this is "127.0.0.1".
	Hostname string

	// Port is the host-side port forwarded to the guest's :22.
	Port int

	// User is the guest account ssh logs in as ("charly", "arch", "root", …).
	User string

	// IdentityFile is the absolute path to the private key.
	IdentityFile string

	// KnownHostsFile is the absolute path to the per-VM known_hosts
	// file (so each VM has its own host-key history rather than
	// conflicting with other VMs in ~/.ssh/known_hosts).
	KnownHostsFile string
}

// SshFragmentPath returns ~/.config/charly/ssh_config given the user's
// home dir.
func SshFragmentPath(home string) string {
	return filepath.Join(home, ".config", "charly", "ssh_config")
}

// SshConfigPath returns ~/.ssh/config given the user's home dir.
func SshConfigPath(home string) string {
	return filepath.Join(home, ".ssh", "config")
}

// EnsureSshConfigInclude inserts the managed `Include ~/.config/charly/ssh_config`
// directive into the user's ~/.ssh/config (creating the file if needed).
// Idempotent: calling twice yields the same content.
//
// The Include line MUST be at the TOP of the file (outside any Host block)
// because ssh_config(5) processes Include directives in the lexical context
// they appear. If the Include lands inside a `Host pawsdev` block, every
// `Host` stanza inside the included file is gated on matching `pawsdev` —
// effectively dead. So the global-block append behavior in
// replaceOrAppendManagedBlock is wrong for ssh config; we prepend instead.
func EnsureSshConfigInclude(home string) error {
	cfgPath := SshConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("Include %s", SshFragmentPath(home))
	var existing string
	if data, err := os.ReadFile(cfgPath); err == nil {
		existing = string(data)
	}
	updated := replaceOrPrependManagedBlock(existing, body, "")
	return os.WriteFile(cfgPath, []byte(updated), 0o600)
}

// RemoveSshConfigInclude removes the managed Include line from
// ~/.ssh/config. Idempotent. Called when the last VM is destroyed.
func RemoveSshConfigInclude(home string) error {
	cfgPath := SshConfigPath(home)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	stripped := stripManagedBlock(string(data), "")
	if strings.TrimSpace(stripped) == "" {
		return os.Remove(cfgPath)
	}
	if stripped == string(data) {
		return nil
	}
	return os.WriteFile(cfgPath, []byte(stripped), 0o600)
}

// WriteVmSshStanza adds (or replaces) a Host stanza in the managed
// fragment. The stanza is wrapped inside the file's single managed
// block; multiple stanzas coexist within that block. Idempotent.
func WriteVmSshStanza(home string, s VmSshStanza) error {
	if s.Alias == "" {
		return fmt.Errorf("ssh stanza: empty alias")
	}
	if s.Hostname == "" || s.IdentityFile == "" {
		return fmt.Errorf("ssh stanza %q: hostname and identity_file required", s.Alias)
	}
	frag := SshFragmentPath(home)
	if err := os.MkdirAll(filepath.Dir(frag), 0o700); err != nil {
		return err
	}
	stanzas := loadStanzas(frag)
	stanzas[s.Alias] = renderStanza(s)
	return saveStanzas(frag, stanzas)
}

// RemoveVmSshStanza drops the named alias from the managed fragment
// and returns the count of remaining stanzas. When the count reaches
// zero, callers should also call RemoveSshConfigInclude. Idempotent.
func RemoveVmSshStanza(home string, alias string) (remaining int, err error) {
	frag := SshFragmentPath(home)
	if _, err := os.Stat(frag); os.IsNotExist(err) {
		return 0, nil
	}
	stanzas := loadStanzas(frag)
	if _, ok := stanzas[alias]; !ok {
		return len(stanzas), nil
	}
	delete(stanzas, alias)
	if len(stanzas) == 0 {
		_ = os.Remove(frag)
		return 0, nil
	}
	return len(stanzas), saveStanzas(frag, stanzas)
}

// ListVmSshAliases returns the alias names currently present in the
// managed fragment, in stable (sorted) order.
func ListVmSshAliases(home string) ([]string, error) { //nolint:unparam // error return kept for interface/API stability
	frag := SshFragmentPath(home)
	if _, err := os.Stat(frag); os.IsNotExist(err) {
		return nil, nil
	}
	stanzas := loadStanzas(frag)
	out := make([]string, 0, len(stanzas))
	for k := range stanzas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// VmSshAlias returns the canonical alias for a VM deployment name.
// The "charly-" prefix namespaces opencharly-managed aliases away from the
// user's own ssh-config Host entries.
func VmSshAlias(vmName string) string {
	return "charly-" + vmName
}

// renderStanza emits the textual form of a single Host stanza.
func renderStanza(s VmSshStanza) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Host %s\n", s.Alias)
	fmt.Fprintf(&sb, "    Hostname %s\n", s.Hostname)
	if s.Port > 0 {
		fmt.Fprintf(&sb, "    Port %d\n", s.Port)
	}
	if s.User != "" {
		fmt.Fprintf(&sb, "    User %s\n", s.User)
	}
	fmt.Fprintf(&sb, "    IdentityFile %s\n", s.IdentityFile)
	fmt.Fprintf(&sb, "    StrictHostKeyChecking accept-new\n")
	if s.KnownHostsFile != "" {
		fmt.Fprintf(&sb, "    UserKnownHostsFile %s\n", s.KnownHostsFile)
	}
	return sb.String()
}

// stanzaHostRegex matches a `Host <alias>` line at start-of-line.
var stanzaHostRegex = regexp.MustCompile(`(?m)^Host\s+(\S+)`)

// loadStanzas reads the managed body from path and parses it into a
// map of alias → stanza text. Returns an empty map when the file or
// the managed block is absent.
func loadStanzas(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	body := managedBody(string(data))
	if body == "" {
		return map[string]string{}
	}
	out := map[string]string{}
	matches := stanzaHostRegex.FindAllStringSubmatchIndex(body, -1)
	for i, m := range matches {
		alias := body[m[2]:m[3]]
		start := m[0]
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		out[alias] = body[start:end]
	}
	return out
}

// saveStanzas writes the alias → stanza map back into the managed
// block at path, in alias-sorted order for deterministic output.
func saveStanzas(path string, stanzas map[string]string) error {
	keys := make([]string, 0, len(stanzas))
	for k := range stanzas {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(strings.TrimRight(stanzas[k], "\n"))
		sb.WriteString("\n")
	}
	body := sb.String()
	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	updated := replaceOrAppendManagedBlock(existing, strings.TrimRight(body, "\n"), "")
	return os.WriteFile(path, []byte(updated), 0o600)
}

// managedBody returns just the contents between the begin/end fence
// markers in `text`. Returns "" when the markers are absent.
func managedBody(text string) string {
	_, after, ok := strings.Cut(text, managedBlockBegin)
	if !ok {
		return ""
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, managedBlockEnd)
	if !ok0 {
		return ""
	}
	return strings.TrimLeft(before0, "\n")
}
