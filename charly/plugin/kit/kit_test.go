package kit

import "testing"

// TestResolvePackageName covers the package_map distro-aware lookup (cross-distro tests
// where package names diverge, e.g. openssh-server on Fedora vs openssh on Arch). The
// first matching distro tag wins; unmatched distros fall back to the bare name. Relocated
// here with the resolver from charly's package main (formerly checkrun_verbs.go's
// resolvePackageName), now the single source kit.ResolvePackageName.
func TestResolvePackageName(t *testing.T) {
	cases := []struct {
		name       string
		pkg        string
		packageMap map[string]string
		distros    []string
		want       string
	}{
		{"empty map returns scalar", "openssh-server", nil, []string{"fedora"}, "openssh-server"},
		{"map key matches distro tag", "openssh-server", map[string]string{"arch": "openssh", "fedora": "openssh-server"}, []string{"arch"}, "openssh"},
		{"first matching tag wins", "openssh-server", map[string]string{"fedora:43": "openssh-server43", "fedora": "openssh-server"}, []string{"fedora:43", "fedora"}, "openssh-server43"},
		{"no tag matches — fallback to scalar", "openssh-server", map[string]string{"arch": "openssh"}, []string{"ubuntu"}, "openssh-server"},
		{"empty distros — fallback to scalar", "openssh-server", map[string]string{"arch": "openssh"}, nil, "openssh-server"},
		{"empty string map value — fall through to next distro", "openssh-server", map[string]string{"arch": "", "fedora": "openssh-server"}, []string{"arch", "fedora"}, "openssh-server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePackageName(tc.pkg, tc.packageMap, tc.distros); got != tc.want {
				t.Errorf("ResolvePackageName() = %q, want %q", got, tc.want)
			}
		})
	}
}
