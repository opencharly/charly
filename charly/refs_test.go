package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCandyRef(t *testing.T) {
	tests := []struct {
		raw      string
		bare     string
		version  string
		isRemote bool
	}{
		{"python", "python", "", false},
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda", "v1.0.0", true},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda", "", true},
	}
	for _, tt := range tests {
		r := CandyRef{Raw: tt.raw}
		if got := r.Bare(); got != tt.bare {
			t.Errorf("CandyRef{%q}.Bare() = %q, want %q", tt.raw, got, tt.bare)
		}
		if got := r.Version(); got != tt.version {
			t.Errorf("CandyRef{%q}.Version() = %q, want %q", tt.raw, got, tt.version)
		}
		if got := r.IsRemote(); got != tt.isRemote {
			t.Errorf("CandyRef{%q}.IsRemote() = %v, want %v", tt.raw, got, tt.isRemote)
		}
	}
	// A resolved sibling key overrides Bare() but leaves Raw (and thus the
	// transitive-fetch view) intact.
	r := CandyRef{Raw: "ffmpeg", Resolved: "github.com/org/repo/layers/ffmpeg"}
	if r.Bare() != "github.com/org/repo/layers/ffmpeg" {
		t.Errorf("resolved Bare() = %q", r.Bare())
	}
	if r.Raw != "ffmpeg" {
		t.Errorf("resolved must leave Raw intact, got %q", r.Raw)
	}
}

// TestPickCandyVersion covers the per-entity-version arbiter (the sole
// candy-version resolver). Same per-entity `version:` across different git tags
// resolves with NO warning — the newest git tag wins for freshness — which is
// the Problem-B regression guard: a repo re-tag of an UNCHANGED candy must not
// warn. Different per-entity versions warn once and the newest version wins.
func TestPickCandyVersion(t *testing.T) {
	mk := func(ver, tag string) candyCandidate {
		return candyCandidate{
			layer:   &Candy{Name: "x", Version: ver},
			version: ver,
			gitTag:  tag,
			source:  "github.com/o/r@" + tag,
		}
	}
	capture := func(fn func() candyCandidate) (candyCandidate, string) {
		old := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		got := fn()
		_ = w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return got, buf.String()
	}

	// Same per-entity version, different git tags -> NO warning, newest tag wins.
	got, warn := capture(func() candyCandidate {
		return pickCandyVersion("github.com/o/r/layers/x", []candyCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.141.1600", "v2026.150.900"),
		})
	})
	if warn != "" {
		t.Errorf("same per-entity version must not warn, got: %q", warn)
	}
	if got.gitTag != "v2026.150.900" {
		t.Errorf("freshness tiebreak: want newest git tag v2026.150.900, got %q", got.gitTag)
	}

	// Different per-entity versions -> exactly one warning, newest version wins.
	got, warn = capture(func() candyCandidate {
		return pickCandyVersion("github.com/o/r/layers/x", []candyCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.144.0531", "v2026.144.531"),
		})
	})
	if got.version != "2026.144.0531" {
		t.Errorf("newest per-entity version must win, got %q", got.version)
	}
	if !strings.Contains(warn, "resolved to multiple versions") || !strings.Contains(warn, "2026.144.0531") {
		t.Errorf("expected one multi-version warning naming the winner, got: %q", warn)
	}
}

func TestStripVersion(t *testing.T) {
	tests := []struct {
		ref     string
		wantRef string
		wantVer string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "@github.com/org/repo/layers/cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/cuda:main", "@github.com/org/repo/layers/cuda", "main"},
		{"@github.com/org/repo/layers/cuda", "@github.com/org/repo/layers/cuda", ""},
		{"pixi", "pixi", ""},
		{"my-layer", "my-layer", ""},
	}

	for _, tt := range tests {
		gotRef, gotVer := StripVersion(tt.ref)
		if gotRef != tt.wantRef || gotVer != tt.wantVer {
			t.Errorf("StripVersion(%q) = (%q, %q), want (%q, %q)", tt.ref, gotRef, gotVer, tt.wantRef, tt.wantVer)
		}
	}
}

func TestBareRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda"},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda"},
		{"pixi", "pixi"},
		{"my-layer", "my-layer"},
	}

	for _, tt := range tests {
		got := BareRef(tt.ref)
		if got != tt.want {
			t.Errorf("BareRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestParseRemoteRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantRepo string
		wantSub  string
		wantName string
		wantVer  string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo", "layers/cuda", "cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/image:main", "github.com/org/repo", "layers/image", "image", "main"},
		{"@github.com/org/repo/layers/name", "github.com/org/repo", "layers/name", "name", ""},
		{"@github.com/org/repo/image", "github.com/org/repo", "image", "image", ""},
		{"pixi", "", "", "pixi", ""},
	}

	for _, tt := range tests {
		got := ParseRemoteRef(tt.ref)
		if got.RepoPath != tt.wantRepo || got.SubPath != tt.wantSub || got.Name != tt.wantName || got.Version != tt.wantVer {
			t.Errorf("ParseRemoteRef(%q) = {Repo: %q, SubPath: %q, Name: %q, Version: %q}, want {%q, %q, %q, %q}",
				tt.ref, got.RepoPath, got.SubPath, got.Name, got.Version, tt.wantRepo, tt.wantSub, tt.wantName, tt.wantVer)
		}
		if got.Raw != tt.ref {
			t.Errorf("ParseRemoteRef(%q).Raw = %q", tt.ref, got.Raw)
		}
	}
}

func TestIsRemoteCandyRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"pixi", false},
		{"my-layer", false},
		{"@github.com/org/repo/layers/cuda", true},
		{"@github.com/opencharly/charly/layers/cuda", true},
		{"@gitlab.com/org/repo/layers/cuda", true},
		{"@github.com/org/repo/layers/cuda:v1.0.0", true},
		{"github.com/org/repo/layers/cuda", false}, // no @ prefix = not remote
	}

	for _, tt := range tests {
		got := IsRemoteCandyRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteCandyRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestIsRemoteImageRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ollama", false},
		{"@github.com/org/repo/image", true},
		{"@github.com/org/repo/box:v1.0.0", true},
		{"github.com/org/repo/image", false}, // no @ prefix
	}

	for _, tt := range tests {
		got := IsRemoteImageRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteImageRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestScanRemoteCandies(t *testing.T) {
	dir := t.TempDir()
	candiesDir := filepath.Join(dir, "candy")
	if err := os.MkdirAll(filepath.Join(candiesDir, "cuda"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(candiesDir, "python-ml"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(candiesDir, "cuda", "charly.yml"), []byte("candy:\n  name: cuda\n  package:\n    - cuda-toolkit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candiesDir, "python-ml", "charly.yml"), []byte("candy:\n  name: python-ml\n  require:\n    - cuda\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candiesDir, "python-ml", "pixi.toml"), []byte("[project]\nname = \"python-ml\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wantRefs := map[string]bool{
		"github.com/opencharly/ml-layers/candy/cuda":      true,
		"github.com/opencharly/ml-layers/candy/python-ml": true,
	}
	layers, err := ScanRemoteCandy(dir, "github.com/opencharly/ml-layers", wantRefs)
	if err != nil {
		t.Fatalf("ScanRemoteCandy() error = %v", err)
	}

	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}

	cuda, ok := layers["github.com/opencharly/ml-layers/candy/cuda"]
	if !ok {
		t.Fatal("cuda candy not found")
	}
	if !cuda.Remote {
		t.Error("cuda should be remote")
	}
	if cuda.RepoPath != "github.com/opencharly/ml-layers" {
		t.Errorf("cuda.RepoPath = %q", cuda.RepoPath)
	}
	if cuda.Name != "cuda" {
		t.Errorf("cuda.Name = %q, want %q", cuda.Name, "cuda")
	}
	if cuda.SubPathPrefix != "candy/" {
		t.Errorf("cuda.SubPathPrefix = %q, want %q", cuda.SubPathPrefix, "candy/")
	}

	pyml := layers["github.com/opencharly/ml-layers/candy/python-ml"]
	if !pyml.HasPixiToml {
		t.Error("python-ml should have pixi.toml")
	}
	// A remote candy's plain-name sibling dep is qualified at scan time to the
	// sibling's fully-qualified map key, so the dependency graph resolves it
	// against the cuda candy fetched from the same repo (keyed identically).
	wantDep := "github.com/opencharly/ml-layers/candy/cuda"
	if len(pyml.Require) != 1 || pyml.Require[0].Bare() != wantDep {
		t.Errorf("python-ml.Require = %v, want [%s]", pyml.Require, wantDep)
	}
	// CandyRef.Raw preserves the original short-name form for transitive fetch,
	// while .Bare() yields the qualified sibling key the graph resolves on.
	if pyml.Require[0].Raw != "cuda" {
		t.Errorf("python-ml.Require[0].Raw = %q, want cuda", pyml.Require[0].Raw)
	}
}

func TestScanAllCandiesNoRemote(t *testing.T) {
	layers, err := ScanAllCandy("testdata")
	if err != nil {
		t.Fatalf("ScanAllCandy() error = %v", err)
	}

	localCandies, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	if len(layers) != len(localCandies) {
		t.Errorf("len(layers) = %d, want %d", len(layers), len(localCandies))
	}
}

func TestCollectRemoteRefs(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{
					"pixi",
					"@github.com/opencharly/ml-layers/candy/cuda:v1.0.0",
				},
			},
		}),
	}
	layers := map[string]*Candy{
		"pixi": {Name: "pixi", Require: toCandyRefs([]string{})},
		"my-layer": {Name: "my-layer", Require: toCandyRefs([]string{
			"@github.com/myorg/service-layers/layers/svc:v2.0.0",
		})},
	}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	if len(downloads) != 2 {
		t.Fatalf("len(downloads) = %d, want 2", len(downloads))
	}
	// Check that both repos are present
	found := make(map[string]string)
	for _, dl := range downloads {
		found[dl.RepoPath] = dl.Version
	}
	if found["github.com/opencharly/ml-layers"] != "v1.0.0" {
		t.Errorf("ml-layers version = %q, want %q", found["github.com/opencharly/ml-layers"], "v1.0.0")
	}
	if found["github.com/myorg/service-layers"] != "v2.0.0" {
		t.Errorf("service-layers version = %q, want %q", found["github.com/myorg/service-layers"], "v2.0.0")
	}
}

