package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// validate_fixture_test.go — the (c') converted-test infrastructure (task #60): the validate ENGINE
// moved to candy/plugin-box, so its former in-memory `Validate(cfg, layers, dir, opts)` unit tests are
// re-expressed as tiny ON-DISK fixture projects driven through the REAL pipeline (load → project →
// plugin rule) via validateProjectForBuild — the compiled-in command:validate dispatch (RDD-proven the
// dispatch works in-process). Each converted test writes a fixture + asserts the SAME diagnostic the
// synthetic unit pinned; the scenario-mapping table (scratchpad/k3d-scenario-mapping.md) is the
// no-net-loss audit. HOST-NATURAL rules that STAY in charly/ core (validateBuildAndDistro,
// validateBuilderRefs, …) are exercised by a DIRECT call to the kept host function, not a fixture.
//
// Assertion note: the new path may word a diagnostic slightly differently than the former synthetic
// unit (e.g. the copr/repo section label is now `distro.<name>.copr`, not `rpm.copr`, and the ADE +
// version rules are surfaced by CUE-conformance / the tolerant projector). Every substring asserted
// below was captured from the ACTUAL `charly box validate` output on the fixture — never a guess.

// writeValidateFixture writes a project tree (relative-path → file body) under a fresh temp dir and
// returns it. Keys are charly.yml + candy/<name>/charly.yml (the authored form of the former synthetic
// cfg/layers structs), so the fixture goes through the real loader.
func writeValidateFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fixtureGoodBox is the minimal valid box a broken-fixture variant mutates: a resolvable rpm box
// composing candy/mycandy. Prepend the `version:` + `discover:` header so the loader scans candy/.
const fixtureGoodBox = `version: 2026.203.2359
discover:
  - path: candy
    recursive: true
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      A minimal valid box for the validate fixture tests.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [mycandy]
    plan:
      - check: the box image exists
        command: "true"
        context: [build]
`

// fixtureGoodCandy is the minimal valid candy the good box composes.
const fixtureGoodCandy = `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      A minimal valid candy.
    package: [curl]
    plan:
      - check: curl present
        command: "command -v curl"
        context: [build]
`

// fx writes a project = fixtureGoodBox (composing `mycandy`) + a caller-supplied `mycandy` candy body,
// and returns the dir. Most candy-scoped rule tests only need to vary the one candy.
func fx(t *testing.T, mycandyBody string) string {
	t.Helper()
	return writeValidateFixture(t, map[string]string{
		"charly.yml":               fixtureGoodBox,
		"candy/mycandy/charly.yml": mycandyBody,
	})
}

// mustValidateErr runs the real validate gate over dir and fails unless the error contains every
// substring in want.
func mustValidateErr(t *testing.T, dir string, want ...string) {
	t.Helper()
	err := validateProjectForBuild(dir, ResolveOpts{})
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("validation error missing %q; got: %v", w, err)
		}
	}
}

