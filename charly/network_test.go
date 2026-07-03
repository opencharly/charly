package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseResolvNameservers proves the upstream-DNS discovery skips loopback
// stubs (the 127.0.0.53 systemd-resolved stub — unreachable from a container
// netns) and dedupes real resolvers in file order. This is the fix for the
// rootless-podman + systemd-resolved external-DNS failure that crash-looped the
// MCP server in isolated check pods.
func TestParseResolvNameservers(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"systemd-resolved stub skipped", "nameserver 127.0.0.53\nsearch lan\n", nil},
		{"real upstream kept", "search lan\nnameserver 192.168.1.1\n", []string{"192.168.1.1"}},
		{"loopback skipped, real kept", "nameserver 127.0.0.1\nnameserver 8.8.8.8\n", []string{"8.8.8.8"}},
		{"ipv6 loopback skipped", "nameserver ::1\nnameserver 1.1.1.1\n", []string{"1.1.1.1"}},
		{"dedup in order", "nameserver 192.168.1.1\nnameserver 192.168.1.1\nnameserver 9.9.9.9\n", []string{"192.168.1.1", "9.9.9.9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "resolv-"+tc.name)
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := parseResolvNameservers(p); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseResolvNameservers(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
	// Missing file → nil, no panic.
	if got := parseResolvNameservers(filepath.Join(dir, "does-not-exist")); got != nil {
		t.Errorf("missing file should yield nil, got %v", got)
	}
}

func TestResolveNetworkDefault(t *testing.T) {
	orig := EnsureCharlyNetwork
	defer func() { EnsureCharlyNetwork = orig }()
	EnsureCharlyNetwork = func(engine string) error { return nil }

	got, err := ResolveNetwork("", "podman")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != CharlyNetworkName {
		t.Errorf("ResolveNetwork(\"\", \"podman\") = %q, want %q", got, CharlyNetworkName)
	}
}

func TestResolveNetworkExplicitHost(t *testing.T) {
	orig := EnsureCharlyNetwork
	defer func() { EnsureCharlyNetwork = orig }()
	EnsureCharlyNetwork = func(engine string) error {
		t.Error("EnsureCharlyNetwork should not be called for explicit network")
		return nil
	}

	got, err := ResolveNetwork("host", "podman")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != "host" {
		t.Errorf("ResolveNetwork(\"host\", \"podman\") = %q, want \"host\"", got)
	}
}

func TestResolveNetworkExplicitNone(t *testing.T) {
	got, err := ResolveNetwork("none", "docker")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != "none" {
		t.Errorf("ResolveNetwork(\"none\", \"docker\") = %q, want \"none\"", got)
	}
}

// TestReconcileDNSDiff proves ensureNetworkUpstreamDNS's core: reconcile the charly
// network's dns_servers to EXACTLY the host-current upstreams — add missing AND DROP
// STALE. The stale-drop is the fix for a host that changed LANs leaving a dead upstream
// pinned (add-only kept it → aardvark forwarded to a dead server → external resolution
// broke for every container). FAILS against the pre-fix add-only code (no drop).
func TestReconcileDNSDiff(t *testing.T) {
	set := func(ss ...string) map[string]bool {
		m := map[string]bool{}
		for _, s := range ss {
			m[s] = true
		}
		return m
	}
	cases := []struct {
		name     string
		have     map[string]bool
		desired  []string
		wantAdd  []string
		wantDrop []string
	}{
		{"host changed LAN — drop the stale dead upstream, keep current",
			set("192.168.1.1", "192.168.0.1", "2a02::1"),
			[]string{"192.168.0.1", "2a02::1"},
			nil, []string{"192.168.1.1"}},
		{"steady state — set already matches, no churn",
			set("192.168.0.1", "2a02::1"),
			[]string{"192.168.0.1", "2a02::1"},
			nil, nil},
		{"first add on an empty network",
			set(),
			[]string{"192.168.0.1"},
			[]string{"192.168.0.1"}, nil},
		{"full swap — every server changed",
			set("10.0.0.1"),
			[]string{"192.168.0.1", "192.168.0.2"},
			[]string{"192.168.0.1", "192.168.0.2"}, []string{"10.0.0.1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			add, drop := reconcileDNSDiff(c.have, c.desired)
			if !reflect.DeepEqual(add, c.wantAdd) {
				t.Errorf("add = %v, want %v", add, c.wantAdd)
			}
			if !reflect.DeepEqual(drop, c.wantDrop) {
				t.Errorf("drop = %v, want %v", drop, c.wantDrop)
			}
		})
	}
}