// TestCollectRemoteRefsOptsExtraCandyRefs proves a deploy's add_candy: candy ref
// (passed via ResolveOpts.ExtraCandyRefs) is collected EVEN WHEN no image references
// it — the regression guard for the bed-composition enabler: box/arch's check-arch-vm
// add_candy's the out-of-tree plugin-spice candy with NO candy in the image closure
// requiring it, so without ExtraCandyRefs the plugin would never enter the candy scan
// and the bed's `spice:` checks would fail as an unknown verb. A PINNED ref is used so
// no default-branch git query (network) runs.
func TestCollectRemoteRefsOptsExtraCandyRefs(t *testing.T) {
	// An image config that references NOTHING remote — proves the add_candy ref is
	// collected via ExtraCandyRefs, not via any image-closure edge.
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{"arch": {Candy: []string{"pixi"}}})}
	layers := map[string]*Candy{"pixi": {Name: "pixi", Require: toCandyRefs([]string{})}}

	pluginRef := "@github.com/opencharly/charly/candy/plugin-spice:v2026.174.0425"
	opts := ResolveOpts{ExtraCandyRefs: []string{pluginRef}}
	downloads, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts() error = %v", err)
	}
	var got *RemoteDownload
	for i := range downloads {
		if downloads[i].RepoPath == "github.com/opencharly/charly" {
			got = &downloads[i]
		}
	}
	if got == nil {
		t.Fatalf("ExtraCandyRefs add_candy plugin ref was NOT collected; downloads=%+v", downloads)
	}
	if got.Version != "v2026.174.0425" {
		t.Errorf("plugin-spice download version = %q, want %q", got.Version, "v2026.174.0425")
	}
	if !slices.Contains(got.Refs, BareRef(pluginRef)) {
		t.Errorf("plugin-spice download refs = %v, want to contain %q", got.Refs, BareRef(pluginRef))
	}

	// A LOCAL ExtraCandyRef is a no-op (already covered by ScanCandy): collecting it
	// adds no remote download.
	localOnly, err := CollectRemoteRefsOpts(cfg, layers, ResolveOpts{ExtraCandyRefs: []string{"plugin-spice"}})
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts(local extra) error = %v", err)
	}
	if len(localOnly) != 0 {
		t.Errorf("local ExtraCandyRef produced %d downloads, want 0", len(localOnly))
	}
}