// mustValidateOK runs the real validate gate over dir and fails if it reports any error.
func mustValidateOK(t *testing.T, dir string) {
	t.Helper()
	if err := validateProjectForBuild(dir, ResolveOpts{}); err != nil {
		t.Fatalf("expected no validation error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Success / happy-path fixtures
// ---------------------------------------------------------------------------

// TestValidate_Success ← TestValidateSuccess. A minimal resolvable box+candy validates clean.
func TestValidate_Success(t *testing.T) {
	mustValidateOK(t, writeValidateFixture(t, map[string]string{
		"charly.yml":               fixtureGoodBox,
		"candy/mycandy/charly.yml": fixtureGoodCandy,
	}))
}

// ---------------------------------------------------------------------------
// Candy-reference / composition rules (validateCandyReferences / …Includes — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_MissingCandy ← TestValidateMissingCandy. A box referencing a candy that does not exist.
func TestValidate_MissingCandy(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [nonexistent]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, `candy "nonexistent" not found`)
}

// TestValidate_MissingCandyTypo ← TestValidateMissingCandyWithTypo. A close (typo) name suggests a fix.
func TestValidate_MissingCandyTypo(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [pixie]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/pixi/charly.yml": `pixi:
  candy:
    version: 2026.194.1200
    description: |-
      pixi.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "did you mean")
}

// TestValidate_UnknownDependency ← TestValidateUnknownDependency. A candy `require:` naming a
// nonexistent candy.
func TestValidate_UnknownDependency(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    require: [unknown]
    plan: [{check: x, command: "true", context: [build]}]`), "unknown candy")
}

// TestValidate_CandyIncludesNoInstallFiles ← TestValidateCandyWithIncludesNoInstallFiles. A pure
// composition candy (only `candy:`) is legitimately content-less.
func TestValidate_CandyIncludesNoInstallFiles(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [sway-desktop]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/pipewire/charly.yml": `pipewire:
  candy:
    version: 2026.194.1200
    description: |-
      pw.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/wayvnc/charly.yml": `wayvnc:
  candy:
    version: 2026.194.1200
    description: |-
      vnc.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/sway-desktop/charly.yml": `sway-desktop:
  candy:
    version: 2026.194.1200
    description: |-
      composes pipewire + wayvnc, ships no install files of its own.
    candy: [pipewire, wayvnc]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_CandyIncludesCycle ← TestValidateCandyIncludesCycle. Circular `candy:` composition.
func TestValidate_CandyIncludesCycle(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [a]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/a/charly.yml": `a:
  candy:
    version: 2026.194.1200
    description: |-
      a.
    candy: [b]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/b/charly.yml": `b:
  candy:
    version: 2026.194.1200
    description: |-
      b.
    candy: [a]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "circular candy composition")
}

// TestValidate_CandyIncludesMissing ← TestValidateCandyIncludesMissing. A `candy:` naming a nonexistent
// candy.
func TestValidate_CandyIncludesMissing(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    candy: [nonexistent]
    plan: [{check: x, command: "true", context: [build]}]`), "unknown candy")
}

// ---------------------------------------------------------------------------
// Candy contents / ADE / install-file rules (validateCandyContents — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_CandyNoInstallFiles ← TestValidateCandyNoInstallFiles. A candy with only a check plan
// (no packages / composition / plugin / data) ships no install files.
func TestValidate_CandyNoInstallFiles(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    plan: [{check: x, command: "true", context: [build]}]`), "must have at least one install file")
}

// TestValidate_CargoWithoutSrc ← TestValidateCargoWithoutSrc. A Cargo.toml candy with no src/ dir.
func TestValidate_CargoWithoutSrc(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml":               fixtureGoodBox,
		"candy/mycandy/charly.yml": fixtureGoodCandy,
		"candy/mycandy/Cargo.toml": "[package]\nname = \"x\"\n",
	})
	mustValidateErr(t, dir, "requires src/")
}

// TestValidate_CandyMissingVersion ← TestValidateCandyMissingVersion. A local candy with no version:
// fails the CUE-conformance gate (the mandatory-CalVer rule is #Candy-enforced, host-natural).
func TestValidate_CandyMissingVersion(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    description: |-
      c.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`), `candy "mycandy"`, "version")
}

// ---------------------------------------------------------------------------
// Volume / alias rules (validateVolume / validateAliases — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_VolumesValid ← TestValidateVolumesValid.
func TestValidate_VolumesValid(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    volume:
      - {name: data, path: ~/.myapp}
    plan: [{check: x, command: "true", context: [build]}]`))
}

// TestValidate_VolumesDuplicate ← TestValidateVolumesDuplicate.
func TestValidate_VolumesDuplicate(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    volume:
      - {name: data, path: /d1}
      - {name: data, path: /d2}
    plan: [{check: x, command: "true", context: [build]}]`), `duplicate volume name "data"`)
}

// TestValidate_AliasesValid ← TestValidateAliasesValid (candy + box aliases both valid).
func TestValidate_AliasesValid(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [svc]
    alias:
      - {name: mycli, command: mycli-bin}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/svc/charly.yml": `svc:
  candy:
    version: 2026.194.1200
    description: |-
      svc.
    package: [curl]
    alias:
      - {name: svc-cli, command: svc-cli-bin}
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_AliasesDuplicate ← TestValidateAliasesDuplicate (candy-level).
func TestValidate_AliasesDuplicate(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    alias:
      - {name: mycli, command: cmd1}
      - {name: mycli, command: cmd2}
    plan: [{check: x, command: "true", context: [build]}]`), "duplicate alias name")
}

// TestValidate_AliasesInvalidName ← TestValidateAliasesInvalidName.
func TestValidate_AliasesInvalidName(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    alias:
      - {name: "-bad", command: cmd}
    plan: [{check: x, command: "true", context: [build]}]`), "must match")
}

// TestValidate_ImageAliasesDuplicate ← TestValidateImageAliasesDuplicate (box-level aliases).
func TestValidate_ImageAliasesDuplicate(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [svc]
    alias:
      - {name: mycli, command: cmd1}
      - {name: mycli, command: cmd2}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/svc/charly.yml": `svc:
  candy:
    version: 2026.194.1200
    description: |-
      svc.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "duplicate alias name")
}

