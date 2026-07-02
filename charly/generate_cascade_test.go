package main

import (
	"strings"
	"testing"
)

// TestGenerateBuildPath_FoldsTopPackagesAndCascade is the regression guard for
// the build-path unification: `generate.go`'s package emission MUST go through
// the SAME `resolveCascadePackages` the deploy path uses, so the candy's
// top-level `package:` base is folded AND distro tag sections cascade (union).
// Before the unification the build path read only TagSection/FormatSection and
// silently dropped every candy's top-level packages.
func TestGenerateBuildPath_FoldsTopPackagesAndCascade(t *testing.T) {
	tmpDir := t.TempDir()

	// A candy with a top-level base + a bare-distro tag section + a versioned
	// override — exercises base-fold + union across [debian:13, debian].
	layer := &Candy{Name: "pkglayer"}
	derivePackageSectionsFromCalamares(layer, &CandyYAML{
		Package: PackageItemsFromStrings([]string{"base-pkg"}),
		Distro: map[string]*DistroPackages{
			"debian":    {Package: PackageItemsFromStrings([]string{"deb-pkg"})},
			"debian-13": {Package: PackageItemsFromStrings([]string{"v13-pkg"})},
		},
	})

	g := &Generator{
		BuildDir: tmpDir,
		Config:   &Config{},
		Candies:  map[string]*Candy{"pkglayer": layer},
		Boxes: map[string]*ResolvedBox{
			"deb-image": {
				Name:           "deb-image",
				Base:           "debian:13",
				IsExternalBase: true,
				Registry:       "ghcr.io/test",
				Tag:            "latest",
				FullTag:        "ghcr.io/test/deb-image:latest",
				Candy:          []string{"pkglayer"},
				Pkg:            "deb",
				BuildFormats:   []string{"deb"},
				Distro:         []string{"debian:13", "debian"},
				Tags:           []string{"all", "debian:13", "debian", "deb"},
				User:           "user",
				UID:            1000,
				GID:            1000,
				Home:           "/home/user",
				DistroDef: &DistroDef{Format: map[string]*FormatDef{
					"deb": {InstallTemplate: "RUN apt-get install -y {{range .Packages}}{{.}} {{end}}\n"},
				}},
				BuilderConfig: &BuilderConfig{Builder: map[string]*BuilderDef{}},
			},
		},
		Containerfiles: make(map[string]string),
	}

	if err := g.generateContainerfile("deb-image"); err != nil {
		t.Fatalf("generateContainerfile: %v", err)
	}
	cf := g.Containerfiles["deb-image"]
	// All three must be present: the top-level base (was dropped pre-fix) + both
	// the bare and versioned distro tag packages (union, most-specific-first).
	for _, want := range []string{"base-pkg", "deb-pkg", "v13-pkg"} {
		if !strings.Contains(cf, want) {
			t.Errorf("Containerfile missing %q (build path must cascade + fold top-level base)\n%s", want, cf)
		}
	}
}
