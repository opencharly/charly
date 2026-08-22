package hostenv

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// distro_traits_test.go — gates the GENERATED distro vocabulary.
//
// There is no hand-written distro table to keep in step any more: schema/distro_vocab.cue's
// #Distros is the single source, and `task cue:gen` projects it into spec.DistroFormats /
// DistroSSHUnits / DistroInits / DistroIDs. So the thing worth testing is no longer "do two
// tables agree" — it is "did the generator actually project every row", since a harvest that
// silently returned a partial map would produce a table that looks fine and answers "" for
// the ids it dropped.

func TestGeneratedDistroTablesCoverEveryID(t *testing.T) {
	if len(spec.DistroIDs) == 0 {
		t.Fatal("spec.DistroIDs is empty — the #Distros harvest is broken, and every assertion " +
			"below would pass vacuously")
	}
	for _, id := range spec.DistroIDs {
		if _, ok := spec.DistroFormats[id]; !ok {
			t.Errorf("DistroFormats has no row for %q — FormatForDistroID would return \"\"", id)
		}
		if _, ok := spec.DistroSSHUnits[id]; !ok {
			t.Errorf("DistroSSHUnits has no row for %q — the sshd start would render an empty unit name", id)
		}
		if _, ok := spec.DistroInits[id]; !ok {
			t.Errorf("DistroInits has no row for %q — the guest would fall through to no init system", id)
		}
	}
	// The three tables project the SAME struct, so a key in one and not the others means the
	// harvest diverged between fields.
	for _, tbl := range []struct {
		name string
		m    map[string]string
	}{
		{"DistroFormats", spec.DistroFormats},
		{"DistroSSHUnits", spec.DistroSSHUnits},
		{"DistroInits", spec.DistroInits},
	} {
		if len(tbl.m) != len(spec.DistroIDs) {
			t.Errorf("%s has %d rows but DistroIDs has %d — the harvest diverged between trait fields",
				tbl.name, len(tbl.m), len(spec.DistroIDs))
		}
	}
}

// The CUE def constrains each trait to a closed set; this asserts the GENERATED values
// landed inside it, which is what the rest of the codebase switches on.
func TestGeneratedDistroTraitsAreInTheirVocabularies(t *testing.T) {
	formats := map[string]bool{"rpm": true, "deb": true, "pac": true, "apk": true}
	units := map[string]bool{"ssh": true, "sshd": true}
	inits := map[string]bool{"systemd": true, "openrc": true}

	for id, f := range spec.DistroFormats {
		if !formats[f] {
			t.Errorf("DistroFormats[%q] = %q, not one of rpm/deb/pac/apk", id, f)
		}
	}
	for id, u := range spec.DistroSSHUnits {
		if !units[u] {
			t.Errorf("DistroSSHUnits[%q] = %q, not one of ssh/sshd", id, u)
		}
	}
	for id, i := range spec.DistroInits {
		if !inits[i] {
			t.Errorf("DistroInits[%q] = %q, not one of systemd/openrc", id, i)
		}
	}
	// OpenRC must be reachable from the data, not from a branch on a distro name: if this
	// ever comes out empty, something has re-hardcoded the Alpine special case.
	var openrc []string
	for id, i := range spec.DistroInits {
		if i == "openrc" {
			openrc = append(openrc, id)
		}
	}
	if len(openrc) == 0 {
		t.Error("no distro declares init: openrc — OpenRC support would only exist as a hardcoded branch")
	}
}

// FormatForDistroID reads the generated table; an unknown id must resolve to "" so callers
// can distinguish "no format" from a wrong one.
func TestFormatForDistroID_UsesTheGeneratedTable(t *testing.T) {
	for _, id := range spec.DistroIDs {
		if FormatForDistroID(id) != spec.DistroFormats[id] {
			t.Errorf("FormatForDistroID(%q) disagrees with the generated table", id)
		}
	}
	if got := FormatForDistroID("definitely-not-a-distro"); got != "" {
		t.Errorf("FormatForDistroID on an unknown id = %q, want \"\"", got)
	}
}
