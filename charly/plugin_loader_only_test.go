package main

// plugin_loader_only_test.go — $CHARLY_PLUGIN_ONLY is the supported way to resolve
// AS-IF-UNPACKAGED.
//
// bakedPluginDirs is a SEARCH PATH, and resolveCommandPluginBinary returns on the first baked
// hit before the project is loaded at all. So on a host with the charly package installed, a
// word that is baked into /usr/lib/charly/plugins masks the project's own declaration of that
// word: `charly <word> --help` cannot distinguish "my project declares this" from "the
// installed package provides it", and a project-path regression is invisible.
//
// $CHARLY_PLUGIN_DIR cannot express this — it PREPENDS, so the FHS path stays on the search no
// matter what it is set to. Before $CHARLY_PLUGIN_ONLY the only way to observe the project path
// was to mask the directory with a mount namespace (`bwrap --tmpfs /usr/lib/charly/plugins`),
// which needs a host tool and root-ish privileges and cannot run in a unit test.

import (
	"path/filepath"
	"testing"
)

// TestBakedPluginDirs_OnlyDropsTheFHSPath proves the flag removes the FHS directory from the
// search rather than merely reordering it. The FHS path being ABSENT is the whole capability;
// asserting only that the override is present would pass without the change.
func TestBakedPluginDirs_OnlyDropsTheFHSPath(t *testing.T) {
	override := filepath.Join(t.TempDir(), "plugins")

	for _, tc := range []struct {
		name    string
		dir, on string
		want    []string
	}{
		{"unset: FHS only", "", "", []string{bakedPluginDir}},
		{"dir set: override first, FHS still searched", override, "", []string{override, bakedPluginDir}},
		{"only=1 with a dir: exactly the override", override, "1", []string{override}},
		{"only=1 with no dir: empty search", "", "1", nil},
		{"only=0 is not the flag", override, "0", []string{override, bakedPluginDir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CHARLY_PLUGIN_DIR", tc.dir)
			t.Setenv("CHARLY_PLUGIN_ONLY", tc.on)
			got := bakedPluginDirs()
			if len(got) != len(tc.want) {
				t.Fatalf("bakedPluginDirs() = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("bakedPluginDirs()[%d] = %q, want %q (full: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestBakedPluginDirs_OnlyIsExactMatch guards the flag against truthy-string drift: anything
// other than "1" must leave the FHS path on the search, so a typo fails closed (the package's
// plugins stay reachable) rather than silently hiding them from a deployed container.
func TestBakedPluginDirs_OnlyIsExactMatch(t *testing.T) {
	for _, v := range []string{"true", "yes", "TRUE", " 1", "1 ", "01"} {
		t.Setenv("CHARLY_PLUGIN_DIR", "")
		t.Setenv("CHARLY_PLUGIN_ONLY", v)
		got := bakedPluginDirs()
		if len(got) != 1 || got[0] != bakedPluginDir {
			t.Fatalf("CHARLY_PLUGIN_ONLY=%q: got %v, want the FHS path to remain (fail closed)", v, got)
		}
	}
}
