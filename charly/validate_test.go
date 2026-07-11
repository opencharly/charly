package main

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"base": {Candy: []string{"pixi"}},
		}),
	}

	layers := map[string]*Candy{
		"pixi": {
			Name: "pixi",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateBuildTunables(t *testing.T) {
	cases := []struct {
		name    string
		ic      BoxConfig
		wantErr string // substring; "" = expect no error
	}{
		{"all unset is valid", BoxConfig{}, ""},
		{"valid full set", BoxConfig{Jobs: new(4), PodmanJobs: new(0), PodmanJobsCap: new(8), Cache: "image", ContextIgnore: []string{"image", ".check"}}, ""},
		{"jobs zero rejected", BoxConfig{Jobs: new(0)}, "jobs must be >= 1"},
		{"jobs negative rejected", BoxConfig{Jobs: new(-2)}, "jobs must be >= 1"},
		{"podman_jobs negative rejected", BoxConfig{PodmanJobs: new(-1)}, "podman_jobs must be >= 0"},
		{"podman_jobs zero allowed (auto)", BoxConfig{PodmanJobs: new(0)}, ""},
		{"podman_jobs_cap zero rejected", BoxConfig{PodmanJobsCap: new(0)}, "podman_jobs_cap must be >= 1"},
		{"bad cache mode rejected", BoxConfig{Cache: "bogus"}, "cache must be one of"},
		{"cache none allowed", BoxConfig{Cache: "none"}, ""},
		{"empty context_ignore entry rejected", BoxConfig{ContextIgnore: []string{"image", "  "}}, "context_ignore[1] must not be empty"},
		{"keep_images zero allowed (disabled)", BoxConfig{KeepImages: new(0)}, ""},
		{"keep_images negative rejected", BoxConfig{KeepImages: new(-1)}, "keep_images must be >= 0"},
		{"keep_check_runs valid", BoxConfig{KeepCheckRuns: new(10)}, ""},
		{"keep_check_runs negative rejected", BoxConfig{KeepCheckRuns: new(-3)}, "keep_check_runs must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Defaults: tc.ic, Box: boxMapOf(map[string]BoxConfig{})}
			errs := &ValidationError{}
			validateBuildTunables(cfg, errs)
			if tc.wantErr == "" {
				if errs.HasErrors() {
					t.Errorf("expected no error, got: %v", errs.Errors)
				}
				return
			}
			if !errs.HasErrors() {
				t.Fatalf("expected error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(strings.Join(errs.Errors, "\n"), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, errs.Errors)
			}
		})
	}
}

func TestValidateInvalidPkg(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"invalid"},
		},
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Error("expected error for invalid pkg")
	}
	if !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingCandy(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"nonexistent"}},
		}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for missing candy")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingCandyWithTypo(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"pixie"}}, // typo for "pixi"
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for missing candy")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected typo suggestion, got: %v", err)
	}
}

func TestValidateCandyNoInstallFiles(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"empty": {Name: "empty"}, // no install files
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for candy without install files")
	}
	if !strings.Contains(err.Error(), "must have at least one install file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCargoWithoutSrc(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"tool": {
			Name:         "tool",
			HasCargoToml: true,
			HasSrcDir:    false,
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for Cargo.toml without src/")
	}
	if !strings.Contains(err.Error(), "requires src/") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCoprWithoutPackages(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"layer": {
			Name: "layer",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]any{"copr": []any{"owner/project"}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for rpm.copr without rpm.packages")
	}
	if !strings.Contains(err.Error(), "rpm.copr requires packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateReposWithoutPackages(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"layer": {
			Name: "layer",
			plan: []Step{{Run: "build", Op: cmdOp("true")}}, // needs some install file
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]any{"repo": []any{map[string]any{"name": "test", "url": "http://example.com"}}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for rpm.repo without packages")
	}
	if !strings.Contains(err.Error(), "rpm.repo requires packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateModulesWithoutPackages(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"layer": {
			Name: "layer",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]any{"modules": []any{"valkey:remi-9.0"}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for rpm.modules without rpm.packages")
	}
	if !strings.Contains(err.Error(), "rpm.modules requires packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateRepoUrlAndRpmBothSet removed — format-specific validation
// rules (e.g., "exactly one of url or rpm") are now in build.yml validate
// section, not in Go code.

// TestValidateRepoNeitherUrlNorRpm removed — format-specific validation now in build.yml

func TestValidatePacPkgValue(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"pac"}},
		Box:      boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("pkg: pac should be valid, got error: %v", err)
	}
}

