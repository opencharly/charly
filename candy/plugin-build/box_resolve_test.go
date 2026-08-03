package build

import (
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// Relocated from charly/config_test.go (#55 decoupling cone, Batch B). These tests exercise
// buildkit.ResolveBox / ResolveAllBox / deploykit.CollectBoxPorts directly — capability tests of
// the resolve engine, not of charly's own loader. The former charly-side tests built their *Config
// via LoadConfig("testdata") (a real project fixture) or a literal &Config{}; both shapes are
// reconstructed here as literal spec.Config trees (charly's `Config` type IS `spec.Config` — a
// plain alias, config.go:38 — so no signature gap existed, only a fixture-sourcing one).
//
// resolveOptsFixture supplies a non-nil-but-empty DistroCfg/BuilderCfg: ResolveBox requires both
// non-nil (buildkit/config_resolve.go has no LoadUnified fallback), but reads them only for
// distro-vocabulary expansion (ExpandPackageInheritance/ResolveDistro) and user_policy adoption —
// none of which any assertion below depends on. `dir` is threaded through recursively by
// ResolveBox/ResolveAllBox but never read on this path, so any placeholder string is safe.
func resolveOptsFixture(extra spec.ResolveOpts) buildkit.ResolveOpts {
	return buildkit.ResolveOpts{
		IncludeDisabled:      extra.IncludeDisabled,
		IncludeDisabledNames: extra.IncludeDisabledNames,
		RequestedBoxes:       extra.RequestedBoxes,
		DistroCfg:            &spec.DistroConfig{},
		BuilderCfg:           &spec.BuilderConfig{},
	}
}

func boxMapOfFixture(m map[string]spec.BoxConfig) spec.BoxMap {
	out := make(spec.BoxMap, len(m))
	for k, v := range m {
		out[k] = spec.EncodeBox(v)
	}
	return out
}

// testdataStyleConfig reconstructs the box tree the former charly/testdata/images.yml +
// build.yml fixture declared (defaults + 7 named images, one disabled), as a literal
// spec.Config — the same shape TestResolveImage/TestFullTag/TestEnabledField/
// TestResolveImageNotFound exercised via charly's own LoadConfig("testdata").
func testdataStyleConfig() *spec.Config {
	return &spec.Config{
		Defaults: spec.BoxConfig{
			Base:      "quay.io/fedora/fedora:43",
			Platforms: []string{"linux/amd64", "linux/arm64"},
			Tag:       "auto",
			Registry:  "ghcr.io/test",
			Build:     []string{"rpm"},
		},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"base": {Base: "quay.io/fedora/fedora:43", Candy: []string{"pixi"}},
			"bazzite": {
				Base:      "ghcr.io/ublue-os/bazzite:stable",
				Platforms: []string{"linux/amd64"},
				Candy:     []string{"custom-packages"},
			},
			"cuda": {
				Base:      "quay.io/fedora/fedora:43",
				Platforms: []string{"linux/amd64"},
				Candy:     []string{"pixi", "cuda"},
			},
			"disabled-image": {
				Base:    "quay.io/fedora/fedora:43",
				Enabled: boolPtrFixture(false),
				Candy:   []string{"nonexistent-layer"},
			},
			"inference": {
				Base:  "ml-cuda",
				Tag:   "nightly",
				Candy: []string{"supervisord", "ollama"},
			},
			"ml-cuda": {Base: "cuda", Candy: []string{"python", "ml-libs"}},
			"ubuntu-dev": {
				Base:  "ubuntu:24.04",
				Build: []string{"deb"},
				Candy: []string{"pixi", "nodejs"},
			},
		}),
	}
}

func boolPtrFixture(b bool) *bool { return &b }

