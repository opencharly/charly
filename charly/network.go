package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CharlyNetworkName is the shared bridge network used by all charly containers.
const CharlyNetworkName = "charly"

// EnsureCharlyNetwork creates the "charly" network if it does not exist.
// It is a package-level var for testability.
var EnsureCharlyNetwork = defaultEnsureCharlyNetwork

func defaultEnsureCharlyNetwork(engine string) error {
	binary := EngineBinary(engine)
	// The shared bridge needs a working EXTERNAL DNS upstream so containers can
	// resolve registries / git remotes / MCP fallbacks. On a rootless-podman +
	// systemd-resolved host, /etc/resolv.conf is the 127.0.0.53 stub —
	// unreachable from a container netns — so aardvark-dns has no usable upstream
	// and every external name lookup times out (internal container names still
	// resolve). We pin the host's REAL upstreams on the network so aardvark
	// forwards correctly. Only meaningful for podman (docker's daemon manages
	// container DNS itself); empty when no non-loopback upstream is discoverable.
	upstreams := hostUpstreamDNSServers()
	podman := isPodmanEngine(engine)

	// Check if network already exists
	check := exec.Command(binary, "network", "inspect", CharlyNetworkName)
	check.Stdout = nil
	check.Stderr = nil
	if check.Run() == nil {
		if podman {
			ensureNetworkUpstreamDNS(binary, upstreams)
		}
		return nil
	}
	// Create it — with the discovered upstream DNS for podman.
	args := []string{"network", "create"}
	if podman {
		for _, u := range upstreams {
			args = append(args, "--dns", u)
		}
	}
	args = append(args, CharlyNetworkName)
	create := exec.Command(binary, args...)
	output, err := create.CombinedOutput()
	if err != nil {
		// Handle race: network may have been created between inspect and create
		recheck := exec.Command(binary, "network", "inspect", CharlyNetworkName)
		recheck.Stdout = nil
		recheck.Stderr = nil
		if recheck.Run() == nil {
			if podman {
				ensureNetworkUpstreamDNS(binary, upstreams)
			}
			return nil
		}
		return fmt.Errorf("creating %s network: %w\n%s", CharlyNetworkName, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Created '%s' network\n", CharlyNetworkName)
	return nil
}

// isPodmanEngine reports whether the resolved engine binary is podman (the only
// engine whose bridge DNS we tune; docker's daemon handles container DNS itself).
func isPodmanEngine(engine string) bool {
	return strings.Contains(strings.ToLower(EngineBinary(engine)), "podman")
}

// hostUpstreamDNSServers returns the host's REAL upstream DNS resolvers, so the
// charly bridge's aardvark-dns can forward EXTERNAL queries. On a systemd-resolved
// host /etc/resolv.conf is the 127.0.0.53 stub (unreachable from a container
// netns), so the real upstreams live in /run/systemd/resolve/resolv.conf; we read
// those first and fall back to any non-loopback nameserver in /etc/resolv.conf.
// Loopback (127.x / ::1) entries are skipped — inside a container they resolve to
// the container's own localhost, not the host's. Returns nil when only loopback
// stubs exist (then the network DNS is left unset rather than guessed).
func hostUpstreamDNSServers() []string {
	for _, path := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		if servers := parseResolvNameservers(path); len(servers) > 0 {
			return servers
		}
	}
	return nil
}

// parseResolvNameservers extracts non-loopback `nameserver` entries from a
// resolv.conf-shaped file, deduped in file order. Missing/unreadable file → nil.
func parseResolvNameservers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip := fields[1]
		// Skip loopback stubs (127.0.0.0/8, ::1) — not reachable from a
		// container's network namespace.
		if strings.HasPrefix(ip, "127.") || ip == "::1" {
			continue
		}
		if !seen[ip] {
			out = append(out, ip)
			seen[ip] = true
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: resolv.conf scan error: %v\n", err)
	}
	return out
}

// ensureNetworkUpstreamDNS adds any missing upstream resolver to the EXISTING
// charly network's dns_servers via `podman network update --dns-add` (podman 5+).
// Idempotent: servers already present are skipped, so a steady-state deploy
// performs no update (no aardvark churn). Best-effort — a failure (engine without
// `network update`, etc.) is logged, never fatal: the network still works, just
// without the external-DNS fix.
func ensureNetworkUpstreamDNS(binary string, upstreams []string) {
	if len(upstreams) == 0 {
		return
	}
	have := networkDNSServers(binary)
	var add []string
	for _, u := range upstreams {
		if !have[u] {
			add = append(add, u)
		}
	}
	if len(add) == 0 {
		return
	}
	args := []string{"network", "update", CharlyNetworkName}
	for _, u := range add {
		args = append(args, "--dns-add", u)
	}
	if out, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "charly: could not add upstream DNS %v to the %s network (%v); "+
			"external name resolution in containers may fail\n%s\n",
			add, CharlyNetworkName, err, strings.TrimSpace(string(out)))
	}
}

// networkDNSServers returns the set of dns_servers currently configured on the
// charly network. Empty on any inspect failure.
func networkDNSServers(binary string) map[string]bool {
	have := map[string]bool{}
	out, err := exec.Command(binary, "network", "inspect", CharlyNetworkName,
		"--format", "{{range .DNSServers}}{{println .}}{{end}}").Output()
	if err != nil {
		return have
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			have[s] = true
		}
	}
	return have
}

// ResolveNetwork returns the network to use for a container.
// If configured is non-empty (explicit override like "host"), it is returned as-is.
// Otherwise, the shared "charly" network is ensured and returned.
func ResolveNetwork(configured, engine string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if err := EnsureCharlyNetwork(engine); err != nil {
		return "", err
	}
	return CharlyNetworkName, nil
}
