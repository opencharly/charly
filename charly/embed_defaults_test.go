package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedDefaults_SchemaConformance proves the node-form embedded defaults
// validate against the unified #NodeDoc schema (per-arm closedness + kind-narrowed
// children) — the SAME validate-before-execute gate every project charly.yml
// passes through the loader. This is the canonical schema gate for the embedded
// build vocabulary + sidecar templates (charly.yml).
func TestEmbeddedDefaults_SchemaConformance(t *testing.T) {
	data, err := os.ReadFile("charly.yml")
	if err != nil {
		t.Fatalf("read embedded defaults: %v", err)
	}
	if err := validateNodeDocCUE("charly.yml", data); err != nil {
		t.Errorf("embedded defaults fail #NodeDoc validation:\n%v", err)
	}
}

// TestEmbeddedBuildDefaults_NoBuildYml proves a project with NO build.yml import
// still resolves the default distro/builder vocabulary from the binary embed.
func TestEmbeddedBuildDefaults_NoBuildYml(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
defaults:
  registry: ghcr.io/example
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// distro/builder are plugin kinds now; read the vocab back via the accessors.
	distros, builders := uf.Distros(), uf.Builders()
	for _, d := range []string{"fedora", "arch"} {
		if distros[d] == nil {
			t.Errorf("embedded distro %q missing with no build.yml import", d)
		}
	}
	if builders["pixi"] == nil {
		t.Error("embedded builder pixi missing with no build.yml import")
	}
}

// TestEmbeddedBuildDefaults_ProjectWins proves the base/overlay precedence: a
// project that redefines an embedded distro OVERRIDES it (project wins), a NEW
// distro EXTENDS the vocabulary, and untouched embedded distros remain. This is
// the high-risk RDD assertion — the embed must be the base, the project the
// overlay that wins.
func TestEmbeddedBuildDefaults_ProjectWins(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
fedora:
  distro:
    version: "99"
    bootstrap:
      install_cmd: custom-fedora
mydistro:
  distro:
    bootstrap:
      install_cmd: custom-mine
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// distro is a plugin kind now; read the vocab back via the Distros() accessor.
	distros := uf.Distros()
	// Override: the project's fedora (version "99") WINS over the embedded fedora
	// (version "43"). The marker is a valid #Distro.version (numeric per the schema
	// regex) — the legacy "marker99" was rejected by the node-form load gate.
	if distros["fedora"] == nil || distros["fedora"].Version != "99" {
		t.Errorf("project fedora override lost (embed wrongly won); got %+v", distros["fedora"])
	}
	// Extend: the project's new distro coexists with the embedded ones.
	if distros["mydistro"] == nil {
		t.Error("project-added distro mydistro missing")
	}
	// Untouched: an embedded distro the project didn't mention is still present.
	if distros["arch"] == nil {
		t.Error("embedded distro arch missing (embed not applied as base)")
	}
}

// TestEmbeddedDefaults_SameLoaderPath proves the consolidation invariant: the
// SINGLE binary-embedded charly.yml is compiled + parsed by the SAME unified
// loader core (embeddedDefaults → mergeUnifiedDocs (node-form)) and yields
// BOTH the build vocabulary
// AND the sidecar-template library from one parse — no bespoke per-section
// loader. This is the core "parse its own charly.yml with exactly the same code
// path" guarantee.
func TestEmbeddedDefaults_SameLoaderPath(t *testing.T) {
	def, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	// Build vocabulary view — distro/builder/resource are plugin kinds now, read back
	// via the accessors (over def.PluginKinds) from the SAME parse.
	distros := def.Distros()
	for _, d := range []string{"fedora", "arch"} {
		if distros[d] == nil {
			t.Errorf("embedded distro %q missing from unified parse", d)
		}
	}
	if def.Builders()["pixi"] == nil {
		t.Error("embedded builder pixi missing from unified parse")
	}
	if def.Resources()["nvidia-gpu"] == nil {
		t.Error("embedded resource nvidia-gpu missing from unified parse")
	}
	// Sidecar-template view — sidecar is a plugin kind (candy/plugin-sidecar); the
	// embedded tailscale template is an OPAQUE body in def.PluginKinds["sidecar"]
	// from the SAME parse, the SAME UnifiedFile.
	body, ok := def.PluginKinds["sidecar"]["tailscale"]
	if !ok {
		t.Fatal("embedded sidecar tailscale missing from unified parse")
	}
	if img := sidecarBodyImage(t, body); img != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("tailscale sidecar image = %q, want ghcr.io/tailscale/tailscale:latest", img)
	}
}

// TestEmbeddedDefaults_SidecarProjectWins proves a project may declare a root
// `sidecar:` template that OVERRIDES the embedded one (project-wins), routed
// through LoadUnified. This is the load-bearing behavior the sidecar→plugin
// extraction relies on: sidecar is a plugin kind now, so the project's `tailscale`
// lands in uf.PluginKinds["sidecar"]["tailscale"] and the embedded one (merged in by
// applyEmbeddedDefaults via the generic root-wins mergePluginKindsMap — Cutover A's
// name-keyed override) fills only absent names — so the project's value WINS, ONE
// entry, not two appended.
func TestEmbeddedDefaults_SidecarProjectWins(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
tailscale:
  sidecar:
    image: example.com/custom-tailscale:pinned
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	sidecars := uf.PluginKinds["sidecar"]
	if n := len(sidecars); n != 1 {
		t.Fatalf("expected exactly 1 tailscale entry after root-wins override, got %d (%v)", n, sidecars)
	}
	if img := sidecarBodyImage(t, sidecars["tailscale"]); img != "example.com/custom-tailscale:pinned" {
		t.Errorf("project sidecar override lost (embed wrongly won); got %q", img)
	}
}

