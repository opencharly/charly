package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// builderTestImg builds a ResolvedBox carrying the four externalized builders in its BuilderConfig
// (pixi/npm/cargo by detect-file, aur by detect-config) with the given build formats — the gate the
// scoping fix turns on.
func builderTestImg(buildFormats ...string) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{
		Name:         "t",
		Home:         "/home/u",
		BuildFormats: buildFormats,
		BuilderConfig: &buildkit.BuilderConfig{Builder: map[string]*BuilderDef{
			"pixi":  {DetectFiles: []string{"pixi.toml"}},
			"npm":   {DetectFiles: []string{"package.json"}},
			"cargo": {DetectFiles: []string{"Cargo.toml"}},
			"aur":   {DetectConfig: "aur"},
		}},
	}
}

// aurCandy builds a spec.CandyReader fixture carrying an "aur" FormatSection with the given
// packages — the DetectConfig-builder trigger CandyNeedsBuilderStep reads via FormatSection.
func aurCandy(name string, pkgs ...string) spec.CandyReader {
	return testCandy(name,
		spec.CandyModel{FormatSections: map[string]spec.PackageSection{"aur": {FormatName: "aur", Packages: pkgs}}},
		spec.CandyView{},
	)
}

// TestDetectExternalizedBuilders_ScopedAndDistroGated is the regression gate for the C4 over-build:
// a deploy must surface ONLY the builders its resolved closure actually triggers, and a DetectConfig
// builder (aur) must be distro-gated — a fedora deploy never surfaces aur even when a multi-distro
// candy carries an aur: section. The pre-fix code (blanket file/section detection across the whole
// box scan, no distro gate) would FAIL these.
func TestDetectExternalizedBuilders_ScopedAndDistroGated(t *testing.T) {
	// (1) A pixi-only deploy on a FEDORA image surfaces ONLY pixi — not npm/cargo/aur. This is the
	// exact C4 scenario (check-jupyter-pod): jupyter has pixi.toml, nothing else.
	pixiOnly := map[string]spec.CandyReader{"jupyter": pixiCandy(t, "jupyter")}
	got := deploykit.DetectExternalizedBuilders([]string{"jupyter"}, pixiOnly, externalizedBuilders, builderTestImg("rpm"))
	if !reflect.DeepEqual(got, []string{"pixi"}) {
		t.Fatalf("pixi-only fedora deploy surfaced %v, want exactly [pixi] (the C4 over-build is back if this lists npm/cargo/aur)", got)
	}

	// (2) A multi-distro candy carrying an aur: section, deployed on a FEDORA image (build formats =
	// [rpm], no aur), surfaces NO aur — the distro gate. (It also carries no pixi.toml etc., so the
	// result is empty.)
	multiAur := map[string]spec.CandyReader{"chrome": aurCandy("chrome", "google-chrome")}
	got = deploykit.DetectExternalizedBuilders([]string{"chrome"}, multiAur, externalizedBuilders, builderTestImg("rpm"))
	if len(got) != 0 {
		t.Fatalf("fedora deploy of an aur:-section candy surfaced %v, want [] (aur must be distro-gated out on a non-aur box)", got)
	}

	// (3) The SAME candy on an ARCH image (build formats include aur) DOES surface aur — under-load
	// would break a real arch deploy.
	got = deploykit.DetectExternalizedBuilders([]string{"chrome"}, multiAur, externalizedBuilders, builderTestImg("pac", "aur"))
	if !reflect.DeepEqual(got, []string{"aur"}) {
		t.Fatalf("arch deploy of an aur:-section candy surfaced %v, want [aur]", got)
	}

	// (4) A candy that triggers NO builder surfaces nothing.
	none := map[string]spec.CandyReader{"plain": testCandy("plain", spec.CandyModel{}, spec.CandyView{})}
	if got := deploykit.DetectExternalizedBuilders([]string{"plain"}, none, externalizedBuilders, builderTestImg("rpm")); len(got) != 0 {
		t.Fatalf("no-builder candy surfaced %v, want []", got)
	}

	// (5) No BuilderConfig (e.g. a synthetic compile context) → nil, never a panic.
	if got := deploykit.DetectExternalizedBuilders([]string{"jupyter"}, pixiOnly, externalizedBuilders, &buildkit.ResolvedBox{Name: "x"}); got != nil {
		t.Fatalf("nil BuilderConfig surfaced %v, want nil", got)
	}
}