func TestResolveImage(t *testing.T) {
	cfg := testdataStyleConfig()

	tests := []struct {
		name           string
		boxName        string
		calverTag      string
		wantBase       string
		wantIsExternal bool
		wantPkg        string
		wantTag        string
		wantPlatforms  []string
	}{
		{
			name:           "base image inherits defaults",
			boxName:        "base",
			calverTag:      "2026.045.1415",
			wantBase:       "quay.io/fedora/fedora:43",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.045.1415", // auto -> calver
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
		},
		{
			name:           "cuda overrides platforms",
			boxName:        "cuda",
			calverTag:      "2026.045.1415",
			wantBase:       "quay.io/fedora/fedora:43",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.045.1415",
			wantPlatforms:  []string{"linux/amd64"},
		},
		{
			name:           "ml-cuda has internal base",
			boxName:        "ml-cuda",
			calverTag:      "2026.045.1415",
			wantBase:       "cuda",
			wantIsExternal: false,
			wantPkg:        "rpm",
			wantTag:        "2026.045.1415",
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
		},
		{
			name:           "inference has pinned tag",
			boxName:        "inference",
			calverTag:      "2026.045.1415",
			wantBase:       "ml-cuda",
			wantIsExternal: false,
			wantPkg:        "rpm",
			wantTag:        "nightly", // pinned, not calver
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
		},
		{
			name:           "ubuntu-dev uses deb",
			boxName:        "ubuntu-dev",
			calverTag:      "2026.045.1415",
			wantBase:       "ubuntu:24.04",
			wantIsExternal: true,
			wantPkg:        "deb",
			wantTag:        "2026.045.1415",
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
		},
		{
			name:           "bazzite is bootc",
			boxName:        "bazzite",
			calverTag:      "2026.045.1415",
			wantBase:       "ghcr.io/ublue-os/bazzite:stable",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.045.1415",
			wantPlatforms:  []string{"linux/amd64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := buildkit.ResolveBox(cfg, tt.boxName, tt.calverTag, "", resolveOptsFixture(spec.ResolveOpts{}))
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
			}

			if resolved.Base != tt.wantBase {
				t.Errorf("Base = %q, want %q", resolved.Base, tt.wantBase)
			}
			if resolved.IsExternalBase != tt.wantIsExternal {
				t.Errorf("IsExternalBase = %v, want %v", resolved.IsExternalBase, tt.wantIsExternal)
			}
			if resolved.Pkg != tt.wantPkg {
				t.Errorf("Pkg = %q, want %q", resolved.Pkg, tt.wantPkg)
			}
			if resolved.Tag != tt.wantTag {
				t.Errorf("Tag = %q, want %q", resolved.Tag, tt.wantTag)
			}
			if !reflect.DeepEqual(resolved.Platforms, tt.wantPlatforms) {
				t.Errorf("Platforms = %v, want %v", resolved.Platforms, tt.wantPlatforms)
			}
		})
	}
}

