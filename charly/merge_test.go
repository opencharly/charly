package main

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const mb = 1024 * 1024

// TestPlanMerge_AllFitOneGroup verifies all layers merge into one group when they fit.
func TestPlanMerge_AllFitOneGroup(t *testing.T) {
	sizes := []int64{10 * mb, 20 * mb, 30 * mb, 15 * mb}
	steps := planMerge(sizes, 1024*mb)

	if len(steps) != 1 {
		t.Fatalf("expected 1 step (merged group), got %d", len(steps))
	}
	if steps[0].Keep {
		t.Error("expected Keep=false for merge group")
	}
	if len(steps[0].Layers) != 4 {
		t.Errorf("expected 4 layers in group, got %d", len(steps[0].Layers))
	}
}

// TestPlanMerge_MaxMBSplit verifies group splits when max_mb is exceeded.
func TestPlanMerge_MaxMBSplit(t *testing.T) {
	sizes := []int64{40 * mb, 40 * mb, 40 * mb, 40 * mb}
	steps := planMerge(sizes, 100*mb) // max=100MB

	// 40+40=80 fits, 80+40=120 doesn't -> group [0,1], then [2,3]
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Keep || len(steps[0].Layers) != 2 {
		t.Errorf("step 0: expected merge of 2 layers, got Keep=%v Layers=%v", steps[0].Keep, steps[0].Layers)
	}
	if steps[1].Keep || len(steps[1].Layers) != 2 {
		t.Errorf("step 1: expected merge of 2 layers, got Keep=%v Layers=%v", steps[1].Keep, steps[1].Layers)
	}
}

// TestPlanMerge_LargeLayerAlone verifies a layer exceeding max_mb stays alone.
func TestPlanMerge_LargeLayerAlone(t *testing.T) {
	sizes := []int64{10 * mb, 300 * mb, 20 * mb}
	steps := planMerge(sizes, 256*mb)

	// 10 fits, 10+300=310 > 256 -> flush [0] (single, kept), 300 alone (kept), 20 alone (kept)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	for _, step := range steps {
		if !step.Keep {
			t.Errorf("expected all steps to be Keep=true (single-layer groups), got merge for %v", step.Layers)
		}
	}
}

// TestPlanMerge_MixedSizes verifies grouping with varied sizes.
func TestPlanMerge_MixedSizes(t *testing.T) {
	sizes := []int64{50 * mb, 50 * mb, 50 * mb, 200 * mb, 30 * mb, 30 * mb}
	steps := planMerge(sizes, 200*mb)

	// 50+50+50=150 fits, 150+200=350 doesn't -> merge [0,1,2]
	// 200 alone (kept), 200+30=230 doesn't -> flush [3] (kept)
	// 30+30=60 fits -> merge [4,5]
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0].Keep || len(steps[0].Layers) != 3 {
		t.Errorf("step 0: expected merge of 3 layers, got Keep=%v Layers=%v", steps[0].Keep, steps[0].Layers)
	}
	if !steps[1].Keep {
		t.Error("step 1: expected Keep=true for 200MB layer")
	}
	if steps[2].Keep || len(steps[2].Layers) != 2 {
		t.Errorf("step 2: expected merge of 2 layers, got Keep=%v Layers=%v", steps[2].Keep, steps[2].Layers)
	}
}

// makeTarLayer creates a synthetic layer containing the given files.
func makeTarLayer(files map[string]string) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Size: int64(len(content)),
			Mode: 0644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// readTarEntries reads all tar entries from a layer into a map.
func readTarEntries(layer v1.Layer) (map[string]string, error) {
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	entries := make(map[string]string)
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var content bytes.Buffer
		if hdr.Size > 0 {
			if _, err := io.Copy(&content, tr); err != nil {
				return nil, err
			}
		}
		entries[hdr.Name] = content.String()
	}
	return entries, nil
}

