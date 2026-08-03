package vm

// Relocated from charly/host_infra_test.go's "hostdistro.go" section (#55
// decoupling cone, Batch C): the 4 distro-detection tests assert
// vmshared.SplitOsReleaseLine / vmshared.HostDistro directly — zero charly
// coupling. The ledger tests move to plugin-bundle (Batch A), the
// builder-run tests to plugin-build (Batch B), and the managed-block tests to
// plugin-bundle (Batch A) — all separately, per the decoupling classification.

import (
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

func TestDetectHostDistroFedora43(t *testing.T) {
	// Verify parsing against a realistic os-release body — we synthesize
	// a temp file and exercise the line-parsing primitive.
	tests := []struct {
		line string
		k, v string
		ok   bool
	}{
		{`ID=fedora`, "ID", "fedora", true},
		{`VERSION_ID=43`, "VERSION_ID", "43", true},
		{`ID_LIKE="debian ubuntu"`, "ID_LIKE", "debian ubuntu", true},
		{`NAME='Fedora Linux'`, "NAME", "Fedora Linux", true},
		{`# comment`, "", "", false},
		{``, "", "", false},
	}
	for _, tc := range tests {
		k, v, ok := vmshared.SplitOsReleaseLine(tc.line)
		if k != tc.k || v != tc.v || ok != tc.ok {
			t.Errorf("splitOsReleaseLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				tc.line, k, v, ok, tc.k, tc.v, tc.ok)
		}
	}
}

func TestHostDistroTagsAndFormatHint(t *testing.T) {
	tests := []struct {
		hd      *vmshared.HostDistro
		wantTag string
		wantFmt string
	}{
		{
			hd:      &vmshared.HostDistro{ID: "fedora", VersionID: "43"},
			wantTag: "fedora:43",
			wantFmt: "rpm",
		},
		{
			hd:      &vmshared.HostDistro{ID: "ubuntu", VersionID: "24.04", IDLike: []string{"debian"}},
			wantTag: "ubuntu:24.04",
			wantFmt: "deb",
		},
		{
			hd:      &vmshared.HostDistro{ID: "arch"},
			wantTag: "arch",
			wantFmt: "pac",
		},
	}
	for _, tc := range tests {
		tc.hd.PopulateTags()
		if got := tc.hd.PrimaryTag(); got != tc.wantTag {
			t.Errorf("PrimaryTag() = %q, want %q", got, tc.wantTag)
		}
		if got := tc.hd.FormatHint(); got != tc.wantFmt {
			t.Errorf("FormatHint() = %q, want %q", got, tc.wantFmt)
		}
	}
}

func TestParseGlibcVersion(t *testing.T) {
	tests := map[string]string{
		"ldd (GNU libc) 2.39\n":                     "2.39",
		"ldd (Ubuntu GLIBC 2.35-0ubuntu3.8) 2.35\n": "2.35",
		"ldd (GNU libc) 2.38.0\n":                   "2.38",
		"something unexpected\n":                    "",
		"":                                          "",
	}
	for in, want := range tests {
		if got := vmshared.ParseGlibcVersion(in); got != want {
			t.Errorf("parseGlibcVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareGlibc(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.39", "2.35", 1},
		{"2.35", "2.39", -1},
		{"2.35", "2.35", 0},
		{"2.39.0", "2.39.1", 0},
		{"2.40", "2.9", 1},
		{"", "2.39", 0}, // unknown compares equal
		{"2.39", "", 0},
	}
	for _, tc := range tests {
		if got := vmshared.CompareGlibc(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareGlibc(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