func TestResolveImageNotFound(t *testing.T) {
	cfg := testdataStyleConfig()

	_, err := buildkit.ResolveBox(cfg, "nonexistent", "2026.045.1415", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err == nil {
		t.Error("ResolveBox() expected error for nonexistent image")
	}
}

func TestResolveImageBuilders(t *testing.T) {
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     []string{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   spec.BuilderMap{"pixi": "default-builder", "npm": "default-builder"},
		},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"default-builder": {Candy: []string{}},
			"custom-builder":  {Candy: []string{}},
			"uses-default":    {Candy: []string{}},
			"uses-custom":     {Candy: []string{}, Builder: spec.BuilderMap{"pixi": "custom-builder"}},
		}),
	}

	// Image with no explicit builder inherits defaults.builder
	resolved, err := buildkit.ResolveBox(cfg, "uses-default", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.BuilderFor("pixi") != "default-builder" {
		t.Errorf("Builder[pixi] = %q, want %q", resolved.Builder.BuilderFor("pixi"), "default-builder")
	}

	// Image with explicit builder overrides defaults per-type
	resolved, err = buildkit.ResolveBox(cfg, "uses-custom", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.BuilderFor("pixi") != "custom-builder" {
		t.Errorf("Builder[pixi] = %q, want %q", resolved.Builder.BuilderFor("pixi"), "custom-builder")
	}
	// npm should still be inherited from defaults
	if resolved.Builder.BuilderFor("npm") != "default-builder" {
		t.Errorf("Builder[npm] = %q, want %q", resolved.Builder.BuilderFor("npm"), "default-builder")
	}

	// No defaults.builder → empty
	cfg2 := &spec.Config{
		Defaults: spec.BoxConfig{Build: []string{"rpm"}, Platforms: []string{"linux/amd64"}},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"app": {Candy: []string{}},
		}),
	}
	resolved, err = buildkit.ResolveBox(cfg2, "app", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if len(resolved.Builder) != 0 {
		t.Errorf("Builder = %v, want empty", resolved.Builder)
	}

	// Self-reference filtered out
	cfg3 := &spec.Config{
		Defaults: spec.BoxConfig{
			Build:     []string{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   spec.BuilderMap{"pixi": "my-builder"},
		},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"my-builder": {Candy: []string{}},
		}),
	}
	resolved, err = buildkit.ResolveBox(cfg3, "my-builder", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.HasBuilder("pixi") {
		t.Errorf("Self-referencing builder should be filtered, got %v", resolved.Builder)
	}

	// Inheritance from base image
	cfg4 := &spec.Config{
		Defaults: spec.BoxConfig{Build: []string{"pac"}, Platforms: []string{"linux/amd64"}},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"base-img":    {Build: []string{"pac"}, Candy: []string{}, Builder: spec.BuilderMap{"aur": "aur-builder"}},
			"aur-builder": {Candy: []string{}},
			"child-img":   {Base: "base-img", Candy: []string{}},
		}),
	}
	resolved, err = buildkit.ResolveBox(cfg4, "child-img", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.BuilderFor("aur") != "aur-builder" {
		t.Errorf("Builder[aur] = %q, want %q (inherited from base)", resolved.Builder.BuilderFor("aur"), "aur-builder")
	}
}

// TestCollectBoxPorts proves the box's published ports are inherited from EVERY
// candy in its base chain (boxes no longer declare ports), deduped by container
// port, sorted ascending, with the /udp suffix preserved.
func TestCollectBoxPorts(t *testing.T) {
	mk := func(name string, specs ...spec.PortSpec) spec.CandyReader {
		m := spec.CandyModel{Port: specs}
		m.Name = name
		v := spec.CandyView{Name: name}
		return deploykit.NewSpecCandyModel(m, v)
	}
	layers := map[string]spec.CandyReader{
		"sshd":     mk("sshd", spec.PortSpec{Port: 2222, Protocol: "tcp"}),
		"web":      mk("web", spec.PortSpec{Port: 3000, Protocol: "https+insecure"}),
		"cdp":      mk("cdp", spec.PortSpec{Port: 9222}),
		"udp-svc":  mk("udp-svc", spec.PortSpec{Port: 47998, Protocol: "udp"}),
		"web-dup":  mk("web-dup", spec.PortSpec{Port: 3000}), // duplicate container port → deduped
		"no-ports": mk("no-ports"),
	}
	cfg := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			// child inherits the base box's candy ports
			"base":  {Candy: []string{"sshd", "web"}},
			"child": {Base: "base", Candy: []string{"cdp", "udp-svc", "web-dup", "no-ports"}},
		}),
	}

	got, err := deploykit.CollectBoxPorts(cfg, layers, "child")
	if err != nil {
		t.Fatalf("CollectBoxPorts() error = %v", err)
	}
	// Inherited (sshd 2222, web 3000) + own (cdp 9222, udp 47998); web-dup's
	// 3000 deduped; sorted by container port; /udp preserved.
	want := []string{"2222", "3000", "9222", "47998/udp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CollectBoxPorts(child) = %v, want %v", got, want)
	}
}

