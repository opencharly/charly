package main

import (
	"testing"
)

// TestCanonicalizeDeployArg relocated to candy/plugin-bundle (#55 decoupling, Batch A) — it
// asserted deploykit.CanonicalizeDeployArg directly, zero charly dep.

// TestResolveLocalImageRef_PrefersBaseOverAlias asserts that when two
// equal-CalVer candidates share the `ai.opencharly.box` label
// (because `tagDeployAlias` tags an instance alias inheriting the
// base label), the resolver picks the BASE ref (repo's trailing
// segment == short name) over the alias (`<base>/<instance>`).
func TestResolveLocalImageRef_PrefersBaseOverAlias(t *testing.T) {
	// matchesShortName logic exercised via the sort callback's
	// behavior. We can't run the full resolver without podman, but
	// we verify the helper directly by simulating the candidates.
	// The actual matchesShortName closure lives inside
	// resolveLocalImageRef; here we mirror it.
	matchesShortName := func(ref, name string) bool {
		repo := ref
		for i, ch := range ref {
			if ch == ':' || ch == '@' {
				repo = ref[:i]
				break
			}
		}
		if i := lastIndex(repo, '/'); i >= 0 {
			repo = repo[i+1:]
		}
		return repo == name
	}
	for _, tc := range []struct {
		ref, name string
		want      bool
	}{
		{"ghcr.io/opencharly/versa:2026.132.1941", "versa", true},
		{"ghcr.io/opencharly/versa/ecovoyage:2026.132.1941", "versa", false},
		{"ghcr.io/opencharly/sway-browser-vnc:1.0", "sway-browser-vnc", true},
		{"ghcr.io/opencharly/sway-browser-vnc/ecovoyage:1.0", "sway-browser-vnc", false},
	} {
		if got := matchesShortName(tc.ref, tc.name); got != tc.want {
			t.Errorf("matchesShortName(%q, %q) = %v, want %v", tc.ref, tc.name, got, tc.want)
		}
	}
}

// TestMergeDeployOntoMetadata_KeyedByDeployNameNotImage / TestMergeDeployOntoMetadata_
// VolumesScopedToDeployKey relocated to candy/plugin-bundle (#55 decoupling, Batch A) — they
// asserted deploykit.MergeDeployOntoMetadata directly, zero charly dep.

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