// TestMergeLayers verifies tar entries from multiple layers are combined.
func TestMergeLayers(t *testing.T) {
	layer1, err := makeTarLayer(map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})
	if err != nil {
		t.Fatal(err)
	}

	layer2, err := makeTarLayer(map[string]string{
		"c.txt": "foo",
		"b.txt": "overwritten", // should override b.txt from layer1
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	// All entries should be present (tar allows duplicates; later wins at extract time)
	if len(entries) < 3 {
		t.Errorf("expected at least 3 entries, got %d: %v", len(entries), entries)
	}
	if entries["c.txt"] != "foo" {
		t.Errorf("expected c.txt=foo, got %q", entries["c.txt"])
	}
}

// TestMergeLayers_Whiteout verifies whiteout files are preserved.
func TestMergeLayers_Whiteout(t *testing.T) {
	layer1, err := makeTarLayer(map[string]string{
		"usr/bin/app": "binary",
	})
	if err != nil {
		t.Fatal(err)
	}

	layer2, err := makeTarLayer(map[string]string{
		"usr/bin/.wh.app": "", // whiteout
		"usr/bin/app2":    "new binary",
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := entries["usr/bin/.wh.app"]; !ok {
		t.Error("whiteout file usr/bin/.wh.app not preserved")
	}
	if _, ok := entries["usr/bin/app2"]; !ok {
		t.Error("new file usr/bin/app2 not preserved")
	}
}

// TestHistoryAlignment verifies empty-layer history entries are preserved correctly.
func TestHistoryAlignment(t *testing.T) {
	// Build a synthetic image with layers and mixed history
	layer1, _ := makeTarLayer(map[string]string{"a": "1"})
	layer2, _ := makeTarLayer(map[string]string{"b": "2"})
	layer3, _ := makeTarLayer(map[string]string{"c": "3"})

	img := empty.Image
	img, _ = mutate.Append(img,
		mutate.Addendum{
			Layer:   layer1,
			History: v1.History{CreatedBy: "RUN step1"},
		},
		mutate.Addendum{
			History: v1.History{CreatedBy: "ENV FOO=bar", EmptyLayer: true},
		},
		mutate.Addendum{
			Layer:   layer2,
			History: v1.History{CreatedBy: "RUN step2"},
		},
		mutate.Addendum{
			Layer:   layer3,
			History: v1.History{CreatedBy: "RUN step3"},
		},
		mutate.Addendum{
			History: v1.History{CreatedBy: "USER 1000", EmptyLayer: true},
		},
	)

	layers, _ := img.Layers()
	sizes := make([]int64, len(layers))
	for i, l := range layers {
		sizes[i], _ = l.Size()
	}

	// All layers are tiny, so they should all merge
	steps := planMerge(sizes, 1024*mb)

	// All 3 layers are small -> one merge group
	if len(steps) != 1 || steps[0].Keep {
		t.Fatalf("expected 1 merge step, got %d steps", len(steps))
	}

	newImg, err := executeMerge(img, layers, steps)
	if err != nil {
		t.Fatal(err)
	}

	cf, _ := newImg.ConfigFile()

	// Should have: merged-layer history, ENV empty history, USER empty history
	// The empty-layer entries should be preserved
	emptyCount := 0
	nonEmptyCount := 0
	for _, h := range cf.History {
		if h.EmptyLayer {
			emptyCount++
		} else {
			nonEmptyCount++
		}
	}

	if nonEmptyCount != 1 {
		t.Errorf("expected 1 non-empty history entry (merged), got %d", nonEmptyCount)
	}
	if emptyCount != 2 {
		t.Errorf("expected 2 empty history entries (ENV + USER), got %d", emptyCount)
	}

	newLayers, _ := newImg.Layers()
	if len(newLayers) != 1 {
		t.Errorf("expected 1 layer after merge, got %d", len(newLayers))
	}
}

// TestMergeLayers_WhiteoutSuppressesOriginal verifies that when a later layer whiteouts
// a file from an earlier layer, the original file is suppressed from the merged output.
// This prevents "file exists" errors during overlay unpack when both the file and its
// whiteout would otherwise coexist in the same merged layer.
func TestMergeLayers_WhiteoutSuppressesOriginal(t *testing.T) {
	// Layer 1: contains a file that will be deleted
	layer1, err := makeTarLayer(map[string]string{
		"usr/share/dbus-1/services/swaync.service": "service content",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 2: whiteouts the file from layer 1
	layer2, err := makeTarLayer(map[string]string{
		"usr/share/dbus-1/services/.wh.swaync.service": "", // whiteout
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	// The whiteout must be present
	if _, ok := entries["usr/share/dbus-1/services/.wh.swaync.service"]; !ok {
		t.Error("whiteout .wh.swaync.service must be present in merged output")
	}

	// The original file MUST be suppressed — it cannot coexist with its whiteout
	// in the same layer or overlay unpack will fail with "file exists"
	if _, ok := entries["usr/share/dbus-1/services/swaync.service"]; ok {
		t.Error("original swaync.service must be suppressed by whiteout — coexistence causes overlay unpack failure")
	}
}

// TestMergeLayers_OpaqueWhiteout verifies that an opaque whiteout suppresses all
// non-whiteout entries under the directory from earlier layers.
func TestMergeLayers_OpaqueWhiteout(t *testing.T) {
	// Layer 1: populates a directory
	layer1, err := makeTarLayer(map[string]string{
		"etc/conf.d/old.conf":   "old config",
		"etc/conf.d/other.conf": "other config",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 2: opaque whiteout + new content (replaces entire directory)
	layer2, err := makeTarLayer(map[string]string{
		"etc/conf.d/.wh..wh..opq": "", // opaque whiteout
		"etc/conf.d/new.conf":     "new config",
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	// Old entries must be suppressed
	if _, ok := entries["etc/conf.d/old.conf"]; ok {
		t.Error("old.conf must be suppressed by opaque whiteout")
	}
	if _, ok := entries["etc/conf.d/other.conf"]; ok {
		t.Error("other.conf must be suppressed by opaque whiteout")
	}

	// New entry and opaque whiteout must be present
	if _, ok := entries["etc/conf.d/.wh..wh..opq"]; !ok {
		t.Error("opaque whiteout must be present in merged output")
	}
	if _, ok := entries["etc/conf.d/new.conf"]; !ok {
		t.Error("new.conf must be present in merged output")
	}
}

// TestMergeLayers_WhiteoutSupersededByReintroduction verifies that when a file is
// re-introduced after its whiteout within the same merge group, the whiteout is
// suppressed (not the file). This prevents "file exists" EEXIST errors during overlay
// unpack when both the re-introduced file and its now-moot whiteout would otherwise
// coexist in the same merged layer.
func TestMergeLayers_WhiteoutSupersededByReintroduction(t *testing.T) {
	// Layer 0: file installed
	layer0, err := makeTarLayer(map[string]string{
		"usr/lib/app/config.conf": "original content",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 1: file whiteout-ed (deleted)
	layer1, err := makeTarLayer(map[string]string{
		"usr/lib/app/.wh.config.conf": "",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 2: file re-introduced (new version)
	layer2, err := makeTarLayer(map[string]string{
		"usr/lib/app/config.conf": "new content",
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer0, layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	// The re-introduced file must be present with its new content
	if content, ok := entries["usr/lib/app/config.conf"]; !ok {
		t.Error("re-introduced config.conf must be present in merged output")
	} else if content != "new content" {
		t.Errorf("config.conf content = %q, want %q", content, "new content")
	}

	// The superseded whiteout must be suppressed — coexistence causes EEXIST during overlay unpack
	if _, ok := entries["usr/lib/app/.wh.config.conf"]; ok {
		t.Error("superseded .wh.config.conf must be suppressed — coexistence with re-introduced file causes EEXIST")
	}
}
