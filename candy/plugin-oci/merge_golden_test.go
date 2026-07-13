package oci

import (
	"sort"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

// TestMergeEngineGoldenParity is the P14a "S5 parity golden" for the MERGE
// engine (merge.go: planMerge → executeMerge → mergeLayers → whiteoutTarget).
// It captures the CURRENT byte-level output of the engine as a STABLE golden so
// that when the engine relocates to candy/plugin-oci (verb:oci, OpMerge) a later
// cutover can prove the relocated code produces byte-identical output.
//
// The synthetic image exercises every whiteout code path in ONE merge group:
//
//	(a) several small mergeable layers with distinct file paths,
//	(b) a regular whiteout (.wh.<name>) deleting an earlier file,
//	(c) an opaque whiteout (.wh..wh..opq) replacing a directory, and
//	(d) a file re-introduced after its whiteout.
//
// It replicates mergeImageRef's engine call sequence precisely (merge.go:161-210,
// minus the daemon load/save that needs podman): img.Layers() → per-layer Size()
// → planMerge(sizes, maxBytes) → executeMerge(img, layers, steps). All layers are
// tiny, so with the default max_mb they collapse into ONE merge group, and the
// merged layer's DiffID is a content hash over the exact tar bytes mergeLayers
// emits — a strong byte-identical anchor for the relocation.
//
// DETERMINISM NOTE: each layer is built from a SINGLE-entry map. makeTarLayer
// (merge_test.go) iterates a Go map, whose order is randomized per run — a
// MULTI-file layer would therefore have a non-deterministic internal tar order,
// making the merged tar bytes (and thus the DiffID) flake run-to-run. One file
// per layer keeps every layer's internal order trivial and the cross-layer merge
// order equal to the (deterministic) layer order, so the DiffID is stable and
// lockable. See the teammate report for the makeTarLayer hardening this implies
// for any future byte-level merge assertion.
func TestMergeEngineGoldenParity(t *testing.T) {
	// LOCKED GOLDEN — capture the CURRENT engine output; the relocated engine
	// must reproduce these exactly.
	const (
		wantMergedLayerCount = 1
		// DiffID of the single merged layer (sha256 of its uncompressed tar).
		wantMergedDiffID = "sha256:deb131690afd28b5bf45b9da61b1c70f18b77e72cb211d70d7302bdba983b81d"
	)
	// Sorted list of file paths the merged layer contains after whiteout
	// processing (suppressed originals + the moot re-introduction whiteout gone).
	wantMergedPaths := []string{
		"etc/app/.wh.removed.conf", // regular whiteout marker — KEPT
		"etc/app/a.conf",           // distinct base file — KEPT
		"etc/conf.d/.wh..wh..opq",  // opaque whiteout marker — KEPT
		"opt/reintro/config.conf",  // re-introduced after whiteout — KEPT (new content)
		"srv/www/index.html",       // distinct late file — KEPT
		"usr/bin/tool",             // distinct file — KEPT
		"usr/share/data.txt",       // distinct file — KEPT
	}

	// Ordered layer set — one file per layer (see the DETERMINISM NOTE). Order
	// is significant for whiteout semantics: a target must precede its whiteout.
	fileLayers := []struct {
		name    string
		content string
	}{
		{"etc/app/a.conf", "alpha"},                    // (a) distinct, survives
		{"etc/app/removed.conf", "will-be-whiteouted"}, // (b) regular-whiteout target
		{"etc/conf.d/old1.conf", "old1"},               // (c) opaque-whiteout target
		{"etc/conf.d/old2.conf", "old2"},               // (c) opaque-whiteout target
		{"opt/reintro/config.conf", "original"},        // (d) re-introduction target (early)
		{"usr/bin/tool", "toolbin"},                    // (a) distinct
		{"usr/share/data.txt", "somedata"},             // (a) distinct
		{"etc/app/.wh.removed.conf", ""},               // (b) whiteout the earlier removed.conf
		{"etc/conf.d/.wh..wh..opq", ""},                // (c) opaque whiteout of etc/conf.d/
		{"opt/reintro/.wh.config.conf", ""},            // (d) whiteout the re-introduction target
		{"opt/reintro/config.conf", "reintroduced"},    // (d) re-introduce after the whiteout
		{"srv/www/index.html", "<html>"},               // (a) distinct
	}

	img := empty.Image
	for i, fl := range fileLayers {
		layer, err := makeTarLayer(map[string]string{fl.name: fl.content})
		if err != nil {
			t.Fatalf("layer %d (%s): %v", i, fl.name, err)
		}
		img, err = mutate.Append(img, mutate.Addendum{
			Layer:   layer,
			History: v1.History{CreatedBy: "RUN " + fl.name},
		})
		if err != nil {
			t.Fatalf("appending layer %d (%s): %v", i, fl.name, err)
		}
	}

	// --- Replicate mergeImageRef's engine call sequence (merge.go:161-210) ---
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("reading layers: %v", err)
	}
	sizes := make([]int64, len(layers))
	for i, l := range layers {
		sizes[i], err = l.Size()
		if err != nil {
			t.Fatalf("reading layer %d size: %v", i, err)
		}
	}

	maxBytes := int64(defaultMaxMB) * 1024 * 1024
	steps := planMerge(sizes, maxBytes)

	newImg, err := executeMerge(img, layers, steps)
	if err != nil {
		t.Fatalf("executeMerge: %v", err)
	}

	// --- Assert the STABLE golden of the merged output ---
	mergedLayers, err := newImg.Layers()
	if err != nil {
		t.Fatalf("reading merged layers: %v", err)
	}

	t.Logf("merged layer count = %d", len(mergedLayers))
	if len(mergedLayers) != wantMergedLayerCount {
		t.Errorf("merged layer count = %d, want %d", len(mergedLayers), wantMergedLayerCount)
	}

	if len(mergedLayers) >= 1 {
		diffID, err := mergedLayers[0].DiffID()
		if err != nil {
			t.Fatalf("reading merged layer DiffID: %v", err)
		}
		entries, err := readTarEntries(mergedLayers[0])
		if err != nil {
			t.Fatalf("reading merged layer tar: %v", err)
		}
		gotPaths := make([]string, 0, len(entries))
		for name := range entries {
			gotPaths = append(gotPaths, name)
		}
		sort.Strings(gotPaths)

		t.Logf("merged layer[0] DiffID = %s", diffID.String())
		t.Logf("merged layer[0] sorted paths = %#v", gotPaths)

		if diffID.String() != wantMergedDiffID {
			t.Errorf("merged layer[0] DiffID = %s, want %s", diffID.String(), wantMergedDiffID)
		}
		if !equalStringSlice(gotPaths, wantMergedPaths) {
			t.Errorf("merged layer[0] paths = %#v, want %#v", gotPaths, wantMergedPaths)
		}

		// Explicit whiteout-semantics assertions (guard the golden's meaning).
		if _, ok := entries["etc/app/removed.conf"]; ok {
			t.Error("regular whiteout: removed.conf must be suppressed by its whiteout")
		}
		if _, ok := entries["etc/conf.d/old1.conf"]; ok {
			t.Error("opaque whiteout: old1.conf must be suppressed")
		}
		if _, ok := entries["etc/conf.d/old2.conf"]; ok {
			t.Error("opaque whiteout: old2.conf must be suppressed")
		}
		if _, ok := entries["opt/reintro/.wh.config.conf"]; ok {
			t.Error("re-introduction: the moot whiteout must be suppressed")
		}
		if got := entries["opt/reintro/config.conf"]; got != "reintroduced" {
			t.Errorf("re-introduced config.conf content = %q, want %q", got, "reintroduced")
		}
	}
}

// equalStringSlice reports whether two string slices are element-wise equal.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