// ---------------------------------------------------------------------------
// Package-section rules (validatePkgConfig — plugin-box). NOTE: package sections are authored under
// `distro:` blocks, so the diagnostic labels are `distro.<name>.copr` / `distro.<name>.repo` — NOT
// the former synthetic `rpm.copr` (asserted from the real output). The `modules` variant is an
// ENVELOPE-UNIT test in candy/plugin-box (a real `distro.<name>.module` produces Raw key "module",
// which the rule — checking "modules" — never matches; see k3d-scenario-mapping.md).
// ---------------------------------------------------------------------------

// TestValidate_CoprWithoutPackages ← TestValidateCoprWithoutPackages.
func TestValidate_CoprWithoutPackages(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    distro:
      fedora:
        copr: [owner/project]
    plan: [{check: x, command: "true", context: [build]}]`), "copr requires packages")
}

// TestValidate_ReposWithoutPackages ← TestValidateReposWithoutPackages.
func TestValidate_ReposWithoutPackages(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    distro:
      fedora:
        repo:
          - {name: test, url: "http://example.com"}
    plan: [{check: x, command: "true", context: [build]}]`), "repo requires packages")
}

// ---------------------------------------------------------------------------
// Builder DETECTION rules (validateBuilders — plugin-box). The candy-needs-builder DETECTION half
// moved to the plugin; the authored builder-REFERENCE half (validateBuilderRefs) stays host (below).
// ---------------------------------------------------------------------------

// TestValidate_AurWithoutAurBuilder ← TestValidateAurWithoutAurBuilder.
func TestValidate_AurWithoutAurBuilder(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
arch-img:
  candy:
    version: 2026.194.1200
    description: |-
      arch.
    base: docker.io/library/archlinux:latest
    build: [pac, aur]
    candy: [aur-layer]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/aur-layer/charly.yml": `aur-layer:
  candy:
    version: 2026.194.1200
    description: |-
      aur.
    distro:
      arch:
        aur:
          package: [yay-bin]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "no builder.aur configured")
}

// TestValidate_AurOnFedoraImageNoError ← TestValidateAurOnFedoraImageNoError. A Fedora image
// (build=[rpm]) consuming a multi-distro candy (rpm:+aur:) must NOT require builder.aur — the IR
// compiler skips the aur section entirely.
func TestValidate_AurOnFedoraImageNoError(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
fedora-img:
  candy:
    version: 2026.194.1200
    description: |-
      fedora.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [multi]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/multi/charly.yml": `multi:
  candy:
    version: 2026.194.1200
    description: |-
      multi-distro (rpm + aur).
    distro:
      fedora:
        package: [google-chrome-stable]
      arch:
        aur:
          package: [google-chrome]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	if err := validateProjectForBuild(dir, ResolveOpts{}); err != nil && strings.Contains(err.Error(), "no builder.aur configured") {
		t.Fatalf("Fedora image (build=[rpm]) must not require builder.aur; got: %v", err)
	}
}

// TestValidate_AurOnArchImageWithoutAurInBuildFormats ← TestValidateAurOnArchImageWithoutAurInBuildFormats.
// An Arch image with build=[pac] (no aur) consuming an aur candy: the IR compiler skips aur, so the
// validator must too.
func TestValidate_AurOnArchImageWithoutAurInBuildFormats(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
arch-pac-only:
  candy:
    version: 2026.194.1200
    description: |-
      arch pac-only.
    base: docker.io/library/archlinux:latest
    build: [pac]
    candy: [aur-layer]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/aur-layer/charly.yml": `aur-layer:
  candy:
    version: 2026.194.1200
    description: |-
      aur.
    distro:
      arch:
        aur:
          package: [yay-bin]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	if err := validateProjectForBuild(dir, ResolveOpts{}); err != nil && strings.Contains(err.Error(), "no builder.aur configured") {
		t.Fatalf("Arch image build=[pac] (no aur) must not require builder.aur; got: %v", err)
	}
}

// TestValidate_PixiBuilderUnconditional ← TestValidatePixiBuilderUnconditional. A pixi.toml candy
// requires builder.pixi regardless of the image's build formats (detect_files, not detect_config).
func TestValidate_PixiBuilderUnconditional(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
fedora-img:
  candy:
    version: 2026.194.1200
    description: |-
      fedora.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [pixi-layer]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/pixi-layer/charly.yml": `pixi-layer:
  candy:
    version: 2026.194.1200
    description: |-
      pixi.
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/pixi-layer/pixi.toml": "[project]\nname = \"x\"\n",
	})
	mustValidateErr(t, dir, "no builder.pixi configured")
}