func TestValidateInvalidPkgValue(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"zypper"},
		},
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Error("expected error for invalid pkg value")
	}
	if !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidatePacReposMissingServer removed — format-specific field requirements
// (pac repos must have server) are now in build.yml validate rules, not Go code.

func TestValidateAurWithoutAurBuilder(t *testing.T) {
	// Arch image declaring build: [pac, aur] (i.e. it WILL invoke the aur
	// builder per IR-compile time) but with no builder.aur configured —
	// this is the legitimate failure case the validator must still catch.
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"pac", "aur"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"arch-img": {
				Base:  "arch:latest",
				Build: BuildFormats{"pac", "aur"},
				Candy: []string{"aur-layer"},
			},
		}),
	}
	layers := map[string]*Candy{
		"aur-layer": {
			Name: "aur-layer",
			formatSections: map[string]*PackageSection{
				"aur": {FormatName: "aur", Packages: []string{"yay-bin"}, Raw: map[string]any{"packages": []any{"yay-bin"}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for aur packages without builder.aur")
	}
	if !strings.Contains(err.Error(), "no builder.aur configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateAurOnFedoraImageNoError covers the multi-distro candy case.
// A candy that ships rpm: AND aur: sections is consumed by a Fedora image
// with build: [rpm]. The IR compiler skips the aur: section entirely
// (install_build.go:236-249 iterates only img.BuildFormats), so the
// arch-builder is never invoked. The validator must NOT require
// builder.aur on the Fedora consumer.
func TestValidateAurOnFedoraImageNoError(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"rpm"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"fedora-img": {
				Base:  "quay.io/fedora/fedora:43",
				Build: BuildFormats{"rpm"},
				Candy: []string{"multi-distro-layer"},
			},
		}),
	}
	layers := map[string]*Candy{
		"multi-distro-layer": {
			Name: "multi-distro-layer",
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Packages: []string{"google-chrome-stable"}, Raw: map[string]any{"packages": []any{"google-chrome-stable"}}},
				"aur": {FormatName: "aur", Packages: []string{"google-chrome"}, Raw: map[string]any{"packages": []any{"google-chrome"}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err != nil && strings.Contains(err.Error(), "no builder.aur configured") {
		t.Fatalf("Fedora image (build=[rpm]) consuming a multi-distro candy with rpm:+aur: must not require builder.aur; got: %v", err)
	}
}

// TestValidateAurOnArchImageWithoutAurInBuildFormats covers the partial-build
// case. An Arch image with build: [pac] (no aur) consumes a candy with an
// aur: section. The IR compiler skips aur, so the validator must skip it too.
func TestValidateAurOnArchImageWithoutAurInBuildFormats(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"pac"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"arch-pac-only": {
				Base:  "arch:latest",
				Build: BuildFormats{"pac"},
				Candy: []string{"aur-layer"},
			},
		}),
	}
	layers := map[string]*Candy{
		"aur-layer": {
			Name: "aur-layer",
			formatSections: map[string]*PackageSection{
				"aur": {FormatName: "aur", Packages: []string{"yay-bin"}, Raw: map[string]any{"packages": []any{"yay-bin"}}},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err != nil && strings.Contains(err.Error(), "no builder.aur configured") {
		t.Fatalf("Arch image with build=[pac] (no aur) must not require builder.aur; got: %v", err)
	}
}

// TestValidatePixiBuilderUnconditional covers the detect_files path. Pixi
// produces distro-agnostic artifacts copied into the final stage, so the
// builder requirement applies regardless of the image's BuildFormats.
// A Fedora image with a candy containing pixi.toml MUST still require
// builder.pixi — the BuildFormats gate applies only to detect_config-based
// builders (aur).
func TestValidatePixiBuilderUnconditional(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"rpm"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"fedora-img": {
				Base:  "quay.io/fedora/fedora:43",
				Build: BuildFormats{"rpm"},
				Candy: []string{"pixi-layer"},
			},
		}),
	}
	layers := map[string]*Candy{
		"pixi-layer": {
			Name:        "pixi-layer",
			HasPixiToml: true,
		},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for pixi.toml without builder.pixi")
	}
	if !strings.Contains(err.Error(), "no builder.pixi configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateUnknownDependency(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"layer": {
			Name:    "layer",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			Require: toCandyRefs([]string{"unknown"}),
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for unknown dependency")
	}
	if !strings.Contains(err.Error(), "unknown candy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageCycle(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]BoxConfig{
			"a": {Base: "b", Candy: []string{}},
			"b": {Base: "c", Candy: []string{}},
			"c": {Base: "a", Candy: []string{}},
		}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for image cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCandyCycle(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"a"}},
		}),
	}
	layers := map[string]*Candy{
		"a": {Name: "a", plan: []Step{{Run: "build", Op: cmdOp("true")}}, Require: toCandyRefs([]string{"b"})},
		"b": {Name: "b", plan: []Step{{Run: "build", Op: cmdOp("true")}}, Require: toCandyRefs([]string{"c"})},
		"c": {Name: "c", plan: []Step{{Run: "build", Op: cmdOp("true")}}, Require: toCandyRefs([]string{"a"})},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for layer cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMultipleErrors(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"invalid"}},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"missing1", "missing2"}},
		}),
	}
	layers := map[string]*Candy{}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected errors")
	}

	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	// Should have at least 3 errors: invalid pkg, two missing candies
	if len(valErr.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(valErr.Errors), valErr.Errors)
	}
}