func TestCollectRemoteRefsLocalTemplate(t *testing.T) {
	// kind:local template candy: lists must feed the same remote-ref collection
	// path as image candy: lists (regression guard for the 2026-05 CachyOS
	// migration, where the charly-cachyos kind:local template composes 30 remote
	// @-ref candies — previously invisible to CollectRemoteRefs).
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{
					"@github.com/opencharly/charly/layers/pixi:v1.0.0",
				},
			},
		}),
		Local: rawTemplateMap(map[string]*LocalSpec{
			"workstation": {
				Candy: []string{
					"@github.com/opencharly/charly/layers/nvidia:v1.0.0",
					"@github.com/myorg/extra-layers/layers/svc:v2.0.0",
				},
			},
		}),
	}
	layers := map[string]*Candy{}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	found := make(map[string]string)
	for _, dl := range downloads {
		found[dl.RepoPath] = dl.Version
	}
	// The image ref and the kind:local template ref share a repo at the same
	// version → one download for charly, one for the extra repo.
	if found["github.com/opencharly/charly"] != "v1.0.0" {
		t.Errorf("charly version = %q, want %q", found["github.com/opencharly/charly"], "v1.0.0")
	}
	if found["github.com/myorg/extra-layers"] != "v2.0.0" {
		t.Errorf("extra-layers version = %q, want %q (kind:local template ref not collected)", found["github.com/myorg/extra-layers"], "v2.0.0")
	}
}

func TestCollectRemoteRefsOptsIncludeDisabled(t *testing.T) {
	// A disabled image's remote candy refs must be collected when a
	// `--include-disabled <name>` build scopes IncludeDisabled to that image —
	// so the FETCH set (CollectRemoteRefsOpts) stays in lockstep with the
	// RESOLVE set (ResolveAllBox/GlobalCandyOrder). Regression guard for the
	// 2026-05 deb-family split: no enabled debian image references `pixi`, so a
	// disabled `debian-builder --include-disabled` would otherwise hit
	// "unknown layer .../pixi" in computing global candy order.
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"debian-builder": {
				Enabled: new(false),
				Candy: []string{
					"@github.com/opencharly/charly/layers/pixi:v1.0.0",
				},
			},
		}),
	}
	layers := map[string]*Candy{}

	// Default opts (enabled-only) → the disabled image is skipped, no downloads.
	if dls, err := CollectRemoteRefs(cfg, layers); err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	} else if len(dls) != 0 {
		t.Fatalf("default opts: len(downloads) = %d, want 0 (disabled image skipped)", len(dls))
	}

	// Scoped --include-disabled debian-builder → the ref IS collected.
	opts := ResolveOpts{IncludeDisabled: true, IncludeDisabledNames: map[string]bool{"debian-builder": true}}
	dls, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts() error = %v", err)
	}
	found := make(map[string]string)
	for _, dl := range dls {
		found[dl.RepoPath] = dl.Version
	}
	if found["github.com/opencharly/charly"] != "v1.0.0" {
		t.Errorf("scoped include-disabled: charly version = %q, want %q (disabled image's remote layer not collected)", found["github.com/opencharly/charly"], "v1.0.0")
	}

	// A DIFFERENT disabled image must stay filtered under the scoped opts.
	cfg.SetBox("other-disabled", BoxConfig{
		Enabled: new(false),
		Candy:   []string{"@github.com/myorg/other/layers/x:v3.0.0"},
	})
	dls2, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts() error = %v", err)
	}
	for _, dl := range dls2 {
		if dl.RepoPath == "github.com/myorg/other" {
			t.Errorf("scoped opts leaked an unscoped disabled image's refs: %s", dl.RepoPath)
		}
	}
}

