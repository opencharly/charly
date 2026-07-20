package substratekind

// status_flat_test.go — ported from charly/status_test.go + charly/status_collector_test.go (K6):
// the whole status_collector.go move brought its test coverage with it.

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// --- formatTunnelSummary ---

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

// --- flatCollector.lookupDeploy ---

func TestFlatCollector_LookupDeploy_KeyShapes(t *testing.T) {
	c := &flatCollector{
		deploy: &deploykit.BundleConfig{
			Bundle: map[string]spec.BundleNode{
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

// --- flatCollector.enrichVmRow ---

// TestEnrichVmRow covers the deploy-tree enrichment (SSH-port/network from a matching target:vm
// entry's vm_state).
func TestEnrichVmRow(t *testing.T) {
	cases := []struct {
		name      string
		row       spec.DeploymentStatus
		deploy    *deploykit.BundleConfig
		wantNet   string
		wantPorts []spec.PortMapping
	}{
		{
			name: "no deploy entry — row unchanged",
			row:  spec.DeploymentStatus{Kind: spec.SubstrateVM, Image: "cachyos-gpu"},
		},
		{
			name: "enriched from target:vm deploy vm_state",
			row:  spec.DeploymentStatus{Kind: spec.SubstrateVM, Image: "cachyos-gpu"},
			deploy: &deploykit.BundleConfig{
				Bundle: map[string]spec.BundleNode{
					"vm:cachyos-gpu": {
						Target:  "vm",
						From:    "cachyos-gpu",
						VmState: &spec.VmDeployState{SshPort: 12228, SshUser: "cachy", Backend: "libvirt"},
					},
				},
			},
			wantPorts: []spec.PortMapping{{HostPort: 12228, CtrPort: 22, Proto: "tcp"}},
		},
		{
			name: "bed whose deploy key differs from vm entity is matched",
			row:  spec.DeploymentStatus{Kind: spec.SubstrateVM, Image: "k3s-vm"},
			deploy: &deploykit.BundleConfig{
				Bundle: map[string]spec.BundleNode{
					// deploy KEY (check-k3s-vm) != vm entity (k3s-vm).
					"check-k3s-vm": {
						Target:  "vm",
						From:    "k3s-vm",
						VmState: &spec.VmDeployState{SshPort: 2225, SshUser: "arch"},
					},
				},
			},
			wantPorts: []spec.PortMapping{{HostPort: 2225, CtrPort: 22, Proto: "tcp"}},
		},
		{
			name: "network filled from deploy entry with no vm_state",
			row:  spec.DeploymentStatus{Kind: spec.SubstrateVM, Image: "arch"},
			deploy: &deploykit.BundleConfig{
				Bundle: map[string]spec.BundleNode{
					"arch": {Target: "vm", From: "arch", Network: "bridge0"},
				},
			},
			wantNet: "bridge0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &flatCollector{}
			row := tc.row
			c.enrichVmRow(&row, flatCollectOpts{Deploy: tc.deploy})
			if row.Network != tc.wantNet {
				t.Errorf("Network = %q, want %q", row.Network, tc.wantNet)
			}
			if len(row.Ports) != len(tc.wantPorts) {
				t.Fatalf("Ports = %+v, want %+v", row.Ports, tc.wantPorts)
			}
			for i := range tc.wantPorts {
				if row.Ports[i] != tc.wantPorts[i] {
					t.Errorf("Ports[%d] = %+v, want %+v", i, row.Ports[i], tc.wantPorts[i])
				}
			}
		})
	}
}