func TestFullTag(t *testing.T) {
	cfg := testdataStyleConfig()

	resolved, err := buildkit.ResolveBox(cfg, "base", "2026.045.1415", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}

	want := "ghcr.io/test/base:2026.045.1415"
	if resolved.FullTag != want {
		t.Errorf("FullTag = %q, want %q", resolved.FullTag, want)
	}
}

func TestEnabledField(t *testing.T) {
	cfg := testdataStyleConfig()

	// disabled-image exists in raw config
	disabledImg, ok := cfg.BoxConfig("disabled-image")
	if !ok {
		t.Fatal("disabled-image not found in raw config")
	}
	if disabledImg.IsEnabled() {
		t.Error("disabled-image should not be enabled")
	}

	// disabled-image is excluded from BoxNames()
	for _, name := range cfg.BoxNames() {
		if name == "disabled-image" {
			t.Error("disabled-image should not appear in BoxNames()")
		}
	}

	// disabled-image is excluded from ResolveAllBox()
	all, err := buildkit.ResolveAllBox(cfg, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveAllBox() error = %v", err)
	}
	if _, ok := all["disabled-image"]; ok {
		t.Error("disabled-image should not appear in ResolveAllBox()")
	}

	// ResolveBox returns error for disabled image
	_, err = buildkit.ResolveBox(cfg, "disabled-image", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err == nil {
		t.Error("ResolveBox() should return error for disabled image")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got: %v", err)
	}

	// Enabled images still work
	_, err = buildkit.ResolveBox(cfg, "base", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Errorf("ResolveBox() unexpected error for enabled box: %v", err)
	}

	// --include-disabled (global) reaches the disabled image
	_, err = buildkit.ResolveBox(cfg, "disabled-image", "test", "", resolveOptsFixture(spec.ResolveOpts{IncludeDisabled: true}))
	if err != nil {
		t.Errorf("ResolveBox(IncludeDisabled=true) should succeed for disabled image, got: %v", err)
	}

	// --include-disabled scoped to a different name still rejects
	_, err = buildkit.ResolveBox(cfg, "disabled-image", "test", "", resolveOptsFixture(spec.ResolveOpts{
		IncludeDisabled:      true,
		IncludeDisabledNames: map[string]bool{"some-other-image": true},
	}))
	if err == nil {
		t.Error("scoped IncludeDisabled to a different name should still reject")
	}

	// --include-disabled scoped to the requested name succeeds
	_, err = buildkit.ResolveBox(cfg, "disabled-image", "test", "", resolveOptsFixture(spec.ResolveOpts{
		IncludeDisabled:      true,
		IncludeDisabledNames: map[string]bool{"disabled-image": true},
	}))
	if err != nil {
		t.Errorf("scoped IncludeDisabled to the requested name should succeed, got: %v", err)
	}
}

// TestResolveAllBox_ResolveBoxParity proves buildkit.ResolveAllBox's per-box projection and a
// direct buildkit.ResolveBox call agree on Tags for a builder-based image (relocated from
// charly/render_seam_cache_test.go, K3 cone2 test closure, split by assertion: the RESOLVER-OUTPUT
// half of the former TestNewCandyScanGeneratorPopulatesBoxes — whether ResolveAllBox/ResolveBox
// produce consistent, non-empty Tags — belongs here as capability coverage of the resolve engine;
// the CACHE-BEHAVIOR half — whether charly's newCandyScanGenerator stores a pushed box set
// verbatim — stayed in charly with a literal fixture, since this plugin can't reach charly's
// constructor). A literal *spec.Config fixture (mirroring box/fedora's real "fedora-builder"
// image shape: a fedora-distro, rpm-format, builder-produced box) replaces the original test's
// real box/fedora disk read — ResolvedBox.Tags is derived purely from the box's own
// Distro/BuildFormats fields (config_resolve.go: `Tags = append([]string{"all"}, Distro...,
// BuildFormats...)`), so no populated DistroCfg/BuilderCfg is needed for this claim.
func TestResolveAllBox_ResolveBoxParity(t *testing.T) {
	const boxName = "fedora-builder"
	cfg := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			boxName: {
				Base:    "quay.io/fedora/fedora:43",
				Distro:  []string{"fedora"},
				Build:   []string{"rpm"},
				Produce: []string{"pixi", "npm"},
			},
		}),
	}

	all, err := buildkit.ResolveAllBox(cfg, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveAllBox: %v", err)
	}
	viaAll := all[boxName]
	if viaAll == nil {
		t.Fatalf("ResolveAllBox: box %q not resolved", boxName)
	}
	if len(viaAll.Tags) == 0 {
		t.Fatal("Tags is empty — a builder-based image must carry distro/build-format tags")
	}

	direct, err := buildkit.ResolveBox(cfg, boxName, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox: %v", err)
	}
	if !reflect.DeepEqual(viaAll.Tags, direct.Tags) {
		t.Errorf("ResolveAllBox/ResolveBox Tags mismatch: %v vs %v", viaAll.Tags, direct.Tags)
	}
}