// TestEmbeddedDefaults_AllVocabKindsOverridable proves Req #2 for the three
// vocabulary kinds not already covered by TestEmbeddedBuildDefaults_ProjectWins
// (distro) and TestEmbeddedDefaults_SidecarProjectWins (sidecar): builder, init,
// and resource each stay project-charly.yml OVERRIDABLE (project wins wholesale
// via the gap-filling applyEmbeddedDefaults merge) AND EXTENDABLE (a new entry
// coexists), while untouched embedded entries survive — independent of the
// embedded config now being node-form YAML.
func TestEmbeddedDefaults_AllVocabKindsOverridable(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
pixi:
  builder:
    detect_config: marker99
mybuilder:
  builder:
    detect_config: custom
systemd:
  init:
    model: file_copy
myinit:
  init:
    model: file_copy
nvidia-gpu:
  resource:
    gpu:
      vendor: marker-vendor
amd-gpu:
  resource:
    gpu:
      vendor: "0x1002"
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}

	// builder/init/resource are plugin kinds now; read each vocab back via its accessor.
	builders, inits, resources := uf.Builders(), uf.Inits(), uf.Resources()

	// builder: override wins WHOLESALE (gap-fill replaces, never deep-merges), a
	// new entry coexists, an untouched embedded entry survives.
	if b := builders["pixi"]; b == nil || b.DetectConfig != "marker99" {
		t.Errorf("builder pixi override lost (embed wrongly won); got %+v", builders["pixi"])
	}
	if b := builders["pixi"]; b != nil && len(b.DetectFiles) != 0 {
		t.Errorf("builder pixi override must replace wholesale (embed DetectFiles must be gone); got %+v", b.DetectFiles)
	}
	if builders["mybuilder"] == nil {
		t.Error("project-added builder mybuilder missing")
	}
	if builders["cargo"] == nil {
		t.Error("embedded builder cargo missing (embed not applied as base)")
	}

	// init
	if i := inits["systemd"]; i == nil || i.Model != "file_copy" {
		t.Errorf("init systemd override lost; got %+v", inits["systemd"])
	}
	if inits["myinit"] == nil {
		t.Error("project-added init myinit missing")
	}
	if inits["supervisord"] == nil {
		t.Error("embedded init supervisord missing")
	}

	// resource
	if r := resources["nvidia-gpu"]; r == nil || r.Gpu == nil || r.Gpu.Vendor != "marker-vendor" {
		t.Errorf("resource nvidia-gpu override lost; got %+v", resources["nvidia-gpu"])
	}
	if resources["amd-gpu"] == nil {
		t.Error("project-added resource amd-gpu missing")
	}
}

// TestProjectVocabOverride_IsSchemaValidated proves the Req #2 boundary: a
// project's vocabulary override is validated against the SAME #Kind schemas as
// the embedded defaults (validateVocabularyCollections — the shared helper). An
// unknown key (typo) in a project builder override is rejected by the closed
// #Builder, exactly as it would be in the embedded charly.yml.
func TestProjectVocabOverride_IsSchemaValidated(t *testing.T) {
	proj := []byte(`version: ` + LatestSchemaVersion().String() + `
builder:
  badbuilder:
    bogus_field: true
`)
	doc, err := cueDocFromYAML("proj.yml", proj)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	var viol []string
	validateVocabularyCollections(doc, []string{"builder"}, "proj.yml",
		func(format string, args ...any) { viol = append(viol, fmt.Sprintf(format, args...)) })
	if len(viol) == 0 {
		t.Error("expected closed #Builder to reject unknown key bogus_field in a project builder override")
	}
}

// TestNoHardcodedYAMLFilenames is the file-agnostic invariant guard. charly.yml
// is the ONE YAML filename the code knows. The binary-embedded default config is
// node-form YAML (//go:embed charly.yml, embed_defaults.go), parsed by the
// same unified loader as any project charly.yml. Outside migration code (which must name legacy
// files to migrate FROM) and tests, no source may hardcode a per-kind project
// filename — discovery + the UnifiedFileName constant cover them all. build.yml
// and sidecar.yml are now legacy filenames (named only in migration + tests);
// deploy.yml (per-machine host state) and charly.yml are deliberately NOT in the
// forbidden set.
func TestNoHardcodedYAMLFilenames(t *testing.T) {
	forbidden := regexp.MustCompile(`"(box|candy|base|vm|pod|k8s|check|local|android|image|images|layer)\.yml"`)
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range entries {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "migrate_") {
			continue // tests + migration are the sanctioned homes for legacy names
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if m := forbidden.FindString(line); m != "" {
				t.Errorf("%s:%d hardcodes per-kind filename %s — use discovery / UnifiedFileName:\n  %s",
					f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}
