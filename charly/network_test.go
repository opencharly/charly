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