// ---------------------------------------------------------------------------
// DAG-cycle rules (validateBoxDAG / validateCandyDAG — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_ImageCycle ← TestValidateImageCycle. A box base cycle a→b→c→a.
func TestValidate_ImageCycle(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
a:
  candy:
    version: 2026.194.1200
    description: |-
      a.
    base: b
    build: [rpm]
    plan: [{check: x, command: "true", context: [build]}]
b:
  candy:
    version: 2026.194.1200
    description: |-
      b.
    base: c
    build: [rpm]
    plan: [{check: x, command: "true", context: [build]}]
c:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    base: a
    build: [rpm]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "cycle")
}

// TestValidate_CandyCycle ← TestValidateCandyCycle. A candy `require:` cycle a→b→c→a.
func TestValidate_CandyCycle(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [a]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/a/charly.yml": `a:
  candy:
    version: 2026.194.1200
    description: |-
      a.
    package: [curl]
    require: [b]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/b/charly.yml": `b:
  candy:
    version: 2026.194.1200
    description: |-
      b.
    package: [curl]
    require: [c]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/c/charly.yml": `c:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    require: [a]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "cycle")
}

// ---------------------------------------------------------------------------
// Multiple-error collection + disabled-image skip
// ---------------------------------------------------------------------------

// TestValidate_MultipleErrors ← TestValidateMultipleErrors. Validation collects errors, not
// fail-first: two missing candies + one duplicate volume all surface together.
func TestValidate_MultipleErrors(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [mycandy, missing1, missing2]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/mycandy/charly.yml": `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    volume:
      - {name: data, path: /d1}
      - {name: data, path: /d2}
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, "validation errors", "missing1", "missing2", `duplicate volume name "data"`)
}