func TestCollectRemoteRefsDefaultsBuilderTransitiveCandies(t *testing.T) {
	// An image whose builder comes from defaults.builder (a NAMESPACED builder,
	// with NO per-image builder: block) must still have that builder's transitive
	// candies fetched — collectBox follows the EFFECTIVE builder edge
	// (effectiveBuilderForBox → resolveEffectiveBuilder), not the empty raw
	// img.Builder. Regression guard for the bazzite/aurora
	// "unknown layer .../rpmfusion" under-collection: the builder's candy lives in
	// a DISTINCT repo, so it appears in downloads ONLY if the defaults-supplied
	// builder edge was actually followed (it was absent before the fix, because
	// the raw per-image img.Builder these images carry is empty).
	cfg := &Config{
		Defaults: BoxConfig{Builder: BuilderMap{"pixi": "charly.fedora-builder"}},
		Box: boxMapOf(map[string]BoxConfig{
			"bazzite": {
				Base:  "ghcr.io/ublue-os/bazzite:stable", // external base
				Candy: []string{"@github.com/opencharly/charly/layers/foo:v1.0.0"},
				// NO per-image Builder: — supplied by defaults.builder above.
			},
		}),
		Namespaces: map[string]*Config{
			"charly": {
				Box: boxMapOf(map[string]BoxConfig{
					"fedora-builder": {
						Base:    "quay.io/fedora/fedora:43",
						Produce: []string{"pixi"},
						Candy:   []string{"@github.com/buildorg/build-layers/layers/rpmfusion:v1.0.0"},
					},
				}),
			},
		},
	}
	layers := map[string]*Candy{}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	found := make(map[string]string)
	for _, dl := range downloads {
		found[dl.RepoPath] = dl.Version
	}
	// Sanity: the image's own candy repo is always collected.
	if found["github.com/opencharly/charly"] != "v1.0.0" {
		t.Errorf("image's own layer not collected: charly = %q", found["github.com/opencharly/charly"])
	}
	// The fix: charly.fedora-builder (from defaults.builder) is built as an
	// intermediate, so its transitive candy's repo MUST be collected. Absent
	// before the fix (raw img.Builder was empty) → "unknown layer" at generate.
	if found["github.com/buildorg/build-layers"] != "v1.0.0" {
		t.Errorf("defaults.builder's transitive layer not collected: build-layers = %q, want %q "+
			"(builder edge followed via raw img.Builder instead of the effective builder?)",
			found["github.com/buildorg/build-layers"], "v1.0.0")
	}
}

func TestCollectRemoteRefsSameCandyBothTagsCollected(t *testing.T) {
	// Same bare ref at two git tags: collection now emits BOTH (the git tag is
	// only the FETCH coordinate). Per-entity-version arbitration (newest-wins,
	// or no-warning when the candy's own version: matches) happens AFTER fetch in
	// pickCandyVersion — see TestPickCandyVersion. Collection's job is just to
	// fetch every distinct (repo, git-tag).
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{
					"@github.com/org/repo/layers/cuda:v2.0.0",
				},
			},
		}),
	}
	layers := map[string]*Candy{
		"local": {Name: "local", Version: "2026.001.0001", Require: toCandyRefs([]string{
			"@github.com/org/repo/layers/cuda:v1.0.0",
		})},
	}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() unexpected error: %v", err)
	}
	cudaVers := map[string]bool{}
	for _, dl := range downloads {
		for _, ref := range dl.Refs {
			if ref == "github.com/org/repo/layers/cuda" {
				cudaVers[dl.Version] = true
			}
		}
	}
	if !cudaVers["v1.0.0"] || !cudaVers["v2.0.0"] || len(cudaVers) != 2 {
		t.Errorf("cuda fetch coordinates = %v, want both [v1.0.0 v2.0.0] collected", cudaVers)
	}
}

