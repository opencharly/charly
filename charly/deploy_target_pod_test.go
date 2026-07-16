package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// deploy_target_pod_test.go — guards the pod-overlay BUILD path's host-side invariants after the
// P11c overlay-BUILD dissolution. The core pod overlay target struct + its render assembly MOVED to
// the candy (candy/plugin-deploy-pod/overlay.go); the per-step dispatch (ociEmitStep) stays core
// (charly/oci_step_emit.go). These tests exercise the core dispatch (ociEmitStep) + the host-side
// staging (Generator.createRemoteCandyCopies, called by hostBuildOverlay's prep) the candy's
// render depends on. The candy's full buildOverlay (Containerfile assembly + podman build) is
// covered by the candy's own tests + the orchestrator's `charly check run check-pod` bed (the R8
// parity gate).

// TestPodOverlayInlineCopyResolvesUnderContext guards the add_candy-on-pod overlay build's
// context-prefix invariant: a write: step's inline content is staged to <BuildDir>/_inline/<candy>/<hash>
// and the matching Containerfile COPY references it relative to the build context. The overlay
// dispatch (ociEmitStep → stepEmitOp → Generator.emitTasks → emitWrite) must thread
// ContextRelPrefix == ImageBuildDir (the overlay build dir, relative to the build-context root);
// with an empty ContextRelPrefix the COPY drops the build-dir prefix and resolves to a non-existent
// path, failing the overlay build with `COPY … _inline/<candy>/<hash>: stat: no such file or directory`.
// Regression for that failure; mirrors the full build's contextRelPrefix = .build/<boxName>.
func TestPodOverlayInlineCopyResolvesUnderContext(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	relBuildDir := filepath.Join(".build", "overlay-test")

	gen := &Generator{Dir: ctxRoot, Candies: map[string]*Candy{"marker": {Name: "marker"}}}
	// The overlay buildEngineContext threads ImageBuildDir == ContextRelPrefix == the overlay
	// build dir (the invariant hostBuildOverlay's prep sets + caches for the step-emit emitter).
	build := buildEngineContext{Generator: gen, Box: &buildkit.ResolvedBox{Name: "base"}, ImageBuildDir: relBuildDir, ContextRelPrefix: relBuildDir}
	tgt := ociTestTarget(build)

	op := &spec.Op{Write: "/etc/marker", Content: "POD-ADDCANDY-MARKER-OK v1\n", Mode: "0644", RunAs: "root"}
	plan := &deploykit.InstallPlan{Candy: "marker", Steps: []spec.InstallStep{&deploykit.OpStep{Op: op, CandyName: "marker", ResolvedUser: "root"}}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("overlay emit: %v", err)
	}

	src := inlineCopySrc(t, tgt.String())
	// src is relative to the build context (ctxRoot); the staged file must exist there.
	if _, err := os.Stat(filepath.Join(ctxRoot, src)); err != nil {
		t.Fatalf("inline COPY src %q does not resolve to a staged file under the build context: %v", src, err)
	}
}

// TestCreateRemoteCandyCopies_StagesRemoteCandySource guards the add_candy-on-pod overlay build for
// a REMOTE candy: createRemoteCandyCopies (the host-side prep step, build_overlay.go) must stage
// the remote candy's source tree under .build/_candy/<name>.<version>/ so the candy's
// `FROM scratch AS <name>` + `COPY <candyCopySource>/ /` resolves. Without it the real overlay build
// fails at `COPY .build/_candy/<name>.<version>/: no such file or directory`. The per-candy scratch-
// stage Containerfile emission now lives in the candy (overlay.go); this core test locks the HOST-SIDE
// staging the candy's render depends on (hostBuildOverlay calls gen.createRemoteCandyCopies).
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
	candy := &Candy{
		Name: "marker", Version: ver, Remote: true, Path: remoteSrc,
		RepoPath: "github.com/x/y", SubPathPrefix: "candy/",
	}
	gen := &Generator{
		Dir:      ctxRoot,
		BuildDir: filepath.Join(ctxRoot, ".build"), // == g.Dir + "/.build" (NewGenerator default)
		Candies:  map[string]*Candy{candyMapKey(candy): candy},
	}

	if err := gen.createRemoteCandyCopies(); err != nil {
		t.Fatalf("createRemoteCandyCopies: %v", err)
	}

	staged := filepath.Join(ctxRoot, ".build", "_candy", "marker."+ver, "copied.dat")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("remote overlay candy source not staged at %s (the per-candy scratch stage's COPY would fail): %v", staged, err)
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
