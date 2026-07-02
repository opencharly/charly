package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPodOverlayInlineCopyResolvesUnderContext guards the add_candy-on-pod overlay
// build: a write: step's inline content is staged to <BuildDir>/_inline/<candy>/<hash>
// and the matching Containerfile COPY references it relative to the build context.
// The overlay OCITarget must set ContextRelPrefix == BuildDir (the overlay build dir,
// relative to the build-context root); with an empty ContextRelPrefix the COPY drops
// the build-dir prefix and resolves to a non-existent path, failing the overlay build
// with `COPY … _inline/<candy>/<hash>: stat: no such file or directory`. Regression
// for that failure; mirrors the full build's contextRelPrefix = .build/<boxName>.
func TestPodOverlayInlineCopyResolvesUnderContext(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	relBuildDir := filepath.Join(".build", "overlay-test")

	gen := &Generator{Dir: ctxRoot, Candies: map[string]*Candy{"marker": {Name: "marker"}}}
	pdt := &PodDeployTarget{Generator: gen, Box: &ResolvedBox{Name: "base"}}
	oci := pdt.overlayOCITarget(relBuildDir)

	// Invariant: inline COPY paths resolve only when ContextRelPrefix == BuildDir.
	if oci.ContextRelPrefix != oci.BuildDir {
		t.Fatalf("overlay OCITarget: ContextRelPrefix=%q != BuildDir=%q — inline COPY paths will not resolve",
			oci.ContextRelPrefix, oci.BuildDir)
	}

	op := &Op{Write: "/etc/marker", Content: "POD-ADDCANDY-MARKER-OK v1\n", Mode: "0644", RunAs: "root"}
	plans := []*InstallPlan{{Candy: "marker", Steps: []InstallStep{&OpStep{Op: op, CandyName: "marker", ResolvedUser: "root"}}}}
	if err := oci.Emit(plans, EmitOpts{}); err != nil {
		t.Fatalf("overlay emit: %v", err)
	}

	src := inlineCopySrc(t, oci.String())
	// src is relative to the build context (ctxRoot); the staged file must exist there.
	if _, err := os.Stat(filepath.Join(ctxRoot, src)); err != nil {
		t.Fatalf("inline COPY src %q does not resolve to a staged file under the build context: %v", src, err)
	}
}

// TestPodOverlayStagesRemoteCandySource guards the add_candy-on-pod overlay build for a
// REMOTE candy carrying a copy: (or cmd:) step. Such a step emits `COPY --from=<candy>` /
// `--mount=type=bind,from=<candy>` against the per-candy `FROM scratch AS <candy>` context
// stage, whose `COPY <candyCopySource>/ /` resolves — for a remote candy — to
// `.build/_candy/<name>.<version>/`. buildOverlay must stage that source tree (the SAME
// createRemoteCandyCopies the full build runs, R3); without it the real overlay build fails
// at `COPY .build/_candy/<name>.<version>/: no such file or directory`. A write:-only marker
// never referenced the scratch stage (emitWrite reads the staged _inline file directly), so
// this path went unexercised until a copy: step was added. Regression for that gap: FAILS
// (staged file absent) without the staging call in buildOverlay.
func TestPodOverlayStagesRemoteCandySource(t *testing.T) {
	// Stub label inspection so buildOverlay's USER-restore ExtractMetadata never shells to podman.
	origLabels := InspectLabels
	defer func() { InspectLabels = origLabels }()
	InspectLabels = func(string, string) (map[string]string, error) { return map[string]string{}, nil }

	ctxRoot := t.TempDir() // the build-context root (the project dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	// Simulate a fetched REMOTE add_candy candy cache dir carrying a copy: source file.
	remoteSrc := filepath.Join(ctxRoot, "remote-cache", "marker")
	if err := os.MkdirAll(remoteSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteSrc, "copied.dat"), []byte("POD-ADDCANDY-COPIED-OK\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const ver = "2026.181.1430"
	candy := &Candy{
		Name: "marker", Version: ver, Remote: true, Path: remoteSrc,
		RepoPath: "github.com/x/y", SubPathPrefix: "candy/",
	}
	gen := &Generator{
		Dir:      ctxRoot,
		BuildDir: filepath.Join(ctxRoot, ".build"), // == g.Dir + "/.build" (NewGenerator default)
		Candies:  map[string]*Candy{candyMapKey(candy): candy},
	}
	pdt := &PodDeployTarget{
		DeployName:      "test-overlay",
		BaseImage:       "base:1",
		Generator:       gen,
		Box:             &ResolvedBox{Name: "base"},
		OverlayBuildDir: filepath.Join(".build", "overlay-test"),
	}

	op := &Op{Copy: "copied.dat", To: "/etc/pod-addcandy-copied", RunAs: "root", Mode: "0644"}
	plans := []*InstallPlan{{
		Candy:      "marker",
		AddCandies: []string{"marker"},
		Steps:      []InstallStep{&OpStep{Op: op, CandyName: "marker", ResolvedUser: "root"}},
	}}

	// DryRun: skips the actual `podman build`, but the staging (createRemoteCandyCopies)
	// runs first regardless, so the assertion below is hermetic.
	if err := pdt.buildOverlay(plans, []string{"marker"}, EmitOpts{DryRun: true}); err != nil {
		t.Fatalf("buildOverlay: %v", err)
	}

	staged := filepath.Join(ctxRoot, ".build", "_candy", "marker."+ver, "copied.dat")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("remote overlay candy source not staged at %s (the per-candy scratch stage's COPY would fail): %v", staged, err)
	}

	// The written overlay Containerfile must (a) emit the per-candy scratch context
	// stage whose COPY source is the staged remote dir, and (b) emit the copy: step's
	// `COPY --from=<candy>` against that stage — together the exact pair the staging
	// makes resolvable.
	cf, err := os.ReadFile(filepath.Join(ctxRoot, ".build", "overlay-test", "Containerfile"))
	if err != nil {
		t.Fatalf("read overlay Containerfile: %v", err)
	}
	cfStr := string(cf)
	for _, want := range []string{
		"FROM scratch AS marker",
		".build/_candy/marker." + ver + "/ /", // scratch-stage COPY of the staged source
		"COPY --from=marker",                  // the copy: step references the scratch stage
		"/etc/pod-addcandy-copied",            // the copy: destination
	} {
		if !strings.Contains(cfStr, want) {
			t.Fatalf("overlay Containerfile missing %q:\n%s", want, cfStr)
		}
	}
}

// inlineCopySrc extracts the COPY source token (the _inline/... path) from a
// rendered Containerfile fragment containing a single inline write COPY.
func inlineCopySrc(t *testing.T, containerfile string) string {
	t.Helper()
	for _, line := range strings.Split(containerfile, "\n") {
		if !strings.HasPrefix(line, "COPY ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.Contains(tok, "_inline/") {
				return tok
			}
		}
	}
	t.Fatalf("no inline COPY directive found in:\n%s", containerfile)
	return ""
}