func TestValidateRouteWithoutTraefik(t *testing.T) {
	// Route without traefik is valid — routes are generic metadata consumed by traefik or tunnel
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:  "svc",
			plan:  []Step{{Run: "build", Op: cmdOp("true")}},
			route: &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateRouteWithTraefik(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"traefik", "svc"}},
		}),
	}
	layers := map[string]*Candy{
		"traefik": {
			Name: "traefik",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
		},
		"svc": {
			Name:  "svc",
			plan:  []Step{{Run: "build", Op: cmdOp("true")}},
			route: &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateSkipsDisabledImages(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"good": {Candy: []string{"pixi"}},
			"bad-disabled": {
				Enabled: new(false),
				Candy:   []string{"nonexistent-layer"},
				Build:   BuildFormats{"invalid"},
			},
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() should pass when bad image is disabled, got: %v", err)
	}
}

func TestValidateVolumesValid(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "data", Path: "~/.myapp"}},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateVolumesDuplicate(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name: "svc",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{
				{Name: "data", Path: "~/.myapp"},
				{Name: "data", Path: "~/.other"},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for duplicate volume name")
	}
	if !strings.Contains(err.Error(), "duplicate volume name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesValid(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {
				Candy: []string{"svc"},
				Alias: []AliasConfig{{Name: "mycli", Command: "mycli-bin"}},
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateAliasesDuplicate(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name: "svc",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{
				{Name: "mycli", Command: "cmd1"},
				{Name: "mycli", Command: "cmd2"},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for duplicate alias name")
	}
	if !strings.Contains(err.Error(), "duplicate alias name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesInvalidName(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "-bad", Command: "cmd"}},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for invalid alias name")
	}
	if !strings.Contains(err.Error(), "must match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageAliasesDuplicate(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"test": {
				Candy: []string{"svc"},
				Alias: []AliasConfig{
					{Name: "mycli", Command: "cmd1"},
					{Name: "mycli", Command: "cmd2"},
				},
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {Name: "svc", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for duplicate image alias name")
	}
	if !strings.Contains(err.Error(), "duplicate alias name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSelfBuilder(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"rpm"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"myimg": {
				Candy:   []string{"pixi"},
				Builder: BuilderMap{"pixi": "myimg"},
			},
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for self-referencing builder")
	}
	if !strings.Contains(err.Error(), "cannot reference self") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBuilderInheritedSelfNotError(t *testing.T) {
	// Builder image inheriting defaults.builders that points to itself is NOT an error
	cfg := &Config{
		Defaults: BoxConfig{
			Build:   BuildFormats{"rpm"},
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"builder": {Candy: []string{"pixi"}},
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidatePerImageBuilderNotFound(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Build: BuildFormats{"rpm"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"app": {
				Candy:   []string{"pixi"},
				Builder: BuilderMap{"pixi": "nonexistent"},
			},
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	err := Validate(cfg, vCandies(layers), testdataDir, ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for nonexistent per-image builder")
	}
	if !strings.Contains(err.Error(), "is not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCandyWithIncludesNoInstallFiles(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"sway-desktop"}},
		}),
	}
	layers := map[string]*Candy{
		"pipewire":     {Name: "pipewire", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
		"wayvnc":       {Name: "wayvnc", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
		"sway-desktop": {Name: "sway-desktop", IncludedCandy: toCandyRefs([]string{"pipewire", "wayvnc"})},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("expected no error for composing layer without install files, got: %v", err)
	}
}

func TestValidateCandyIncludesCycle(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"a": {Name: "a", plan: []Step{{Run: "build", Op: cmdOp("true")}}, IncludedCandy: toCandyRefs([]string{"b"})},
		"b": {Name: "b", plan: []Step{{Run: "build", Op: cmdOp("true")}}, IncludedCandy: toCandyRefs([]string{"a"})},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for circular candy composition")
	}
}

func TestValidateCandyIncludesMissing(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"desktop": {Name: "desktop", IncludedCandy: toCandyRefs([]string{"nonexistent"})},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for unknown candy in includes")
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"pixi", "pixi", 0},
		{"pixi", "pixie", 1},
		{"pixi", "pxi", 1},
		{"pixi", "python", 5},
	}

	for _, tt := range tests {
		t.Run(tt.a+"-"+tt.b, func(t *testing.T) {
			got := levenshteinDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestValidatePortRelayValid(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}},
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"supervisord", "socat", "chrome"}},
		}),
	}
	layers := map[string]*Candy{
		"supervisord": {Name: "supervisord", Require: toCandyRefs([]string{"python"}), plan: []Step{{Run: "build", Op: cmdOp("true")}}, formatSections: map[string]*PackageSection{"rpm": {FormatName: "rpm", Packages: []string{"supervisor"}}}},
		"python":      {Name: "python", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
		"socat":       {Name: "socat", plan: []Step{{Run: "build", Op: cmdOp("true")}}, formatSections: map[string]*PackageSection{"rpm": {FormatName: "rpm", Packages: []string{"socat", "iproute"}}}},
		"chrome": {
			Name:           "chrome",
			plan:           []Step{{Run: "build", Op: cmdOp("true")}},
			ports:          []string{"9222"},
			portSpecs:      []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidatePortRelayNotInPorts(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:           "svc",
			plan:           []Step{{Run: "build", Op: cmdOp("true")}},
			ports:          []string{"8080"},
			portSpecs:      []PortSpec{{Port: 8080, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for port_relay port not in candy ports")
	}
	if !strings.Contains(err.Error(), "not declared in the candy's ports") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayNoPorts(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:           "svc",
			plan:           []Step{{Run: "build", Op: cmdOp("true")}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for port_relay without ports")
	}
	if !strings.Contains(err.Error(), "no ports declared") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayDuplicate(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:           "svc",
			plan:           []Step{{Run: "build", Op: cmdOp("true")}},
			ports:          []string{"9222"},
			portSpecs:      []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222, 9222},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for duplicate port_relay port")
	}
	if !strings.Contains(err.Error(), "duplicate port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayMissingSocat(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"test": {Candy: []string{"chrome"}},
		}),
	}
	layers := map[string]*Candy{
		"chrome": {
			Name:           "chrome",
			plan:           []Step{{Run: "build", Op: cmdOp("true")}},
			ports:          []string{"9222"},
			portSpecs:      []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("expected error for port_relay without socat candy")
	}
	if !strings.Contains(err.Error(), "missing \"socat\" candy") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateDataEntryUnknownVolume guards the check at validate.go where a
// candy's data: entry references a volume name that is not declared by any
// candy in the composed image chain. Without this guard, a typo in a data
// candy (e.g. `volume: workspae`) silently produces an image that never
// seeds its workspace, and the error only surfaces at runtime as an empty
// directory.
func TestValidateDataEntryUnknownVolume(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"jupyter": {Candy: []string{"jupyter", "notebook-templates"}},
		}),
	}
	layers := map[string]*Candy{
		"jupyter": {
			Name: "jupyter",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{
				{Name: "workspace", Path: "~/workspace"},
			},
		},
		"notebook-templates": {
			Name: "notebook-templates",
			// Typo: "workspae" instead of "workspace" — must be caught.
			data: []DataYAML{
				{Src: "data/notebooks", Volume: "workspae"},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for data entry referencing unknown volume")
	}
	if !strings.Contains(err.Error(), "workspae") {
		t.Errorf("expected error to mention unknown volume name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not declared by any candy") {
		t.Errorf("expected 'not declared by any candy' phrasing, got: %v", err)
	}
}

// TestValidateDataEntryKnownVolume is the happy path for the same check:
// when the data entry's volume matches a declared volume anywhere in the
// image's candy chain, Validate succeeds.
func TestValidateDataEntryKnownVolume(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOf(map[string]BoxConfig{
			"jupyter": {Candy: []string{"jupyter", "notebook-templates"}},
		}),
	}
	layers := map[string]*Candy{
		"jupyter": {
			Name: "jupyter",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{
				{Name: "workspace", Path: "~/workspace"},
			},
		},
		"notebook-templates": {
			Name: "notebook-templates",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
			data: []DataYAML{
				{Src: "data/notebooks", Volume: "workspace"},
			},
		},
	}

	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err != nil && strings.Contains(err.Error(), "not declared by any layer") {
		t.Errorf("unexpected 'unknown volume' error for valid data entry: %v", err)
	}
}

// ---------------------------------------------------------------------------
// secret_accepts / secret_requires validation tests (Step 3 of the
// credential-backed secrets feature). Each test exercises one of the rules in
// plan §4.4.
// ---------------------------------------------------------------------------

// secretDepsCandy builds a minimal candy with the given secret dependency
// configuration, for reuse across tests.
func secretDepsCandy(opts func(l *Candy)) *Candy {
	l := &Candy{Name: "svc", plan: []Step{{Run: "build", Op: cmdOp("true")}}}
	if opts != nil {
		opts(l)
	}
	return l
}

// TestValidateSecretAcceptsHappyPath — valid secret_accepts entry with an
// explicit Key override that matches the charly/<service>/<key> format. No errors.
func TestValidateSecretAcceptsHappyPath(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.secretAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "OpenRouter API key", Key: "charly/api-key/openrouter"},
			}
		}),
	}
	if err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{}); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

