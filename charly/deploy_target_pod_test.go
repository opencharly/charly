package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// deploy_target_pod_test.go — guards the pod-overlay BUILD path's host-side invariants after the
// P11c overlay-BUILD dissolution. The core pod overlay target struct + its render assembly MOVED to
// the candy (candy/plugin-deploy-pod/overlay.go); the per-step dispatch ENTRY POINT (ociEmitStep)
// stays core (charly/oci_step_emit.go) as a thin forwarder — the dispatch DECISION itself relocated
// to candy/plugin-installstep's "oci-dispatch" word (K5-A item 2). These tests exercise the host-side
// dispatch entry point (ociEmitStep) + the host-side
// staging (Generator.createRemoteCandyCopies, called by hostBuildOverlay's prep) the candy's
// render depends on. The candy's full buildOverlay (Containerfile assembly + podman build) is
// covered by the candy's own tests + the orchestrator's `charly check run check-pod` bed (the R8
// parity gate).

// TestPodOverlayInlineCopyResolvesUnderContext guards the add_candy-on-pod overlay build's
// context-prefix invariant: a write: step's inline content is staged to <BuildDir>/_inline/<candy>/<hash>
// and the matching Containerfile COPY references it relative to the build context. The overlay
// dispatch (ociEmitStep → dispatchOCIStep → candy/plugin-installstep's "oci-dispatch" → emitOp →
// dg.EmitTasks → emitWrite) must thread ContextRelPrefix == ImageBuildDir (the overlay build dir, relative to
// the build-context root) via the BuildEnv scalars; with an empty ContextRelPrefix the COPY drops
// the build-dir prefix and resolves to a non-existent path, failing the overlay build with
// `COPY … _inline/<candy>/<hash>: stat: no such file or directory`. Regression for that failure;
// mirrors the full build's contextRelPrefix = .build/<boxName>.
func TestPodOverlayInlineCopyResolvesUnderContext(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir); also the plugin's resolved-
	// project cache key (os.Getwd()) — unique per test, so this test's stub can't leak into another.
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	relBuildDir := filepath.Join(".build", "overlay-test")

	stubResolvedProject(t, spec.ResolvedProject{
		Boxes:       map[string]spec.ResolvedBoxView{"base": {Name: "base"}},
		CandyModels: map[string]spec.CandyModel{"marker": {Name: "marker"}},
		Candies:     map[string]spec.CandyView{"marker": {}},
	})
	// The overlay buildEngineContext threads ImageBuildDir == ContextRelPrefix == the overlay
	// build dir (the invariant hostBuildOverlay's prep sets, riding the BuildEnv scalars onto the
	// class:step OpEmit).
	build := buildEngineContext{Box: &spec.ResolvedBox{Name: "base"}, ImageBuildDir: relBuildDir, ContextRelPrefix: relBuildDir}
	tgt := ociTestTarget(build)

	op := &spec.Op{Write: "/etc/marker", Content: "POD-ADDCANDY-MARKER-OK v1\n", Mode: "0644", RunAs: "root"}
	plan := &spec.InstallPlan{Candy: "marker", Steps: []spec.InstallStep{&spec.OpStep{Op: op, CandyName: "marker", ResolvedUser: "root"}}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
		t.Fatalf("overlay emit: %v", err)
	}

	src := inlineCopySrc(t, tgt.String())
	// src is relative to the build context (ctxRoot); the staged file must exist there.
	if _, err := os.Stat(filepath.Join(ctxRoot, src)); err != nil {
		t.Fatalf("inline COPY src %q does not resolve to a staged file under the build context: %v", src, err)
	}
}