// TestValidate_SkipsDisabledImages ← TestValidateSkipsDisabledImages. A disabled box's rule-level
// problems (a missing candy) are skipped; --include-disabled would surface them.
func TestValidate_SkipsDisabledImages(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
good:
  candy:
    version: 2026.194.1200
    description: |-
      good.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [pixi]
    plan: [{check: x, command: "true", context: [build]}]
bad-disabled:
  candy:
    version: 2026.194.1200
    description: |-
      bad.
    enabled: false
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [nonexistent-layer]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/pixi/charly.yml": `pixi:
  candy:
    version: 2026.194.1200
    description: |-
      pixi.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateOK(t, dir)
	// The problem is real — it surfaces under --include-disabled (proves the skip, not a false pass).
	if err := validateProjectForBuild(dir, ResolveOpts{IncludeDisabled: true}); err == nil ||
		!strings.Contains(err.Error(), `candy "nonexistent-layer" not found`) {
		t.Fatalf("--include-disabled should surface the disabled box's missing candy; got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Route (candy metadata — no dedicated rule; must not error) ← TestValidateRoute*
// ---------------------------------------------------------------------------

// TestValidate_RouteWithoutTraefik ← TestValidateRouteWithoutTraefik. A route is generic metadata;
// no traefik candy required.
func TestValidate_RouteWithoutTraefik(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    route: {host: svc.localhost, port: 8080}
    plan: [{check: x, command: "true", context: [build]}]`))
}

// TestValidate_RouteWithTraefik ← TestValidateRouteWithTraefik.
func TestValidate_RouteWithTraefik(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [traefik, svc]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/traefik/charly.yml": `traefik:
  candy:
    version: 2026.194.1200
    description: |-
      traefik.
    package: [curl]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/svc/charly.yml": `svc:
  candy:
    version: 2026.194.1200
    description: |-
      svc.
    package: [curl]
    route: {host: svc.localhost, port: 8080}
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateOK(t, dir)
}

// ---------------------------------------------------------------------------
// port_relay rules (validatePortRelay — plugin-box)
// ---------------------------------------------------------------------------

// portRelayBox builds a project whose box composes candy `svc` (+ optionally socat) so the
// box-level socat-requirement arm can be exercised.
func portRelayBox(withSocat bool) string {
	candies := "[svc]"
	if withSocat {
		candies = "[supervisord, socat, svc]"
	}
	return `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
mybox:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: ` + candies + `
    plan: [{check: x, command: "true", context: [build]}]`
}

// TestValidate_PortRelayValid ← TestValidatePortRelayValid.
func TestValidate_PortRelayValid(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": portRelayBox(true),
		"candy/supervisord/charly.yml": `supervisord:
  candy:
    version: 2026.194.1200
    description: |-
      supervisord.
    package: [supervisor]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/socat/charly.yml": `socat:
  candy:
    version: 2026.194.1200
    description: |-
      socat.
    package: [socat, iproute]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/svc/charly.yml": `svc:
  candy:
    version: 2026.194.1200
    description: |-
      svc.
    package: [curl]
    port: [9222]
    port_relay: [9222]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_PortRelayNotInPorts ← TestValidatePortRelayNotInPorts.
func TestValidate_PortRelayNotInPorts(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    port: [8080]
    port_relay: [9222]
    plan: [{check: x, command: "true", context: [build]}]`), "not declared in the candy's ports")
}

// TestValidate_PortRelayNoPorts ← TestValidatePortRelayNoPorts.
func TestValidate_PortRelayNoPorts(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    port_relay: [9222]
    plan: [{check: x, command: "true", context: [build]}]`), "no ports declared")
}

// TestValidate_PortRelayDuplicate ← TestValidatePortRelayDuplicate.
func TestValidate_PortRelayDuplicate(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    port: [9222]
    port_relay: [9222, 9222]
    plan: [{check: x, command: "true", context: [build]}]`), "duplicate port")
}

// TestValidate_PortRelayMissingSocat ← TestValidatePortRelayMissingSocat.
func TestValidate_PortRelayMissingSocat(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": portRelayBox(false),
		"candy/svc/charly.yml": `svc:
  candy:
    version: 2026.194.1200
    description: |-
      svc.
    package: [curl]
    port: [9222]
    port_relay: [9222]
    plan: [{check: x, command: "true", context: [build]}]`,
	})
	mustValidateErr(t, dir, `missing "socat" candy`)
}

// ---------------------------------------------------------------------------
// data-candy rules (validateDataCandies — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_DataEntryUnknownVolume ← TestValidateDataEntryUnknownVolume. A data entry naming a
// volume no candy in the box declares.
func TestValidate_DataEntryUnknownVolume(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
jupyter:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [jup, tmpl]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/jup/charly.yml": `jup:
  candy:
    version: 2026.194.1200
    description: |-
      jup.
    package: [curl]
    volume:
      - {name: workspace, path: ~/workspace}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/tmpl/charly.yml": `tmpl:
  candy:
    version: 2026.194.1200
    description: |-
      tmpl.
    data:
      - {src: data/notebooks, volume: workspae}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/tmpl/data/notebooks/.keep": "",
	})
	mustValidateErr(t, dir, "workspae", "not declared by any candy")
}

// TestValidate_DataEntryKnownVolume ← TestValidateDataEntryKnownVolume. The happy path: a data entry
// whose volume matches a declared volume in the box's candy chain validates clean.
func TestValidate_DataEntryKnownVolume(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
jupyter:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [jup, tmpl]
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/jup/charly.yml": `jup:
  candy:
    version: 2026.194.1200
    description: |-
      jup.
    package: [curl]
    volume:
      - {name: workspace, path: ~/workspace}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/tmpl/charly.yml": `tmpl:
  candy:
    version: 2026.194.1200
    description: |-
      tmpl.
    package: [curl]
    data:
      - {src: data/notebooks, volume: workspace}
    plan: [{check: x, command: "true", context: [build]}]`,
		"candy/tmpl/data/notebooks/.keep": "",
	})
	mustValidateOK(t, dir)
}

// ---------------------------------------------------------------------------
// secret/env dependency rules (validateEnvDeps / validateSecretDeps — plugin-box)
// ---------------------------------------------------------------------------

// TestValidate_SecretAcceptsHappyPath ← TestValidateSecretAcceptsHappyPath.
func TestValidate_SecretAcceptsHappyPath(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    secret_accept:
      - {name: OPENROUTER_API_KEY, description: "OpenRouter API key", key: "charly/api-key/openrouter"}
    plan: [{check: x, command: "true", context: [build]}]`))
}

