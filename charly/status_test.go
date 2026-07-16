package main

import (
	"testing"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/spec"
)

// status_test.go — HOST-side `charly status` tests for the code that STAYS
// core after P14a: formatTunnelSummary, parsePortStrings, Collector.lookupDeploy,
// and the enginekit ContainerSnapshot.HostPortFor data-flow (the enginekit
// package's own coverage, retained here). The probe Parse tests, the
// collectPodLive golden, the live-mounts renderer, the quadlet-description
// parser, the local install-ledger collector, and statusFromState all moved to
// candy/plugin-substrate/status_test.go alongside the code they exercise (the
// P14a clean-subset move); the render-side tests live in
// candy/plugin-status/render_test.go.

// --- formatTunnelSummary (a collection helper — stays host) ---

func TestFormatTunnelSummary(t *testing.T) {
	tests := []struct {
		name string
		in   *spec.TunnelYAML
		want string
	}{
		{"nil", nil, ""},
		{"tailscale all", &spec.TunnelYAML{Provider: "tailscale", Private: spec.PortScope{All: true}}, "tailscale (all ports)"},
		{"cloudflare all", &spec.TunnelYAML{Provider: "cloudflare", Public: spec.PortScope{All: true}}, "cloudflare (all ports)"},
		{"provider only", &spec.TunnelYAML{Provider: "tailscale"}, "tailscale"},
		{"explicit ports", &spec.TunnelYAML{Provider: "tailscale", Private: spec.PortScope{Ports: []int{8080, 9000}}}, "tailscale (ports 8080,9000)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTunnelSummary(tt.in); got != tt.want {
				t.Errorf("formatTunnelSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

// NOTE: the parsePS / parseDockerPortString white-box tests (formerly here)
// moved to the enginekit package with the engine-parsing code they exercise
// (chunk 1 relocated those functions to sdk/enginekit as unexported symbols).

// --- Snapshot HostPortFor ---

func TestHostPortFor_Bridge(t *testing.T) {
	snap := &enginekit.ContainerSnapshot{
		NetworkMode: "charly",
		Ports: []spec.PortMapping{
			{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"},
			{HostIP: "0.0.0.0", HostPort: 5900, CtrPort: 5900, Proto: "tcp"},
		},
	}
	ip, port, ok := snap.HostPortFor(9222, "tcp")
	if !ok || ip != "127.0.0.1" || port != 9240 {
		t.Errorf("9222: ok=%v ip=%q port=%d", ok, ip, port)
	}
	ip, port, ok = snap.HostPortFor(5900, "tcp")
	if !ok || ip != "127.0.0.1" || port != 5900 {
		t.Errorf("5900 (0.0.0.0 → 127.0.0.1): ok=%v ip=%q port=%d", ok, ip, port)
	}
	if _, _, ok := snap.HostPortFor(9999, "tcp"); ok {
		t.Errorf("9999 should not be published")
	}
}

func TestHostPortFor_HostNetwork(t *testing.T) {
	snap := &enginekit.ContainerSnapshot{NetworkMode: "host"}
	ip, port, ok := snap.HostPortFor(9222, "tcp")
	if !ok || ip != "127.0.0.1" || port != 9222 {
		t.Errorf("host-net 9222: ok=%v ip=%q port=%d", ok, ip, port)
	}
}

// --- parsePortStrings (deploy.yml + image label fallback) ---

func TestParsePortStrings(t *testing.T) {
	in := []string{"8888:8888", "127.0.0.1:9240:9222/tcp", "[::1]:5900:5900"}
	out := parsePortStrings(in)
	if len(out) != 3 {
		t.Fatalf("got %d, want 3 — IPv4-prefixed form must parse", len(out))
	}
	if out[0].HostPort != 8888 || out[0].CtrPort != 8888 {
		t.Errorf("[0] = %+v", out[0])
	}
	if out[1].HostIP != "127.0.0.1" || out[1].HostPort != 9240 || out[1].CtrPort != 9222 || out[1].Proto != "tcp" {
		t.Errorf("[1] = %+v", out[1])
	}
	if out[2].HostIP != "[::1]" || out[2].HostPort != 5900 || out[2].CtrPort != 5900 {
		t.Errorf("[2] = %+v", out[2])
	}
}

// --- Collector lookup helpers ---

func TestCollector_LookupDeploy_KeyShapes(t *testing.T) {
	c := &Collector{
		deploy: &BundleConfig{
			Bundle: map[string]BundleNode{
				"selkies-desktop":      {Port: []string{"3000:3000"}},
				"selkies-desktop/work": {Port: []string{"3001:3000"}, Tunnel: &spec.TunnelYAML{Provider: "tailscale", Private: spec.PortScope{All: true}}},
				"weird-joined-name":    {Port: []string{"7777:7777"}},
			},
		},
	}
	// Base image, no instance — direct hit.
	dn, ok := c.lookupDeploy("selkies-desktop", "", "charly-selkies-desktop")
	if !ok || len(dn.Port) == 0 {
		t.Errorf("base lookup failed: ok=%v ports=%v", ok, dn.Port)
	}
	// Image + instance — deployKey form.
	dn, ok = c.lookupDeploy("selkies-desktop", "work", "charly-selkies-desktop-work")
	if !ok || dn.Tunnel == nil || dn.Tunnel.Provider != "tailscale" {
		t.Errorf("instance lookup failed: ok=%v tunnel=%+v", ok, dn.Tunnel)
	}
	// Joined-name fallback.
	dn, ok = c.lookupDeploy("", "", "charly-weird-joined-name")
	if !ok || len(dn.Port) == 0 {
		t.Errorf("joined-name lookup failed: ok=%v", ok)
	}
}
