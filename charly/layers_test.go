package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"

	"gopkg.in/yaml.v3"
)

func TestScanCandies(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	expectedCandies := []string{"pixi", "python", "nodejs", "cargo-tool", "webservice", "pixi-locked"}
	for _, name := range expectedCandies {
		if _, ok := layers[name]; !ok {
			t.Errorf("missing candy %q", name)
		}
	}
}

func TestCandyUnknownKeyRejected(t *testing.T) {
	// The parser HARD-ERRORS on an unknown top-level key (a plural/singular typo)
	// instead of silently dropping it. Regression for #50 — the
	// tasks:/vars:/layers:/secret_accepts: silent-drop that masked broken candies.
	bad := map[string]string{
		"tasks":          "name: t\ntasks:\n  - cmd: echo hi\n",
		"vars":           "name: t\nvars:\n  FOO: bar\n",
		"layers":         "name: t\nlayers:\n  - supervisord\n",
		"secret_accepts": "name: t\nsecret_accepts:\n  - name: X\n",
	}
	for key, body := range bad {
		t.Run("reject_"+key, func(t *testing.T) {
			err := candyBodyGuardErr(body)
			if err == nil {
				t.Fatalf("expected error for unknown plural key %q, got nil", key)
			}
			if !strings.Contains(err.Error(), "unknown top-level key") {
				t.Errorf("error for %q = %v, want 'unknown top-level key'", key, err)
			}
		})
	}

	// The SINGULAR forms must parse cleanly AND populate their fields. The
	// operational list is now `plan:` (the former `task:` key is retired — install
	// ops are `run:` steps in the unified plan).
	good := "name: t\nplan:\n  - run: install\n    command: echo hi\nvar:\n  FOO: bar\ncandy:\n  - supervisord\nsecret_accept:\n  - name: X\n"
	var ly spec.CandyYAML
	if err := yaml.Unmarshal([]byte(good), &ly); err != nil {
		t.Fatalf("singular keys must parse, got error: %v", err)
	}
	if len(ly.Plan) != 1 || ly.Vars["FOO"] != "bar" || len(ly.Candy) != 1 || len(ly.SecretAccept) != 1 {
		t.Errorf("singular keys parsed but fields empty: plan=%d var=%v candy=%v secret_accept=%d",
			len(ly.Plan), ly.Vars, ly.Candy, len(ly.SecretAccept))
	}

	// Packages live ONLY under the `distro:` map — a top-level distro-tag key
	// (`fedora:43:`) is no longer a package surface and is rejected as an unknown
	// key (see TestLegacyTopLevelFormatKeyRejected for the full set).
	if err := candyBodyGuardErr("name: t\nfedora:43:\n  package: [vim]\n"); err == nil {
		t.Fatal("top-level distro-tag key fedora:43: must be rejected (use the distro: map)")
	}
	// The distro: map form parses cleanly and routes to a tag section. Decoded
	// through the CUE normalize path (bare-string packages are canonicalized to
	// {name: …} by the normalizer, replacing the deleted PackageItem.UnmarshalYAML).
	var ly3 spec.CandyYAML
	if err := decodeViaCUEForTest(t, "name: t\ndistro:\n  fedora-43:\n    package: [vim]\n", &ly3); err != nil {
		t.Fatalf("distro: map with fedora-43 must parse, got error: %v", err)
	}
	if ly3.Distro["fedora-43"] == nil || len(ly3.Distro["fedora-43"].Package) != 1 {
		t.Errorf("distro.fedora-43 not parsed: %+v", ly3.Distro)
	}
}

func TestCandyPixi(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi == nil {
		t.Fatal("pixi candy not found")
	}

	if !pixi.HasTasks() {
		t.Error("pixi should have tasks:")
	}
	if pixi.FormatSection("rpm") != nil {
		t.Error("pixi should not have rpm format section")
	}
	if pixi.HasFile("pixi.toml") {
		t.Error("pixi should not have pixi.toml")
	}
	if len(pixi.GetRequire()) != 0 {
		t.Errorf("pixi should have no depends, got %v", pixi.GetRequire())
	}
}

func TestCandyPython(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python candy not found")
	}

	if !python.HasFile("pixi.toml") {
		t.Error("python should have pixi.toml")
	}
	if !reflect.DeepEqual(bareRefs(python.GetRequire()), []string{"pixi"}) {
		t.Errorf("python.Require = %v, want [pixi]", python.GetRequire())
	}
}