// TestValidate_SecretAcceptsCollidesWithEnvAccepts ← TestValidateSecretAcceptsCollidesWithEnvAccepts.
func TestValidate_SecretAcceptsCollidesWithEnvAccepts(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    env_accept:
      - {name: OPENROUTER_API_KEY, description: plaintext}
    secret_accept:
      - {name: OPENROUTER_API_KEY, description: credential-backed}
    plan: [{check: x, command: "true", context: [build]}]`),
		"appears in both env_accepts and secret_accepts")
}

// TestValidate_SecretRequiresCollidesWithEnvRequires ← TestValidateSecretRequiresCollidesWithEnvRequires.
func TestValidate_SecretRequiresCollidesWithEnvRequires(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    env_require:
      - {name: WEBUI_ADMIN_PASSWORD, description: plaintext}
    secret_require:
      - {name: WEBUI_ADMIN_PASSWORD, description: credential-backed}
    plan: [{check: x, command: "true", context: [build]}]`),
		"appears in both env_requires and secret_requires")
}

// TestValidate_SecretAcceptsCollidesWithSecretRequires ← TestValidateSecretAcceptsCollidesWithSecretRequires.
func TestValidate_SecretAcceptsCollidesWithSecretRequires(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    secret_require:
      - {name: API_TOKEN, description: required}
    secret_accept:
      - {name: API_TOKEN, description: optional}
    plan: [{check: x, command: "true", context: [build]}]`),
		"appears in both secret_requires and secret_accepts")
}

// TestValidate_SecretCollidesWithEnvProvides ← TestValidateSecretCollidesWithEnvProvides.
func TestValidate_SecretCollidesWithEnvProvides(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    env_provide:
      API_TOKEN: "http://{{.ContainerName}}:8080/token"
    secret_accept:
      - {name: API_TOKEN, description: credential-backed}
    plan: [{check: x, command: "true", context: [build]}]`),
		"also appears in env_provides")
}

// TestValidate_SecretAcceptsInvalidSlug ← TestValidateSecretAcceptsInvalidSlug.
func TestValidate_SecretAcceptsInvalidSlug(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    secret_accept:
      - {name: _LEADING_UNDERSCORE, description: bad slug}
    plan: [{check: x, command: "true", context: [build]}]`),
		"invalid podman secret slug")
}

// ---------------------------------------------------------------------------
// run-step (task) rules (validateCandyTasks — plugin-box) ← tasks_test.go
// ---------------------------------------------------------------------------

// TestValidate_TaskCopyRequiresTo ← TestValidateCandyTasks_CopyRequiresTo.
func TestValidate_TaskCopyRequiresTo(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    plan:
      - run: build
        copy: foo
      - check: x
        command: "true"
        context: [build]`), "requires to:")
}

// TestValidate_TaskUnresolvedVar ← TestValidateCandyTasks_UnresolvedVar.
func TestValidate_TaskUnresolvedVar(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    plan:
      - run: build
        mkdir: "${UNDEFINED}/foo"
      - check: x
        command: "true"
        context: [build]`), "UNDEFINED")
}

// TestValidate_TaskReservedVarKey ← TestValidateCandyTasks_ReservedVarKey.
func TestValidate_TaskReservedVarKey(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    var:
      USER: ignored
    plan:
      - run: build
        command: "true"
      - check: x
        command: "true"
        context: [build]`), "reserved auto-export")
}

// TestValidate_TaskBuildOnlyAll ← TestValidateCandyTasks_BuildOnlyAll.
func TestValidate_TaskBuildOnlyAll(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    plan:
      - run: build
        build: pixi
      - check: x
        command: "true"
        context: [build]`), "not supported")
}

// TestValidate_TaskHappyPath ← TestValidateCandyTasks_HappyPath. A full valid run-op set validates
// clean (no task rule fires).
func TestValidate_TaskHappyPath(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml":            fixtureGoodBox,
		"candy/mycandy/wrapper": "#!/bin/sh\n",
		"candy/mycandy/charly.yml": `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    var:
      VERSION: "1.0"
    plan:
      - run: mkdir
        mkdir: /etc/foo
        run_as: root
      - run: copy
        copy: wrapper
        to: /etc/foo/bar
        mode: "0644"
        run_as: root
      - run: write
        write: /etc/baz.conf
        content: hello
        run_as: root
      - run: download
        download: "https://x.com/v${VERSION}/app.tar.gz"
        extract: tar.gz
        to: /usr/local/bin
        run_as: root
      - run: link
        link: /usr/local/bin/app-current
        target: /usr/local/bin/app
        run_as: root
      - run: setcap
        setcap: /usr/bin/foo
        caps: cap_setuid=ep
      - run: cmd
        command: "echo hello ${VERSION}"
        run_as: "${USER}"
      - check: x
        command: "true"
        context: [build]`,
	})
	mustValidateOK(t, dir)
}

// ---------------------------------------------------------------------------
// ADE plan-completeness (validateCandyContents — plugin-box) ← plan_unify_test.go
// ---------------------------------------------------------------------------

// TestValidate_RejectsNoCheckStep ← TestPlanUnify_ValidateRejectsNoCheckStep. A candy plan with a
// run: but no check: step fails the ADE gate.
func TestValidate_RejectsNoCheckStep(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      a candy with run: but no check:
    plan:
      - run: install
        command: "true"`), "at least one `check:` step")
}

