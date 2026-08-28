package main

import (
	"fmt"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEmbeddedVocabHasNoFormatLevelInstallTemplate is the regression guard for the
// install_template → phase.install.container cutover: a format or builder def must carry
// its container install recipe in `phase.install.container`, the phase: block's single
// source of truth, never in the retired top-level `install_template:` field.
//
// It lives HERE, in the repo that owns these files. It used to live in
// candy/plugin-migrate as "the reshaper applied to charly's vocabulary is a no-op",
// reading `../../charly/charly.yml` — a path that resolved only while that plugin was a
// directory INSIDE this repo. The de-submodule left the path dangling, and nothing noticed,
// because that repo's CI runs `charly box validate` and never `go test`: the guard was not
// failing, it was never executing. Asserting the invariant directly, where the data is,
// needs neither the reshaper nor a cross-repo checkout.
//
// The check is deliberately SHAPE-scoped, not a text search: `install_template:` also
// appears legitimately at `format.<fmt>.local_pkg.install_template` and
// `distro.bootloader.install_template`, which are different fields that were never part of
// the cutover. A grep would flag all twelve of those.
func TestEmbeddedVocabHasNoFormatLevelInstallTemplate(t *testing.T) {
	for _, path := range []string{"charly.yml", "testdata/build.yml"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, where := range findFormatLevelInstallTemplate(&doc) {
			t.Errorf("%s:%s carries the retired top-level install_template: — it belongs in "+
				"phase.install.container", path, where)
		}
	}
}

// findFormatLevelInstallTemplate returns a "line N under <parent>" locator for every
// install_template: that sits DIRECTLY on a format or builder def, in either of the two
// shapes the vocabulary uses:
//
//	format:  {<name>: {install_template: …}}   // and builder: {<name>: …}
//	<entity>: {builder: {install_template: …}} // the per-entity shape
func findFormatLevelInstallTemplate(n *yaml.Node) []string {
	var out []string
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				switch key.Value {
				case "format", "builder":
					if val.Kind != yaml.MappingNode {
						break
					}
					// The per-entity shape puts the def's fields directly under
					// `builder:`; the defs-map shape puts them one level down.
					if ln := installTemplateChildLine(val, "install_template"); ln > 0 {
						out = append(out, fmt.Sprintf(" line %d under %s", ln, key.Value))
					}
					for j := 0; j+1 < len(val.Content); j += 2 {
						def := val.Content[j+1]
						if def.Kind != yaml.MappingNode {
							continue
						}
						if ln := installTemplateChildLine(def, "install_template"); ln > 0 {
							out = append(out, fmt.Sprintf(" line %d under %s.%s", ln, key.Value, val.Content[j].Value))
						}
					}
				}
				walk(val)
			}
		}
	}
	walk(n)
	return out
}

func installTemplateChildLine(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i].Line
		}
	}
	return 0
}

// TestFindFormatLevelInstallTemplate_Discriminates proves the guard above can actually
// FAIL. Without this, a finder that silently matched nothing — a renamed key, a walk that
// never descends — would make the guard read as coverage while asserting nothing at all.
//
// It also pins the two legitimate nestings the guard must NOT flag: charly.yml carries
// twelve `install_template:` occurrences today, and every one of them is one of these.
func TestFindFormatLevelInstallTemplate_Discriminates(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want int
	}{
		{"format def carrying the retired field", "" +
			"format:\n  apk:\n    install_template: |\n      RUN apk add x\n", 1},
		{"builder defs-map shape", "" +
			"builder:\n  cargo:\n    install_template: |\n      RUN cargo install\n", 1},
		{"per-entity builder shape", "" +
			"go:\n  builder:\n    install_template: |\n      RUN go install ./...\n", 1},
		{"both shapes at once", "" +
			"format:\n  apk:\n    install_template: x\n" +
			"go:\n  builder:\n    install_template: y\n", 2},

		// The legitimate nestings — a text search would flag all of these.
		{"format.<fmt>.local_pkg.install_template is a different field", "" +
			"format:\n  pac:\n    local_pkg:\n      install_template: |\n        RUN makepkg\n", 0},
		{"distro.bootloader.install_template is a different field", "" +
			"arch:\n  distro:\n    bootloader:\n      install_template: |\n        RUN grub-install\n", 0},
		{"the migrated shape is clean", "" +
			"format:\n  apk:\n    phase:\n      install:\n        container: |\n          RUN apk add x\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(tc.yaml), &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := findFormatLevelInstallTemplate(&doc)
			if len(got) != tc.want {
				t.Errorf("found %d retired fields, want %d: %v", len(got), tc.want, got)
			}
		})
	}
}