func TestCandyNodejs(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	nodejs := layers["nodejs"]
	if nodejs == nil {
		t.Fatal("nodejs candy not found")
	}

	// nodejs declares ONLY a top-level package: list (no distro: overrides), so it
	// becomes the always-included BASE (candy.TopPackages) that the cascade
	// resolver folds in for EVERY distro. It is not a format/tag section.
	if !reflect.DeepEqual(nodejs.TopPackages(), []string{"nodejs", "npm"}) {
		t.Errorf("nodejs.TopPackages() = %v, want [nodejs npm]", nodejs.TopPackages())
	}
	if nodejs.FormatSection("rpm") != nil || nodejs.FormatSection("deb") != nil || nodejs.FormatSection("pac") != nil {
		t.Error("nodejs should have no format sections (top-level packages are the base, not a format section)")
	}
	if nodejs.TagSection("fedora") != nil || nodejs.TagSection("debian") != nil {
		t.Error("nodejs should have no tag sections (no distro: map authored)")
	}
}

func TestCandyPacTool(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	pacTool := layers["pac-tool"]
	if pacTool == nil {
		t.Fatal("pac-tool candy not found")
	}

	// pac-tool declares distro.arch → the per-distro "arch" TAG section (bare
	// distro keys route to tag sections now, not a shared format section). The
	// arch.aur sub-block keeps its dedicated "aur" FORMAT section.
	arch := pacTool.TagSection("arch")
	if arch == nil {
		t.Fatal("pac-tool should have an arch tag section")
	}
	if !reflect.DeepEqual(arch.Package, []string{"neovim", "ripgrep"}) {
		t.Errorf("TagSection(arch).Package = %v, want [neovim ripgrep]", arch.Package)
	}
	if pacTool.FormatSection("pac") != nil {
		t.Error("pac-tool should have no pac format section (distro.arch → tag section)")
	}
	// Test raw fields accessible for templates
	repos := buildkit.ToMapSlice(arch.Raw["repo"])
	if len(repos) != 1 {
		t.Errorf("arch repos count = %d, want 1", len(repos))
	}
	options := buildkit.ToStringSlice(arch.Raw["options"])
	if !reflect.DeepEqual(options, []string{"--needed"}) {
		t.Errorf("arch options = %v, want [--needed]", options)
	}

	// Test aur config via generic FormatSection (aur stays a build-format section)
	aur := pacTool.FormatSection("aur")
	if aur == nil {
		t.Fatal("pac-tool should have aur format section")
	}
	if !reflect.DeepEqual(aur.Packages, []string{"yay-bin", "neovim-nightly-bin"}) {
		t.Errorf("FormatSection(aur).Packages = %v, want [yay-bin neovim-nightly-bin]", aur.Packages)
	}

	// Test HasFormatPackages (replaces HasAur)
	if !pacTool.HasFormatPackages() {
		t.Error("pac-tool should have format packages")
	}

	// Test HasInstallFiles
	if !pacTool.HasInstallFiles() {
		t.Error("pac-tool should have install files")
	}
}

func TestCandyCargoTool(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	cargoTool := layers["cargo-tool"]
	if cargoTool == nil {
		t.Fatal("cargo-tool candy not found")
	}

	if !cargoTool.GetHasCargoToml() {
		t.Error("cargo-tool should have Cargo.toml")
	}
	if !cargoTool.HasFile("src") {
		t.Error("cargo-tool should have src/ directory")
	}
}

func TestHasInstallFiles(t *testing.T) {
	// Format-section detection depends on RegisterBuildVocabulary being called first
	// (so unknown top-level keys get routed to FormatSections, not discarded).
	RegisterBuildVocabulary(testDistroConfig())

	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	for name, layer := range layers {
		if !layer.HasInstallFiles() {
			t.Errorf("candy %q should have install files", name)
		}
	}
}

func TestCandyNames(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	names := CandyNames(layers)
	if len(names) != 7 {
		t.Errorf("CandyNames() returned %d names, want 7", len(names))
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("CandyNames() not sorted: %v", names)
			break
		}
	}
}

func TestCandyPorts(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if !ws.HasPorts() {
		t.Error("webservice should have ports")
	}

	ports, err := ws.Port()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if !reflect.DeepEqual(ports, []string{"8080", "9090"}) {
		t.Errorf("Ports() = %v, want [8080 9090]", ports)
	}

	// Test caching
	ports2, err := ws.Port()
	if err != nil {
		t.Fatalf("Ports() second call error = %v", err)
	}
	if !reflect.DeepEqual(ports, ports2) {
		t.Error("Ports() should return cached result")
	}
}

func TestCandyPortsNone(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasPorts() {
		t.Error("pixi should not have ports")
	}

	ports, err := pixi.Port()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if ports != nil {
		t.Errorf("Ports() = %v, want nil", ports)
	}
}