// ---------------------------------------------------------------------------
// Op-level plan rules (validateOps / validateCheck — plugin-box) ← validate_check_test.go
// ---------------------------------------------------------------------------

// TestValidateOps_MultiVerbRejected ← the same. A step bearing two verbs is rejected by Kind().
func TestValidateOps_MultiVerbRejected(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: bad
        copy: /x
        mkdir: /tmp/d
        context: [build]`), "multiple verbs")
}

// TestValidateOps_RuntimeVarInBuildContext ← the same. A build-legal op referencing a runtime-only var.
func TestValidateOps_RuntimeVarInBuildContext(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: uses hostport
        command: "redis-cli -p ${HOST_PORT:6379}"`),
		"runtime-only variable", "HOST_PORT:6379")
}

// TestValidateOps_RuntimeVarInDeployContext ← the same. Pinned to deploy, the runtime var is legal.
func TestValidateOps_RuntimeVarInDeployContext(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: uses hostport
        command: "redis-cli -p ${HOST_PORT:6379}"
        context: [deploy]`))
}

// TestValidateOps_McpClean ← the same. Valid mcp method checks produce no error.
func TestValidateOps_McpClean(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: mcp ping
        mcp: {method: ping}
      - check: mcp list
        mcp: {method: list-tools}
      - check: mcp call
        mcp: {method: call, tool: list_notebooks, input: "{}"}
      - check: mcp read
        mcp: {method: read, uri: "file:///x"}`))
}

// TestValidateOps_RecordClean ← the same.
func TestValidateOps_RecordClean(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: rec list
        record: {method: list}
      - check: rec start
        record: {method: start, record_mode: terminal}
      - check: rec cmd
        record: {method: cmd, text: "echo hi"}
      - check: rec stop
        record: {method: stop, artifact: /tmp/demo.cast, artifact_min_bytes: 100}`))
}

// TestValidateOps_SpiceClean ← the same.
func TestValidateOps_SpiceClean(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: sp status
        spice: {method: status}
      - check: sp shot
        spice: {method: screenshot, artifact: /tmp/s.png}
      - check: sp type
        spice: {method: type, text: hi}
      - check: sp key
        spice: {method: key, key: Return}`))
}

// TestValidateOps_LibvirtClean ← the same.
func TestValidateOps_LibvirtClean(t *testing.T) {
	mustValidateOK(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: lv list
        libvirt: {method: list}
      - check: lv info
        libvirt: {method: info}
      - check: lv shot
        libvirt: {method: screenshot, artifact: /tmp/v.png}
      - check: lv ping
        libvirt: {method: guest/ping}
      - check: lv exec
        libvirt: {method: guest/exec, command: "uname -r"}
      - check: lv snap
        libvirt: {method: snapshot/create, target: pre-upgrade}
      - check: lv qmp
        libvirt: {method: qmp, text: query-status}
      - check: lv key
        libvirt: {method: send-key, key: "ctrl alt F2"}`))
}

// TestValidateOps_Clean ← the same. A full valid candy plan + box plan produces no error.
func TestValidateOps_Clean(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.203.2359
discover: [{path: candy, recursive: true}]
redis-ml:
  candy:
    version: 2026.194.1200
    description: |-
      box.
    base: quay.io/fedora/fedora:43
    build: [rpm]
    candy: [redis]
    plan:
      - check: version
        id: version
        command: "redis-server --version"
      - check: routed
        id: routed
        http:
          http: "https://${DNS}/health"
          status: 200`,
		"candy/redis/charly.yml": `redis:
  candy:
    version: 2026.194.1200
    description: |-
      redis.
    package: [redis]
    plan:
      - check: bin
        file:
          file: /usr/bin/redis-server
          exists: true
          mode: "0755"
      - check: port
        port:
          port: 6379
          listening: true
      - check: ping
        command: "redis-cli -p ${HOST_PORT:6379} ping"
        context: [deploy]`,
	})
	mustValidateOK(t, dir)
}

