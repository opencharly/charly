package substratekind

import (
	"context"
	"errors"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// withMockLibvirtDomains swaps the package-level listLibvirtCharlyDomains
// (forcing libvirtSessionAvailable true too, so the availability gate never
// short-circuits before the mock is reached) for the duration of fn,
// restoring both afterwards. Mirrors the InspectContainer swap pattern in
// charly/checkvars.go so tests need no live libvirt session daemon or
// reverse channel.
func withMockLibvirtDomains(t *testing.T, domains []domainInfo, err error, fn func()) {
	t.Helper()
	prevList, prevAvail := listLibvirtCharlyDomains, libvirtSessionAvailable
	listLibvirtCharlyDomains = func(context.Context) ([]domainInfo, error) { return domains, err }
	libvirtSessionAvailable = func() bool { return true }
	defer func() { listLibvirtCharlyDomains, libvirtSessionAvailable = prevList, prevAvail }()
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

// TestCollectVmStatus_Rows asserts collectVmStatus maps each mocked libvirt
// domain to a BARE row (Kind/Source/Image/Status/Container) — no deploy
// enrichment (a separate concern: status_flat.go's flatCollector.enrichVmRow,
// K6, same package, tested in status_flat_test.go against synthetic
// BundleConfig fixtures).
func TestCollectVmStatus_Rows(t *testing.T) {
	cases := []struct {
		name    string
		domains []domainInfo
		want    []spec.DeploymentStatus
	}{
		{name: "no domains", domains: nil, want: nil},
		{
			name:    "running domain",
			domains: []domainInfo{{Name: "charly-cachyos-gpu", State: "running"}},
			want: []spec.DeploymentStatus{
				{Kind: spec.SubstrateVM, Source: "libvirt", Image: "cachyos-gpu", Status: "running", Container: "charly-cachyos-gpu"},
			},
		},
		{
			name: "stopped + paused domains",
			domains: []domainInfo{
				{Name: "charly-arch", State: "shut off"},
				{Name: "charly-k3s-vm", State: "paused"},
			},
			want: []spec.DeploymentStatus{
				{Kind: spec.SubstrateVM, Source: "libvirt", Image: "arch", Status: "stopped", Container: "charly-arch"},
				{Kind: spec.SubstrateVM, Source: "libvirt", Image: "k3s-vm", Status: "paused", Container: "charly-k3s-vm"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withMockLibvirtDomains(t, tc.domains, nil, func() {
				reply, err := collectVmStatus(context.Background(), spec.SubstrateStatusRequest{})
				if err != nil {
					t.Fatalf("collectVmStatus returned error: %v", err)
				}
				assertDeploymentRowsEqual(t, reply.Rows, tc.want)
			})
		})
	}
}

func TestCollectVmStatus_DegradesOnListError(t *testing.T) {
	withMockLibvirtDomains(t, nil, errors.New("boom"), func() {
		reply, err := collectVmStatus(context.Background(), spec.SubstrateStatusRequest{})
		if err != nil {
			t.Fatalf("collectVmStatus should degrade gracefully, got error: %v", err)
		}
		if len(reply.Rows) != 0 {
			t.Errorf("rows = %d, want 0 on a list-domains error", len(reply.Rows))
		}
	})
}

// assertDeploymentRowsEqual compares the substrate-relevant fields of two
// DeploymentStatus slices (Kind, Source, Image, Status, Container). Shared
// with status_k8s_test.go.
func assertDeploymentRowsEqual(t *testing.T, got, want []spec.DeploymentStatus) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Source != w.Source || g.Image != w.Image ||
			g.Status != w.Status || g.Container != w.Container {
			t.Errorf("row %d mismatch:\n got: %+v\nwant: %+v", i, g, w)
		}
	}
}
