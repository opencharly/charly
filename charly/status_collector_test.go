package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestEnrichVmRow covers the deploy-tree enrichment (SSH-port/network from a
// matching target:vm entry's vm_state) that stays host-side after the K5
// vm-collector move (K5: relocated from the former VMCollector.enrichFromDeploy
// test, TestVMCollector_Collect's "enriched from ... vm_state" cases).
func TestEnrichVmRow(t *testing.T) {
	cases := []struct {
		name      string
		row       spec.DeploymentStatus
		deploy    *BundleConfig
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
			deploy: &BundleConfig{
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
			deploy: &BundleConfig{
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
			deploy: &BundleConfig{
				Bundle: map[string]spec.BundleNode{
					"arch": {Target: "vm", From: "arch", Network: "bridge0"},
				},
			},
			wantNet: "bridge0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Collector{}
			row := tc.row
			c.enrichVmRow(&row, CollectOpts{Deploy: tc.deploy})
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