// TestValidateOps_LowercaseCheckVarInClusterField ← the same. A lowercase ${...} in a kube identifier
// field never resolves (the expander is UPPERCASE-only).
func TestValidateOps_LowercaseCheckVarInClusterField(t *testing.T) {
	bad := fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: kube addons
        kube: {method: addons, cluster: "${deploy_name}"}
        context: [deploy]`)
	mustValidateErr(t, bad, "UPPERCASE", "${deploy_name}")

	ok := fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    package: [curl]
    plan:
      - check: kube addons
        kube: {method: addons, cluster: "${DEPLOY_NAME}"}
        context: [deploy]`)
	if err := validateProjectForBuild(ok, ResolveOpts{}); err != nil && strings.Contains(err.Error(), "UPPERCASE") {
		t.Fatalf("uppercase check var should pass: %v", err)
	}
}

// TestValidateOps_RejectsRuntimeOnlyActInBuild ← install_act_test.go's TestValidateOps_RejectsRuntimeOnlyActInBuild.
// A build-context run: step whose verb has no build/deploy install path (addr) is rejected.
func TestValidateOps_RejectsRuntimeOnlyActInBuild(t *testing.T) {
	mustValidateErr(t, fx(t, `mycandy:
  candy:
    version: 2026.194.1200
    description: |-
      c.
    plan:
      - run: reach x
        addr: "127.0.0.1:80"
        context: [build]
      - check: x
        command: "true"
        context: [build]`), "cannot act")
}

// ---------------------------------------------------------------------------
// HOST-NATURAL rules — these STAY in charly/ core; the converted test calls the KEPT host function
// DIRECTLY (a projection cannot carry the raw authored config they read). testDistroConfig() /
// testBuilderCfg() load the testdata build vocabulary (fedora+arch+debian+ubuntu distros, pixi
// builder) — the same LoadBuildConfigForBox path Validate() used to feed these rules.
// ---------------------------------------------------------------------------

// TestValidateBuildAndDistro_InvalidPkg ← TestValidateInvalidPkg. A build format not in the vocabulary.
func TestValidateBuildAndDistro_InvalidPkg(t *testing.T) {
	cfg := &Config{Defaults: spec.BoxConfig{Build: BuildFormats{"invalid"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not valid") {
		t.Errorf("want 'is not valid', got: %v", errs.Errors)
	}
}

// TestValidateBuildAndDistro_InvalidPkgValue ← TestValidateInvalidPkgValue.
func TestValidateBuildAndDistro_InvalidPkgValue(t *testing.T) {
	cfg := &Config{Defaults: spec.BoxConfig{Build: BuildFormats{"zypper"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not valid") {
		t.Errorf("want 'is not valid', got: %v", errs.Errors)
	}
}

// TestValidateBuildAndDistro_PacValid ← TestValidatePacPkgValue. `pac` is a valid vocabulary format.
func TestValidateBuildAndDistro_PacValid(t *testing.T) {
	cfg := &Config{Defaults: spec.BoxConfig{Build: BuildFormats{"pac"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if errs.HasErrors() {
		t.Errorf("pac should be valid, got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_SelfBuilder ← TestValidateSelfBuilder. A per-image builder referencing self.
func TestValidateBuilderRefs_SelfBuilder(t *testing.T) {
	cfg := &Config{
		Defaults: spec.BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myimg": {Candy: []string{"pixi"}, Builder: buildkit.BuilderMap{"pixi": "myimg"}},
		}),
	}
	errs := &ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "cannot reference self") {
		t.Errorf("want 'cannot reference self', got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_InheritedSelfNotError ← TestValidateBuilderInheritedSelfNotError. A builder
// image inheriting defaults.builder that points to itself is NOT an error.
func TestValidateBuilderRefs_InheritedSelfNotError(t *testing.T) {
	cfg := &Config{
		Defaults: spec.BoxConfig{Build: BuildFormats{"rpm"}, Builder: buildkit.BuilderMap{"pixi": "builder", "npm": "builder"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"builder": {Candy: []string{"pixi"}},
		}),
	}
	errs := &ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if errs.HasErrors() {
		t.Errorf("inherited self-builder should not error, got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_PerImageNotFound ← TestValidatePerImageBuilderNotFound.
func TestValidateBuilderRefs_PerImageNotFound(t *testing.T) {
	cfg := &Config{
		Defaults: spec.BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"app": {Candy: []string{"pixi"}, Builder: buildkit.BuilderMap{"pixi": "nonexistent"}},
		}),
	}
	errs := &ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not found") {
		t.Errorf("want 'is not found', got: %v", errs.Errors)
	}
}
