package main

import (
	"context"
	"errors"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// withMockLibvirtDomains swaps the package-level listLibvirtCharlyDomains for the
// duration of fn, restoring the real implementation afterwards. Mirrors the
// InspectContainer swap pattern in checkvars.go so tests need no live libvirt.
func withMockLibvirtDomains(t *testing.T, domains []domainInfo, err error, fn func()) {
	t.Helper()
	prev := listLibvirtCharlyDomains
	listLibvirtCharlyDomains = func() ([]domainInfo, error) { return domains, err }
	defer func() { listLibvirtCharlyDomains = prev }()
	fn()
}

func TestVMStatusFromDomainState(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"running", "running"},
		{"shut off", "stopped"},
		{"shutting down", "stopped"},
		{"paused", "paused"},
		{"suspended", "paused"},
		{"crashed", "dead"},
		{"unknown", "stopped"},
		{"", "stopped"},
	}
	for _, tc := range cases {
		if got := vmStatusFromDomainState(tc.in); got != tc.want {
			t.Errorf("vmStatusFromDomainState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVMCollector_Collect(t *testing.T) {
	cases := []struct {
		name    string
		domains []domainInfo
		deploy  *BundleConfig
		want    []spec.DeploymentStatus
	}{
		{
			name:    "no domains",
			domains: nil,
			want:    []spec.DeploymentStatus{},
		},
		{
			name: "running domain, no deploy entry",
			domains: []domainInfo{
				{Name: "charly-cachyos-gpu", State: "running"},
			},
			want: []spec.DeploymentStatus{
				{
					Kind:      spec.SubstrateVM,
					Source:    "libvirt",
					Image:     "cachyos-gpu",
					Status:    "running",
					Container: "charly-cachyos-gpu",
				},
			},
		},
		{
			name: "stopped + paused domains, no deploy",
			domains: []domainInfo{
				{Name: "charly-arch", State: "shut off"},
				{Name: "charly-k3s-vm", State: "paused"},
			},
			want: []spec.DeploymentStatus{
				{Kind: spec.SubstrateVM, Source: "libvirt", Image: "arch", Status: "stopped", Container: "charly-arch"},
				{Kind: spec.SubstrateVM, Source: "libvirt", Image: "k3s-vm", Status: "paused", Container: "charly-k3s-vm"},
			},
		},
		{
			name: "running domain enriched from target:vm deploy vm_state",
			domains: []domainInfo{
				{Name: "charly-cachyos-gpu", State: "running"},
			},
			deploy: &BundleConfig{
				Bundle: map[string]BundleNode{
					"vm:cachyos-gpu": {
						Target:  "vm",
						From:    "cachyos-gpu",
						VmState: &spec.VmDeployState{SshPort: 12228, SshUser: "cachy", Backend: "libvirt"},
					},
				},
			},
			want: []spec.DeploymentStatus{
				{
					Kind:      spec.SubstrateVM,
					Source:    "libvirt",
					Image:     "cachyos-gpu",
					Status:    "running",
					Container: "charly-cachyos-gpu",
					Ports:     []spec.PortMapping{{HostPort: 12228, CtrPort: 22, Proto: "tcp"}},
				},
			},
		},
		{
			name: "bed whose deploy key differs from vm entity is matched",
			domains: []domainInfo{
				{Name: "charly-k3s-vm", State: "running"},
			},
			deploy: &BundleConfig{
				Bundle: map[string]BundleNode{
					// deploy KEY (check-k3s-vm) != vm entity (k3s-vm).
					"check-k3s-vm": {
						Target:  "vm",
						From:    "k3s-vm",
						VmState: &spec.VmDeployState{SshPort: 2225, SshUser: "arch"},
					},
				},
			},
			want: []spec.DeploymentStatus{
				{
					Kind:      spec.SubstrateVM,
					Source:    "libvirt",
					Image:     "k3s-vm",
					Status:    "running",
					Container: "charly-k3s-vm",
					Ports:     []spec.PortMapping{{HostPort: 2225, CtrPort: 22, Proto: "tcp"}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withMockLibvirtDomains(t, tc.domains, nil, func() {
				v := &VMCollector{c: &Collector{}}
				opts := CollectOpts{Deploy: tc.deploy}
				got, err := v.Collect(context.Background(), opts)
				if err != nil {
					t.Fatalf("Collect returned error: %v", err)
				}
				assertDeploymentRowsEqual(t, got, tc.want)
			})
		})
	}
}

func TestVMCollector_CollectError(t *testing.T) {
	wantErr := context.DeadlineExceeded
	withMockLibvirtDomains(t, nil, wantErr, func() {
		v := &VMCollector{c: &Collector{}}
		_, err := v.Collect(context.Background(), CollectOpts{})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Collect error = %v, want %v", err, wantErr)
		}
	})
}

func TestVMCollector_Kind(t *testing.T) {
	v := &VMCollector{}
	if v.Kind() != spec.SubstrateVM {
		t.Errorf("Kind() = %q, want %q", v.Kind(), spec.SubstrateVM)
	}
}

// assertDeploymentRowsEqual compares the substrate-relevant fields of two
// DeploymentStatus slices (Kind, Source, Image, Status, Container, Ports). It
// avoids reflect.DeepEqual on the whole struct so unrelated zero-value fields
// don't make the comparison brittle.
func assertDeploymentRowsEqual(t *testing.T, got, want []spec.DeploymentStatus) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Source != w.Source || g.Image != w.Image ||
			g.Status != w.Status || g.Container != w.Container {
			t.Errorf("row %d core mismatch:\n got: %+v\nwant: %+v", i, g, w)
		}
		if len(g.Ports) != len(w.Ports) {
			t.Errorf("row %d port count = %d, want %d", i, len(g.Ports), len(w.Ports))
			continue
		}
		for j := range w.Ports {
			if g.Ports[j] != w.Ports[j] {
				t.Errorf("row %d port %d = %+v, want %+v", i, j, g.Ports[j], w.Ports[j])
			}
		}
	}
}