func TestCollectRemoteRefsDifferentCandiesSameRepo(t *testing.T) {
	// Different candies from same repo at different versions should be OK
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{
					"@github.com/org/repo/layers/cuda:v1.0.0",
					"@github.com/org/repo/layers/python:v2.0.0",
				},
			},
		}),
	}
	layers := map[string]*Candy{}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() unexpected error: %v", err)
	}
	// Should have 2 downloads (same repo, different versions)
	if len(downloads) != 2 {
		t.Fatalf("len(downloads) = %d, want 2", len(downloads))
	}
}

func TestParseDefaultBranch(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"ref: refs/heads/main\tHEAD\nabc123\tHEAD\n", "main"},
		{"ref: refs/heads/master\tHEAD\ndef456\tHEAD\n", "master"},
		{"ref: refs/heads/develop\tHEAD\n789abc\tHEAD\n", "develop"},
		{"abc123\tHEAD\n", ""}, // no symref line
		{"", ""},               // empty output
	}

	for _, tt := range tests {
		got := parseDefaultBranch(tt.output)
		if got != tt.want {
			t.Errorf("parseDefaultBranch(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestParseTagRefs(t *testing.T) {
	output := `abc123def456	refs/tags/v0.1.0
def456abc789	refs/tags/v0.1.0^{}
111222333444	refs/tags/v1.0.0
555666777888	refs/tags/v1.0.0^{}
aaa111bbb222	refs/tags/v2.0.0
ccc333ddd444	refs/tags/v2.0.0^{}
eee555fff666	refs/tags/not-semver
`
	tags := parseTagRefs(output)
	if len(tags) != 3 {
		t.Fatalf("len(tags) = %d, want 3", len(tags))
	}
	// Should contain v0.1.0, v1.0.0, v2.0.0 (no ^{} or non-v tags)
	want := map[string]bool{"v0.1.0": true, "v1.0.0": true, "v2.0.0": true}
	for _, tag := range tags {
		if !want[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v1.0.0", 1},
		{"v1.0.0", "v1.1.0", -1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.9.0", "v1.10.0", -1},
		{"v0.1.0", "v1.0.0", -1},
	}

	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abc123", true},
		{"ABC123", true},
		{"deadbeef", true},
		{"", false},
		{"xyz", false},
		{"abc 123", false},
	}

	for _, tt := range tests {
		got := isHex(tt.s)
		if got != tt.want {
			t.Errorf("isHex(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestRepoGitURL(t *testing.T) {
	got := RepoGitURL("github.com/opencharly/ml-layers")
	want := "https://github.com/opencharly/ml-layers.git"
	if got != want {
		t.Errorf("RepoGitURL() = %q, want %q", got, want)
	}
}

func TestDiscoverRemoteCandies(t *testing.T) {
	dir := t.TempDir()
	candiesDir := filepath.Join(dir, "candy")
	if err := os.MkdirAll(filepath.Join(candiesDir, "beta"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(candiesDir, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candiesDir, "README.md"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	names, err := DiscoverRemoteCandy(dir)
	if err != nil {
		t.Fatalf("DiscoverRemoteCandy() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("len(names) = %d, want 2", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("names = %v, want [alpha beta]", names)
	}
}

func TestCandyCopySource(t *testing.T) {
	g := &Generator{
		Candies: map[string]*Candy{
			"pixi":                              {Name: "pixi", Remote: false},
			"github.com/test/repo/layers/cuda":  {Name: "cuda", Version: "2026.167.1200", Remote: true, RepoPath: "github.com/test/repo"},
			"github.com/test/repo/layers/nover": {Name: "nover", Remote: true, RepoPath: "github.com/test/repo"},
		},
	}

	if got := g.candyCopySource("pixi"); got != "candy/pixi" {
		t.Errorf("local candy: got %q, want %q", got, "candy/pixi")
	}
	// Remote candies stage under the VERSION-keyed .build/_candy/<name>.<version>/
	// so distinct candy versions never share a dir (cross-version crossover).
	if got := g.candyCopySource("github.com/test/repo/layers/cuda"); got != ".build/_candy/cuda.2026.167.1200" {
		t.Errorf("remote candy: got %q, want %q", got, ".build/_candy/cuda.2026.167.1200")
	}
	// Defensive: a version-less remote candy (should never happen — the resolver
	// hard-errors) falls back to the bare name rather than a trailing-dot dir.
	if got := g.candyCopySource("github.com/test/repo/layers/nover"); got != ".build/_candy/nover" {
		t.Errorf("version-less remote candy: got %q, want %q", got, ".build/_candy/nover")
	}
}

// TestRepoOverrideDir covers the CHARLY_REPO_OVERRIDE parser: exact + short-form
// match, miss, multi-pair, and the loud-failure cases (malformed, missing dir,
// non-directory). The RDD local-override is the supported "verify before push".
func TestRepoOverrideDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("unset", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "")
		if d, ok, err := repoOverrideDir("github.com/opencharly/charly"); ok || d != "" || err != nil {
			t.Fatalf("want empty/false/nil, got %q %v %v", d, ok, err)
		}
	})

	t.Run("exact match", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "github.com/opencharly/charly="+dir)
		d, ok, err := repoOverrideDir("github.com/opencharly/charly")
		if err != nil || !ok || d != dir {
			t.Fatalf("want %q/true/nil, got %q %v %v", dir, d, ok, err)
		}
	})

	t.Run("short form auto-prefixes github.com", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "opencharly/charly="+dir)
		d, ok, err := repoOverrideDir("github.com/opencharly/charly")
		if err != nil || !ok || d != dir {
			t.Fatalf("want %q/true/nil, got %q %v %v", dir, d, ok, err)
		}
	})

	t.Run("non-matching repo falls through", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "github.com/other/repo="+dir)
		if d, ok, err := repoOverrideDir("github.com/opencharly/charly"); ok || d != "" || err != nil {
			t.Fatalf("want empty/false/nil, got %q %v %v", d, ok, err)
		}
	})

	t.Run("second pair matches", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "github.com/a/b=/nope, opencharly/charly="+dir)
		d, ok, err := repoOverrideDir("github.com/opencharly/charly")
		if err != nil || !ok || d != dir {
			t.Fatalf("want %q/true/nil, got %q %v %v", dir, d, ok, err)
		}
	})

	t.Run("malformed entry errors", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "no-equals-sign")
		if _, _, err := repoOverrideDir("github.com/opencharly/charly"); err == nil {
			t.Fatal("want error for malformed entry, got nil")
		}
	})

	t.Run("missing dir errors", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "opencharly/charly=/does/not/exist/anywhere")
		if _, _, err := repoOverrideDir("github.com/opencharly/charly"); err == nil {
			t.Fatal("want error for missing dir, got nil")
		}
	})

	t.Run("non-directory errors", func(t *testing.T) {
		t.Setenv(RepoOverrideEnv, "opencharly/charly="+file)
		if _, _, err := repoOverrideDir("github.com/opencharly/charly"); err == nil {
			t.Fatal("want error for non-directory target, got nil")
		}
	})
}

// TestEnsureRepoDownloaded_Override proves the override short-circuits the cache
// + remote fetch entirely (offline: no network, ignores the :vTAG version) and
// never migrates the dev's live tree.
func TestEnsureRepoDownloaded_Override(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(RepoOverrideEnv, "github.com/foo/bar="+dir)
	got, err := EnsureRepoDownloaded("github.com/foo/bar", "v9999.1.1")
	if err != nil {
		t.Fatalf("override should resolve offline, got err: %v", err)
	}
	if got != dir {
		t.Fatalf("want override dir %q, got %q", dir, got)
	}
}