func TestCandyRoute(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if !ws.HasRoute() {
		t.Error("webservice should have route")
	}

	route, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route.Host != "webservice.localhost" {
		t.Errorf("Route().Host = %q, want %q", route.Host, "webservice.localhost")
	}
	if route.Port != "8080" {
		t.Errorf("Route().Port = %q, want %q", route.Port, "8080")
	}

	// Test caching
	route2, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() second call error = %v", err)
	}
	if route != route2 {
		t.Error("Route() should return cached result")
	}
}

func TestCandyRouteNone(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasRoute() {
		t.Error("pixi should not have route")
	}

	route, err := pixi.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route != nil {
		t.Errorf("Route() = %v, want nil", route)
	}
}

func TestCandyPixiLocked(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	locked := layers["pixi-locked"]
	if locked == nil {
		t.Fatal("pixi-locked candy not found")
	}

	if !locked.HasFile("pixi.toml") {
		t.Error("pixi-locked should have pixi.toml")
	}
	if !locked.GetHasPixiLock() {
		t.Error("pixi-locked should have pixi.lock")
	}
	if locked.PixiManifest() != "pixi.toml" {
		t.Errorf("pixi-locked.PixiManifest() = %q, want %q", locked.PixiManifest(), "pixi.toml")
	}
	if !reflect.DeepEqual(bareRefs(locked.GetRequire()), []string{"pixi"}) {
		t.Errorf("pixi-locked.Require = %v, want [pixi]", locked.GetRequire())
	}
}

func TestCandyPixiNoLock(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python candy not found")
	}

	if !python.HasFile("pixi.toml") {
		t.Error("python should have pixi.toml")
	}
	if python.GetHasPixiLock() {
		t.Error("python should not have pixi.lock")
	}
}

func TestCandyVolumes(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if !ws.HasVolumes() {
		t.Error("webservice should have volumes")
	}

	vols := ws.Volume()
	if len(vols) != 1 {
		t.Fatalf("Volumes() returned %d volumes, want 1", len(vols))
	}
	if vols[0].Name != "data" {
		t.Errorf("Volumes()[0].Name = %q, want %q", vols[0].Name, "data")
	}
	if vols[0].Path != "~/.webservice" {
		t.Errorf("Volumes()[0].Path = %q, want %q", vols[0].Path, "~/.webservice")
	}
}

func TestCandyVolumesNone(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasVolumes() {
		t.Error("pixi should not have volumes")
	}
	if len(pixi.Volume()) != 0 {
		t.Errorf("Volumes() = %v, want nil/empty", pixi.Volume())
	}
}

func TestCandyPortRelayFromYAML(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if len(ws.RelayPorts()) == 0 {
		t.Error("webservice should have port_relay")
	}

	relay := ws.RelayPorts()
	if len(relay) != 1 || relay[0] != 8080 {
		t.Errorf("RelayPorts() = %v, want [8080]", relay)
	}
}

func TestCandyPortRelay(t *testing.T) {
	// Test direct struct construction (no testdata file needed)
	layer := testCandy("chrome", spec.CandyModel{
		Plan:           []spec.Step{{Run: "build", Op: cmdOp("true")}},
		PortRelayPorts: []int{9222},
		Port:           []spec.PortSpec{{Port: 9222, Protocol: "http"}},
	}, spec.CandyView{})

	if len(layer.RelayPorts()) == 0 {
		t.Error("candy should have port_relay")
	}
	relay := layer.RelayPorts()
	if len(relay) != 1 || relay[0] != 9222 {
		t.Errorf("RelayPorts() = %v, want [9222]", relay)
	}
}

func TestCandyPortRelayNone(t *testing.T) {
	layer := testCandy("basic", spec.CandyModel{
		Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}},
	}, spec.CandyView{})

	if len(layer.RelayPorts()) != 0 {
		t.Error("basic candy should not have port_relay")
	}
}

func TestCandyPortRelayMultiple(t *testing.T) {
	layer := testCandy("multi", spec.CandyModel{
		Plan:           []spec.Step{{Run: "build", Op: cmdOp("true")}},
		PortRelayPorts: []int{9222, 5900},
		Port:           []spec.PortSpec{{Port: 9222, Protocol: "http"}, {Port: 5900, Protocol: "tcp"}},
	}, spec.CandyView{})

	relay := layer.RelayPorts()
	if len(relay) != 2 {
		t.Fatalf("RelayPorts() returned %d ports, want 2", len(relay))
	}
	if relay[0] != 9222 || relay[1] != 5900 {
		t.Errorf("RelayPorts() = %v, want [9222 5900]", relay)
	}
}