// TestValidateSecretRequiresMissingDescription — secret_requires entry with
// empty description must be rejected (consistency with env_requires).

// TestValidateSecretAcceptsInvalidName — name with invalid chars must be
// rejected by the env-var-name check.

// TestValidateSecretAcceptsCollidesWithEnvAccepts — plan §4.4 rule 1: a name
// cannot appear in both env_accepts and secret_accepts.
func TestValidateSecretAcceptsCollidesWithEnvAccepts(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.envAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "plaintext"},
			}
			l.secretAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected collision error between env_accepts and secret_accepts")
	}
	if !strings.Contains(err.Error(), "appears in both env_accepts and secret_accepts") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretRequiresCollidesWithEnvRequires — same collision check for
// requires variants.
func TestValidateSecretRequiresCollidesWithEnvRequires(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.envRequires = []EnvDependency{
				{Name: "WEBUI_ADMIN_PASSWORD", Description: "plaintext"},
			}
			l.secretRequires = []EnvDependency{
				{Name: "WEBUI_ADMIN_PASSWORD", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected collision error between env_requires and secret_requires")
	}
	if !strings.Contains(err.Error(), "appears in both env_requires and secret_requires") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsCollidesWithSecretRequires — a name cannot appear
// in both secret_accepts and secret_requires in the same candy.
func TestValidateSecretAcceptsCollidesWithSecretRequires(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.secretRequires = []EnvDependency{
				{Name: "API_TOKEN", Description: "required"},
			}
			l.secretAccepts = []EnvDependency{
				{Name: "API_TOKEN", Description: "optional"},
			}
		}),
	}
	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected collision between secret_requires and secret_accepts")
	}
	if !strings.Contains(err.Error(), "appears in both secret_requires and secret_accepts") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretCollidesWithEnvProvides — plan §4.4 rule 2: a secret_*
