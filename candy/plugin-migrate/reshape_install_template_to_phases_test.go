package migrate

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestInstallTemplateToPhases_MovesToPhaseContainer: the reshaper MOVES a
// `format:`/`builder:` def's direct `install_template:` scalar into
// `phase.install.container` across all four shapes — a def with NO phase block
// (fresh phase created at the old position), a def whose phase.install already
// carries a `host:` cell (container inserted beside it), a def that already has
// a `container:` cell (a partially-migrated config — the cell's value is replaced),
// and a per-entity builder def (`<entity>: {builder: {<def fields directly>}}`).
func TestInstallTemplateToPhases_MovesToPhaseContainer(t *testing.T) {
	m := migration{Name: "t", Apply: "installTemplateToPhases"}
	in := "" +
		"format:\n" +
		"  apk:\n" +
		"    # the apk container template\n" +
		"    install_template: |\n" +
		"      RUN apk add --no-cache\n" +
		"      {{- range .Packages}} {{.}}{{end}}\n" +
		"  pac:\n" +
		"    phase:\n" +
		"      install:\n" +
		"        host: |\n" +
		"          pacman -Syu --needed{{range .Packages}} {{.}}{{end}}\n" +
		"    install_template: |\n" +
		"      RUN pacman-key --recv-keys{{range .Keys}} {{.}}{{end}}\n" +
		"  rpm:\n" +
		"    phase:\n" +
		"      install:\n" +
		"        container: |\n" +
		"          RUN dnf install -y OLD\n" +
		"    install_template: |\n" +
		"      RUN dnf install -y NEW\n" +
		"builder:\n" +
		"  cargo:\n" +
		"    cache_mount:\n" +
		"      - dst: /tmp/cargo-cache\n" +
		"    install_template: |\n" +
		"      RUN cargo install --path /ctx\n" +
		"go:\n" +
		"  builder:\n" +
		"    detect_file:\n" +
		"      - go.mod\n" +
		"    install_template: |\n" +
		"      RUN go install ./...\n"
	out, changed := applyTransform(t, m, in)
	if !changed {
		t.Fatal("expected install_template fields to be moved")
	}
	// The retired key is gone entirely; every template body survives exactly once;
	// the partially-migrated rpm container cell was REPLACED (the OLD body is gone);
	// the pac host cell is untouched beside its new container sibling; the apk key's
	// head comment rides onto the new container cell.
	for _, want := range []string{
		"# the apk container template",  // the retired key's comment rides onto container
		"RUN apk add --no-cache",        // apk: fresh phase block, body preserved
		"pacman -Syu --needed",          // pac: host cell untouched beside the new container
		"RUN pacman-key --recv-keys",    // pac: container inserted
		"RUN dnf install -y NEW",        // rpm: existing container replaced
		"RUN cargo install --path /ctx", // builder def moved (defs-map shape)
		"RUN go install ./...",          // builder def moved (per-entity shape)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "install_template:") {
		t.Errorf("an install_template: key survived the move:\n%s", out)
	}
	if strings.Contains(out, "RUN dnf install -y OLD") {
		t.Errorf("the replaced rpm container cell's OLD value survived:\n%s", out)
	}
	// A second pass is a no-op (idempotent).
	if _, changed2 := applyTransform(t, m, out); changed2 {
		t.Error("second pass changed an already-migrated doc")
	}
}

// TestInstallTemplateToPhases_LeavesRetainedFields: the reshaper does NOT touch the
// RETAINED `install_template` uses — distro `local_pkg:`.install_template and
// distro `bootloader:`.install_template — because they sit under different parent
// keys than the format/builder defs.
func TestInstallTemplateToPhases_LeavesRetainedFields(t *testing.T) {
	m := migration{Name: "t", Apply: "installTemplateToPhases"}
	in := "" +
		"distro:\n" +
		"  debian:\n" +
		"    format:\n" +
		"      deb:\n" +
		"        local_pkg:\n" +
		"          install_template: apt-get install -y {{.Glob}}\n" +
		"          probe: command -v apt-get\n" +
		"    bootloader:\n" +
		"      install_template: |\n" +
		"        # bootloader content stays put\n"
	out, changed := applyTransform(t, m, in)
	if changed {
		t.Errorf("hook reported changed=true but only retained fields present:\n%s", out)
	}
	for _, want := range []string{
		"local_pkg:\n          install_template: apt-get install -y {{.Glob}}",
		"bootloader:\n      install_template: |",
		"# bootloader content stays put",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("retained field damaged, missing %q:\n%s", want, out)
		}
	}
}

// TestMigrationTable_InstallTemplateToPhases: the table carries the
// install_template → phase.install.container move as the 6th (newest) entry — a
// project-only (non-touches_host) apply: goHook entry, strictly after
// remove-candy-localpkg, with the hook registered.
func TestMigrationTable_InstallTemplateToPhases(t *testing.T) {
	m := migrationTable[5]
	if m.Name != "install-template-to-phases" || m.Apply != "installTemplateToPhases" || m.TouchesHost {
		t.Errorf("unexpected sixth table entry: %+v", m)
	}
	if _, ok := goHooks[m.Apply]; !ok {
		t.Errorf("hook %q not registered in goHooks", m.Apply)
	}
	if !migrationTable[4].Version.Less(m.Version) {
		t.Errorf("install-template-to-phases version %s must be strictly after remove-candy-localpkg %s", m.Version, migrationTable[4].Version)
	}
}

// TestEmbeddedVocabHasNoInstallTemplate is the read-only regression guard for the
// in-tree install_template → phase.install.container migration: the repo's embedded
// build vocabulary (charly/charly.yml) and build testdata (charly/testdata/build.yml)
// — the two configs whose format:/builder: defs carried the removed field — must
// already be migrated, so applying the reshaper in-memory is a NO-OP. The guard
// never writes; a residual format/builder-level `install_template:` in either file
// makes the reshaper report changed=true and fails the test. (The one-time
// write-back driver this replaces ran the same reshaper over both files with
// SetIndent(4) to produce the migrated configs.)
func TestEmbeddedVocabHasNoInstallTemplate(t *testing.T) {
	m := migration{Name: "t", Apply: "installTemplateToPhases"}
	for _, rel := range []string{"../../charly/charly.yml", "../../charly/testdata/build.yml"} {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		if _, changed := applyTransform(t, m, string(b)); changed {
			t.Errorf("%s: a format/builder-level install_template: survived the migration — re-run the reshaper", rel)
		}
	}
}