// TestCreateRemoteCandyCopies_StagesRemoteCandySource guards Generator.createRemoteCandyCopies
// itself: for a REMOTE candy, it must stage the remote candy's source tree under
// .build/_candy/<name>.<version>/ so the candy's `FROM scratch AS <name>` +
// `COPY <candyCopySource>/ /` resolves. Without it the real overlay build fails at
// `COPY .build/_candy/<name>.<version>/: no such file or directory`. #55 step3 3-II: hostBuildOverlay
// no longer calls this itself — remote-candy staging now runs plugin-side as part of
// candy/plugin-build's resolveBuildEngine (runHostFSPrep, K3 host-prep move), before the overlay
// seam is ever reached. This test exercises the method directly (still a real, reachable core
// function — retention semantics unrelated to hostBuildOverlay's own prep body).
func TestCreateRemoteCandyCopies_StagesRemoteCandySource(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	// Simulate a fetched REMOTE add_candy candy cache dir carrying a copy: source file.
	remoteSrc := filepath.Join(ctxRoot, "remote-cache", "marker")
	if err := os.MkdirAll(remoteSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteSrc, "copied.dat"), []byte("POD-ADDCANDY-COPIED-OK\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const ver = "2026.181.1430"
	candy := testCandy("marker", spec.CandyModel{Version: ver, SourceDir: remoteSrc}, spec.CandyView{
		Remote: true, RepoPath: "github.com/x/y", SubPathPrefix: "candy/",
	})
	gen := &Generator{
		Dir:      ctxRoot,
		BuildDir: filepath.Join(ctxRoot, ".build"), // == g.Dir + "/.build" (the Generator constructors' shared default)
		Candies:  map[string]spec.CandyReader{deploykit.CandyMapKey(candy): candy},
	}

	if err := gen.createRemoteCandyCopies(); err != nil {
		t.Fatalf("createRemoteCandyCopies: %v", err)
	}

	staged := filepath.Join(ctxRoot, ".build", "_candy", "marker."+ver, "copied.dat")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("remote overlay candy source not staged at %s (the per-candy scratch stage's COPY would fail): %v", staged, err)
	}
}

// TestResolveOverlayBaseDistroDef_UsesBaseImageDistroNotHost is the regression for a REAL
// cross-distro bug (#55 step3-II, caught by team-lead's code reading before it shipped): the
// pod-overlay's per-step distro-format vocabulary MUST come from the BASE IMAGE's own declared
// distro (box/fedora's "fedora" box declares `distro: [fedora]`, format `rpm`), never from the
// operator host's distro — a real, not merely latent, bug on any host whose distro differs from
// the base image's (this repo's dev host is commonly Arch/CachyOS, format `pac`/`aur`; the base
// this test resolves is Fedora). Asserts the resolved def carries the `rpm` format (proving the
// BASE box's distro won) and does NOT carry `pac`/`aur` (proving the HOST's distro did not leak
// in) — this assertion would FAIL on the buggy detectHostContext()-sourced code when run on an
// Arch/CachyOS host, exactly the live failure mode team-lead flagged.
func TestResolveOverlayBaseDistroDef_UsesBaseImageDistroNotHost(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(repoRoot), "box", "fedora")
	const base = "fedora"

	t.Cleanup(snapshotProviderState())

	distroCfg, _, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		t.Fatalf("LoadDefaultBuildConfig: %v", err)
	}

	def := resolveOverlayBaseDistroDef(dir, base, distroCfg)
	if def == nil {
		t.Fatalf("resolveOverlayBaseDistroDef(%s): nil def (fixture problem — base box %q must resolve)", base, base)
	}
	if _, ok := def.Format["rpm"]; !ok {
		formats := make([]string, 0, len(def.Format))
		for f := range def.Format {
			formats = append(formats, f)
		}
		t.Fatalf("resolved def has formats %v, want \"rpm\" present (the base image's OWN distro, fedora) — "+
			"a missing rpm format means the base box's distro tag was NOT used", formats)
	}
	for _, hostOnlyFormat := range []string{"pac", "aur"} {
		if _, ok := def.Format[hostOnlyFormat]; ok {
			t.Errorf("resolved def unexpectedly carries format %q — this is the OPERATOR HOST's distro "+
				"(Arch/CachyOS) leaking into the BASE IMAGE's (fedora) per-step rendering, exactly the "+
				"regression team-lead's code reading caught", hostOnlyFormat)
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