// entry name cannot also be in env_provides. env_provides is for plaintext
// service discovery URLs; credentials must not be advertised that way.
func TestValidateSecretCollidesWithEnvProvides(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.envProvides = map[string]string{
				"API_TOKEN": "http://{{.ContainerName}}:8080/token", // would be plaintext
			}
			l.secretAccepts = []EnvDependency{
				{Name: "API_TOKEN", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected error when secret_accepts overlaps env_provides")
	}
	if !strings.Contains(err.Error(), "also appears in env_provides") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsKeyMustStartWithCharly — plan §4.4 rule 5: the
// optional Key override must start with "charly/" to prevent candies from
// exfiltrating unrelated user credentials.
// TestValidateSecretAcceptsInvalidSlug — a name that would produce an invalid
// podman-secret slug (e.g., leading underscore → leading hyphen after
// lowercase-kebab) must be rejected.
func TestValidateSecretAcceptsInvalidSlug(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{})}
	layers := map[string]*Candy{
		"svc": secretDepsCandy(func(l *Candy) {
			l.secretAccepts = []EnvDependency{
				{Name: "_LEADING_UNDERSCORE", Description: "bad slug"},
			}
		}),
	}
	err := Validate(cfg, vCandies(layers), testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Fatal("expected slug-validation error")
	}
	// The name itself is also invalid (starts with _ → isValidEnvVarName
	// permits it, but slug check rejects the leading hyphen).
	if !strings.Contains(err.Error(), "invalid podman secret slug") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestEnvVarNameToPodmanSecretSlug — unit test for the slug helper.
func TestEnvVarNameToPodmanSecretSlug(t *testing.T) {
	cases := map[string]string{
		"OPENROUTER_API_KEY":   "openrouter-api-key",
		"IMMICH_API_KEY":       "immich-api-key",
		"WEBUI_ADMIN_PASSWORD": "webui-admin-password",
		"TS_AUTHKEY":           "ts-authkey",
		"X":                    "x",
	}
	for in, want := range cases {
		if got := envVarNameToPodmanSecretSlug(in); got != want {
			t.Errorf("envVarNameToPodmanSecretSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// vCandies stamps a default per-entity version on every LOCAL candy that lacks
// one, so a fixture map satisfies the mandatory-version rule
// (validateCandyContents) without each literal repeating it — mirrors what
// `charly migrate` (entity-version step) backfills in real configs. Remote candies
// are left alone (their version comes from the fetched candy manifest).
func vCandies(m map[string]*Candy) map[string]*Candy {
	for _, l := range m {
		if l == nil || l.Remote {
			continue
		}
		if l.Version == "" {
			l.Version = "2026.155.1801"
		}
		// Mandatory-ADE fixtures: stamp a minimal compliant description: string
		// and ensure the plan: carries at least one deterministic `check:` step,
		// so tests exercising OTHER validation rules satisfy
		// validateCandyContents's ADE mandate. Mirrors the Version stamp above.
		// Non-destructive — only fills in what's absent, preserving any run:
		// steps already present (converted from the former task: list).
		if strings.TrimSpace(l.Description) == "" {
			l.Description = "fixture"
		}
		hasCheck := false
		for i := range l.plan {
			if l.plan[i].Check != "" {
				hasCheck = true
				break
			}
		}
		if !hasCheck {
			l.plan = append(l.plan, Step{Check: "fixture step", Op: cmdOp("true")})
		}
	}
	return m
}

// TestValidateCandyMissingVersion is the proving coverage for the
// mandatory-version rule: a local candy with no version: fails validation with
// an actionable message. (Uses a distinctly-named map so the vCandies wrap that
// the other tests apply does not mask the error.)