func TestResolveImageDistroBaseChain(t *testing.T) {
	// Tests that distro: tags propagate through the entire base chain,
	// not just the immediate parent.
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     []string{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			// Level 0: defines distro
			"fedora": {
				Base:   "quay.io/fedora/fedora:43",
				Distro: []string{"fedora:43", "fedora"},
				Candy:  []string{},
			},
			// Level 1: no distro set, should inherit from fedora
			"fedora-nonfree": {
				Base:  "fedora",
				Candy: []string{},
			},
			// Level 2: no distro set, should inherit through fedora-nonfree -> fedora
			"nvidia": {
				Base:  "fedora-nonfree",
				Candy: []string{},
			},
			// Level 3: no distro set, should inherit through nvidia -> fedora-nonfree -> fedora
			"ml-app": {
				Base:  "nvidia",
				Candy: []string{},
			},
		}),
	}

	tests := []struct {
		name       string
		boxName    string
		wantDistro []string
	}{
		{"level 0: defines distro", "fedora", []string{"fedora:43", "fedora"}},
		{"level 1: inherits from parent", "fedora-nonfree", []string{"fedora:43", "fedora"}},
		{"level 2: inherits through chain", "nvidia", []string{"fedora:43", "fedora"}},
		{"level 3: inherits through deep chain", "ml-app", []string{"fedora:43", "fedora"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := buildkit.ResolveBox(cfg, tt.boxName, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
			}
			if !reflect.DeepEqual(resolved.Distro, tt.wantDistro) {
				t.Errorf("Distro = %v, want %v", resolved.Distro, tt.wantDistro)
			}
		})
	}
}

func TestResolveImageBuildBaseChain(t *testing.T) {
	// Tests that build: formats propagate through the base chain.
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{
			Registry:  "ghcr.io/test",
			Platforms: []string{"linux/amd64"},
		},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			// Level 0: defines build
			"arch": {
				Base:  "docker.io/library/archlinux:latest",
				Build: []string{"pac"},
				Candy: []string{},
			},
			// Level 1: no build set, should inherit from arch
			"arch-extended": {
				Base:  "arch",
				Candy: []string{},
			},
			// Level 2: no build set, should inherit through chain
			"arch-app": {
				Base:  "arch-extended",
				Candy: []string{},
			},
		}),
	}

	tests := []struct {
		name      string
		boxName   string
		wantBuild []string
	}{
		{"level 0: defines build", "arch", []string{"pac"}},
		{"level 1: inherits from parent", "arch-extended", []string{"pac"}},
		{"level 2: inherits through chain", "arch-app", []string{"pac"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := buildkit.ResolveBox(cfg, tt.boxName, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
			}
			if !reflect.DeepEqual(resolved.BuildFormats, tt.wantBuild) {
				t.Errorf("BuildFormats = %v, want %v", resolved.BuildFormats, tt.wantBuild)
			}
		})
	}
}
