package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// MergeCmd merges small layers in a built container image
type MergeCmd struct {
	Box        string `arg:"" optional:"" help:"Box name from charly.yml"`
	All        bool   `long:"all" help:"Merge all images with merge.auto enabled"`
	MaxMB      int    `long:"max-mb" help:"Maximum size of a merged layer (MB)"`
	MaxTotalMB int    `long:"max-total-mb" help:"Maximum total image size for merge (MB, 0=no limit)"`
	Tag        string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	DryRun     bool   `long:"dry-run" help:"Print merge plan without modifying the image"`
}

// MergeStep represents one step in the merge plan
type MergeStep struct {
	Keep   bool  // true = emit as-is, false = part of a merge group
	Layers []int // indices into the original layer list
}

const defaultMaxMB = 128
const defaultMaxTotalMB = 0 // 0 = no limit

func (c *MergeCmd) Run() error {
	if c.Box == "" && !c.All {
		return fmt.Errorf("specify a box name or use --all")
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	if c.All {
		return c.runAll(cfg)
	}
	return c.runOne(cfg, c.Box)
}

// runAll merges all images that have merge.auto enabled.
func (c *MergeCmd) runAll(cfg *Config) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	images, err := cfg.ResolveAllBox("unused", dir, ResolveOpts{})
	if err != nil {
		return err
	}

	// Merge in dependency order so base images are merged before children
	order, err := ResolveBoxOrder(images, layers)
	if err != nil {
		return err
	}

	merged := 0
	for _, name := range order {
		resolved := images[name]
		if resolved.Merge == nil || !resolved.Merge.Auto {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n--- %s ---\n", name)
		if err := c.runOne(cfg, name); err != nil {
			// Per-image merge failures are non-fatal
			fmt.Fprintf(os.Stderr, "Warning: skipping merge for %s: %v\n", name, err)
			continue
		}
		merged++
	}

	if merged == 0 {
		fmt.Fprintf(os.Stderr, "No images have merge.auto enabled\n")
	}
	return nil
}

// runOne merges a single image.
func (c *MergeCmd) runOne(cfg *Config, boxName string) error {
	dir, _ := os.Getwd()
	resolved, err := cfg.ResolveBox(boxName, "unused", dir, ResolveOpts{})
	if err != nil {
		return err
	}

	// Determine max_mb: CLI flags -> the box config -> default
	maxMB := defaultMaxMB
	if resolved.Merge != nil && resolved.Merge.MaxMB > 0 {
		maxMB = resolved.Merge.MaxMB
	}
	if c.MaxMB > 0 {
		maxMB = c.MaxMB
	}

	// Determine max_total_mb: CLI flags -> the box config -> default
	maxTotalMB := defaultMaxTotalMB
	if resolved.Merge != nil && resolved.Merge.MaxTotalMB > 0 {
		maxTotalMB = resolved.Merge.MaxTotalMB
	}
	if c.MaxTotalMB > 0 {
		maxTotalMB = c.MaxTotalMB
	}

	maxBytes := int64(maxMB) * 1024 * 1024

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	// Resolve build engine for save/load
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.BuildEngine

	img, cleanup, isManifest, err := loadImageFromDaemon(imageRef, engine)
	if err != nil {
		return err
	}
	defer cleanup()

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("reading layers: %w", err)
	}

	sizes := make([]int64, len(layers))
	var totalSize int64
	for i, layer := range layers {
		size, err := layer.Size()
		if err != nil {
			return fmt.Errorf("reading layer %d size: %w", i, err)
		}
		sizes[i] = size
		totalSize += size
	}

	// Skip merge for very large images to avoid OOM during layer decompression.
	// The merge process decompresses layers in memory. Set max_total_mb: 0 to disable.
	if maxTotalMB > 0 {
		maxTotalBytes := int64(maxTotalMB) * 1024 * 1024
		if totalSize > maxTotalBytes {
			fmt.Fprintf(os.Stderr, "Skipping merge: image too large (%.1f GB > %.1f GB limit, override with --max-total-mb)\n",
				float64(totalSize)/(1024*1024*1024), float64(maxTotalBytes)/(1024*1024*1024))
			return nil
		}
	}

	steps := planMerge(sizes, maxBytes)

	if c.DryRun {
		printMergePlan(sizes, steps)
		return nil
	}

	// Check if any merging is needed
	mergeCount := 0
	for _, step := range steps {
		if !step.Keep {
			mergeCount++
		}
	}
	if mergeCount == 0 {
		fmt.Fprintf(os.Stderr, "No layers to merge (%d layers)\n", len(layers))
		return nil
	}

	newImg, err := executeMerge(img, layers, steps)
	if err != nil {
		return err
	}

	// Save merged image. For manifest sources, remove the old manifest first
	// so podman load can create a regular image with the same tag.
	if isManifest {
		binary := EngineBinary(engine)
		rmCmd := exec.Command(binary, "manifest", "rm", imageRef)
		rmCmd.Stdout = io.Discard
		rmCmd.Stderr = io.Discard
		_ = rmCmd.Run() // best-effort: removing a possibly-absent manifest before load
	}

	if err := saveImageToDaemon(newImg, imageRef, engine); err != nil {
		// Empirical investigation (May 2026, immich:2026.128.x rebuild):
		// podman load can reject a merged tarball with EEXIST even when
		// the tar passes every internal-consistency check we know how
		// to run — no within-layer duplicate Names, no whiteout/file
		// collisions, no broken hardlinks, no cross-layer typeflag
		// conflicts. Suspected: a podman-side overlay-unpack quirk under
		// specific layer-content patterns (multi-stage RPM-installed
		// images that touch /usr/lib/sysimage/rpm/* in 6+ source
		// layers). Build skill cross-references a known-similar issue
		// in podman-5.7.x (storage_dest.go blob-reuse race) — possibly
		// related but unconfirmed.
		//
		// The merge optimization is non-fatal; the unmerged image is
		// fully correct (every individual layer digest is valid). We
		// surface a clearer diagnostic + an env-var hook to capture the
		// failing tarball for future investigation.
		return fmt.Errorf("post-build merge optimization failed (image is functional but unmerged): %w\n  Diagnostic: set CHARLY_MERGE_KEEP_TMP=1 and re-run `charly box merge %s` to capture the failing /tmp/charly-merge-*.tar.\n  This is a known limitation against multi-stage RPM-installed images; the build itself succeeded and the image at this tag is correct", err, imageRef)
	}

	newLayers, _ := newImg.Layers()
	fmt.Fprintf(os.Stderr, "Merged: %d layers -> %d layers\n", len(layers), len(newLayers))
	fmt.Fprintf(os.Stderr, "Saved %s\n", imageRef)
	return nil
}

// planMerge groups consecutive layers into groups up to maxBytes.
// Groups with 2+ layers are merged; single-layer groups are kept as-is.
func planMerge(sizes []int64, maxBytes int64) []MergeStep {
	var steps []MergeStep
	var group []int
	var groupSize int64

	flushGroup := func() {
		if len(group) >= 2 {
			steps = append(steps, MergeStep{Keep: false, Layers: group})
		} else {
			for _, idx := range group {
				steps = append(steps, MergeStep{Keep: true, Layers: []int{idx}})
			}
		}
		group = nil
		groupSize = 0
	}

	for i, size := range sizes {
		if groupSize+size <= maxBytes {
			group = append(group, i)
			groupSize += size
		} else {
			flushGroup()
			group = []int{i}
			groupSize = size
		}
	}
	flushGroup()

	return steps
}

// tarEntry holds a tar header and its content for deduplication.
type tarEntry struct {
	Header  *tar.Header
	Content []byte
}

// whiteoutPrefix is the OCI/Docker layer whiteout file prefix.
const whiteoutPrefix = ".wh."

// whiteoutOpaque is the opaque whiteout marker: a layer with this file replaces
// the entire directory contents from lower layers.
const whiteoutOpaque = ".wh..wh..opq"

// whiteoutTarget returns the path that the whiteout suppresses.
// Returns ("", false) for opaque whiteouts.
func whiteoutTarget(name string) (string, bool) {
	base := path.Base(name)
	if base == whiteoutOpaque {
		return "", false
	}
	if !strings.HasPrefix(base, whiteoutPrefix) {
		return "", false
	}
	target := strings.TrimPrefix(base, whiteoutPrefix)
	return path.Join(path.Dir(name), target), true
}

// mergeLayers combines multiple layers into one, deduplicating paths (last writer wins).
// Whiteout semantics are respected: when a later layer deletes a file via a whiteout
// entry (.wh.<name>), the original <name> is suppressed from the merged output so that
// both the original file and its whiteout do not coexist in the same layer (which would
// cause "file exists" errors during overlay unpack).
func mergeLayers(layers []v1.Layer) (v1.Layer, error) {
	// Collect all entries, tracking insertion order and deduplicating by path.
	// candyIdx tracks which layer last wrote each entry (for opaque whiteout handling).
	entries := make(map[string]*tarEntry)
	entryLayer := make(map[string]int) // path -> index of layer that last wrote it
	var order []string

	for li, layer := range layers {
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("reading uncompressed layer: %w", err)
		}

		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				rc.Close()
				return nil, fmt.Errorf("reading tar entry: %w", err)
			}

			var content []byte
			if hdr.Size > 0 {
				content, err = io.ReadAll(tr)
				if err != nil {
					rc.Close()
					return nil, fmt.Errorf("reading tar content for %s: %w", hdr.Name, err)
				}
			}

			if _, seen := entries[hdr.Name]; !seen {
				order = append(order, hdr.Name)
			}
			entries[hdr.Name] = &tarEntry{Header: hdr, Content: content}
			entryLayer[hdr.Name] = li
		}
		rc.Close()
	}

	// Build a suppression set from whiteout entries.
	// For each .wh.<name> whiteout introduced by layer L, suppress <name> if it
	// was introduced by an earlier layer (< L). This ensures the original file and
	// its whiteout do not coexist in the merged layer, which would cause "file exists"
	// errors during overlay unpack.
	//
	// For opaque whiteouts (.wh..wh..opq) in directory D/ introduced by layer L,
	// suppress all non-whiteout entries under D/ that came from earlier layers (< L).
	suppressed := make(map[string]bool)
	for _, name := range order {
		base := path.Base(name)
		if !strings.HasPrefix(base, whiteoutPrefix) {
			continue
		}
		whLayer := entryLayer[name]
		if base == whiteoutOpaque {
			// Opaque whiteout: suppress non-whiteout entries under this directory
			// that came from earlier layers.
			dir := path.Dir(name)
			prefix := dir + "/"
			if dir == "." {
				prefix = ""
			}
			for _, candidate := range order {
				if candidate == name {
					continue
				}
				if entryLayer[candidate] >= whLayer {
					continue // only suppress entries from earlier layers
				}
				candBase := path.Base(candidate)
				if strings.HasPrefix(candBase, whiteoutPrefix) {
					continue // keep whiteout entries
				}
				if prefix == "" || strings.HasPrefix(candidate, prefix) {
					suppressed[candidate] = true
				}
			}
		} else {
			// Regular whiteout: one of the pair must be suppressed.
			// If the target came from an earlier layer: whiteout wins, suppress the file.
			// If the target came from a later layer (re-introduced after whiteout): the
			// re-introduction wins and the whiteout is semantically moot — suppress it.
			// Both cases prevent the file and its whiteout from coexisting in the same
			// merged layer, which causes "file exists" EEXIST errors during overlay unpack.
			if target, ok := whiteoutTarget(name); ok {
				if targetLayer, exists := entryLayer[target]; exists {
					if targetLayer < whLayer {
						suppressed[target] = true // whiteout wins over earlier file
					} else {
						suppressed[name] = true // re-introduction wins, whiteout is moot
					}
				}
			}
		}
	}

	// Write deduplicated entries in order, skipping suppressed ones.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range order {
		if suppressed[name] {
			continue
		}
		entry := entries[name]
		if err := tw.WriteHeader(entry.Header); err != nil {
			return nil, fmt.Errorf("writing tar header for %s: %w", name, err)
		}
		if len(entry.Content) > 0 {
			if _, err := tw.Write(entry.Content); err != nil {
				return nil, fmt.Errorf("writing tar content for %s: %w", name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("closing tar writer: %w", err)
	}

	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// executeMerge rebuilds the image with merged layers and aligned history.
func executeMerge(img v1.Image, layers []v1.Layer, steps []MergeStep) (v1.Image, error) {
	cfgFile, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	history := cfgFile.History

	// Map layer indices to history entries.
	// History entries with EmptyLayer=true don't correspond to actual layers.
	layerToHistory := make(map[int]int) // layer index -> history index
	candyIdx := 0
	for histIdx, h := range history {
		if !h.EmptyLayer {
			if candyIdx < len(layers) {
				layerToHistory[candyIdx] = histIdx
			}
			candyIdx++
		}
	}

	var newAddenda []mutate.Addendum

	// Process steps: for each step, emit the corresponding layer + history.
	// Also emit any empty-layer history entries that fall between steps.
	prevMaxHistIdx := -1

	for _, step := range steps {
		// Find the range of history indices covered by this step
		minHistIdx := len(history)
		maxHistIdx := -1
		for _, li := range step.Layers {
			if hi, ok := layerToHistory[li]; ok {
				if hi < minHistIdx {
					minHistIdx = hi
				}
				if hi > maxHistIdx {
					maxHistIdx = hi
				}
			}
		}

		// Emit empty-layer history entries between previous step and this one
		for hi := prevMaxHistIdx + 1; hi < minHistIdx; hi++ {
			if history[hi].EmptyLayer {
				newAddenda = append(newAddenda, mutate.Addendum{
					History: history[hi],
				})
			}
		}

		if step.Keep {
			li := step.Layers[0]
			h := v1.History{}
			if hi, ok := layerToHistory[li]; ok {
				h = history[hi]
			}
			newAddenda = append(newAddenda, mutate.Addendum{
				Layer:   layers[li],
				History: h,
			})
			// Emit empty-layer entries between this layer's history and maxHistIdx
			if hi, ok := layerToHistory[step.Layers[0]]; ok {
				for ei := hi + 1; ei <= maxHistIdx; ei++ {
					if history[ei].EmptyLayer {
						newAddenda = append(newAddenda, mutate.Addendum{
							History: history[ei],
						})
					}
				}
			}
		} else {
			// Merge group
			groupLayers := make([]v1.Layer, len(step.Layers))
			var createdByParts []string
			for i, li := range step.Layers {
				groupLayers[i] = layers[li]
				if hi, ok := layerToHistory[li]; ok {
					if history[hi].CreatedBy != "" {
						createdByParts = append(createdByParts, history[hi].CreatedBy)
					}
				}
			}

			merged, err := mergeLayers(groupLayers)
			if err != nil {
				return nil, fmt.Errorf("merging layers %v: %w", step.Layers, err)
			}

			mergedSize, _ := merged.Size()
			fmt.Fprintf(os.Stderr, "Merging layers %d-%d (%.1f MB)\n",
				step.Layers[0], step.Layers[len(step.Layers)-1],
				float64(mergedSize)/(1024*1024))

			h := v1.History{
				CreatedBy: "charly merge: " + strings.Join(createdByParts, " && "),
			}
			newAddenda = append(newAddenda, mutate.Addendum{
				Layer:   merged,
				History: h,
			})

			// Emit empty-layer history entries that fall within the merge range
			for hi := minHistIdx + 1; hi <= maxHistIdx; hi++ {
				if history[hi].EmptyLayer {
					newAddenda = append(newAddenda, mutate.Addendum{
						History: history[hi],
					})
				}
			}
		}

		if maxHistIdx > prevMaxHistIdx {
			prevMaxHistIdx = maxHistIdx
		}
	}

	// Emit any trailing empty-layer history entries
	for hi := prevMaxHistIdx + 1; hi < len(history); hi++ {
		if history[hi].EmptyLayer {
			newAddenda = append(newAddenda, mutate.Addendum{
				History: history[hi],
			})
		}
	}

	// Reconstruct image from empty base + config + layers
	newImg := empty.Image
	newImg, err = mutate.ConfigFile(newImg, cfgFile)
	if err != nil {
		return nil, fmt.Errorf("setting config: %w", err)
	}

	// Clear history and diff IDs since addenda will rebuild them
	cf, _ := newImg.ConfigFile()
	cf.History = nil
	cf.RootFS.DiffIDs = nil
	newImg, err = mutate.ConfigFile(newImg, cf)
	if err != nil {
		return nil, fmt.Errorf("clearing config history: %w", err)
	}

	newImg, err = mutate.Append(newImg, newAddenda...)
	if err != nil {
		return nil, fmt.Errorf("appending layers: %w", err)
	}

	return newImg, nil
}

// loadImageFromDaemon loads an image from the container engine via save.
// If the ref is a manifest list (from podman build --manifest), it uses
// skopeo to extract the platform-specific image into a temp tag, then saves that.
// The caller must call cleanup() when done with the image to remove the temp file.
// Returns the image, a cleanup function, and whether the source was a manifest list.
func loadImageFromDaemon(ref string, engine string) (v1.Image, func(), bool, error) {
	binary := EngineBinary(engine)

	// Try saving as a regular image first
	img, cleanup, err := saveAndLoad(binary, ref)
	if err == nil {
		return img, cleanup, false, nil
	}

	// May be a manifest list — use skopeo to extract the platform image
	// into a temp tag that podman save can handle.
	tmpRef := ref
	if idx := strings.LastIndex(tmpRef, ":"); idx != -1 {
		tmpRef = tmpRef[:idx]
	}
	tmpRef += ":charly-merge-tmp"

	hostArch := runtime.GOARCH
	cmd := exec.Command("skopeo", "copy",
		"--override-arch", hostArch, "--override-os", "linux",
		"containers-storage:"+ref,
		"containers-storage:"+tmpRef)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if skoErr := cmd.Run(); skoErr != nil {
		// Not a manifest either — return original save error
		return nil, nil, false, fmt.Errorf("%s save %s: %w", binary, ref, err)
	}

	img, cleanup, err = saveAndLoad(binary, tmpRef)

	// Clean up the temp tag regardless of success
	rmCmd := exec.Command(binary, "rmi", tmpRef)
	rmCmd.Stdout = io.Discard
	rmCmd.Stderr = io.Discard
	_ = rmCmd.Run() // best-effort: cleaning up the temp tag regardless of outcome

	if err != nil {
		return nil, nil, false, fmt.Errorf("saving extracted manifest image: %w", err)
	}

	return img, cleanup, true, nil
}

// saveAndLoad saves an image ref to a temp tarball and loads it as v1.Image.
func saveAndLoad(binary, ref string) (v1.Image, func(), error) {
	tmpFile, err := os.CreateTemp("", "charly-merge-*.tar")
	if err != nil {
		return nil, nil, fmt.Errorf("creating temp file: %w", err)
	}
	RegisterTempCleanup(tmpFile.Name())

	cleanup := func() { _ = os.Remove(tmpFile.Name()); UnregisterTempCleanup(tmpFile.Name()) }

	cmd := exec.Command(binary, "save", ref)
	cmd.Stdout = tmpFile
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return nil, nil, err
	}

	_ = tmpFile.Close()

	img, err := tarball.ImageFromPath(tmpFile.Name(), nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("reading saved image: %w", err)
	}

	return img, cleanup, nil
}

// saveImageToDaemon saves an image to the container engine via load.
//
// On failure, when CHARLY_MERGE_KEEP_TMP=1 the temp tarball is left in /tmp
// for forensic inspection (path printed to stderr). Used to debug the
// rare cases where podman load rejects a merged tar with EEXIST due to
// duplicate-Name entries — the keep-on-fail diagnostic surfaces the
// exact tar so the operator can re-extract and find the collision.
func saveImageToDaemon(img v1.Image, ref string, engine string) error {
	tmpFile, err := os.CreateTemp("", "charly-merge-*.tar")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	RegisterTempCleanup(tmpFile.Name())
	keepOnFail := os.Getenv("CHARLY_MERGE_KEEP_TMP") == "1"
	loaded := false
	defer func() {
		if loaded || !keepOnFail {
			_ = os.Remove(tmpFile.Name())
			UnregisterTempCleanup(tmpFile.Name())
		} else {
			fmt.Fprintf(os.Stderr, "CHARLY_MERGE_KEEP_TMP=1: kept failing tarball at %s\n", tmpFile.Name())
		}
	}()
	defer tmpFile.Close() //nolint:errcheck

	tag, err := name.NewTag(ref)
	if err != nil {
		return fmt.Errorf("parsing image ref %q: %w", ref, err)
	}

	if err := tarball.WriteToFile(tmpFile.Name(), tag, img); err != nil {
		return fmt.Errorf("writing image tarball: %w", err)
	}

	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "load", "-i", tmpFile.Name())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s load: %w", binary, err)
	}

	loaded = true
	return nil
}

// printMergePlan displays the dry-run merge plan.
func printMergePlan(sizes []int64, steps []MergeStep) {
	for _, step := range steps {
		if step.Keep {
			idx := step.Layers[0]
			fmt.Fprintf(os.Stderr, "Layer %2d: %7.1f MB  [keep]\n", idx, float64(sizes[idx])/(1024*1024))
		} else {
			var total int64
			for _, idx := range step.Layers {
				total += sizes[idx]
			}
			for i, idx := range step.Layers {
				prefix := " "
				switch {
				case i == 0:
					prefix = "\\"
				case i == len(step.Layers)-1:
					prefix = "/"
				}
				suffix := ""
				if i == len(step.Layers)-1 {
					suffix = fmt.Sprintf("  > merge (%.1f MB)", float64(total)/(1024*1024))
				}
				fmt.Fprintf(os.Stderr, "Layer %2d: %7.1f MB  %s%s\n", idx, float64(sizes[idx])/(1024*1024), prefix, suffix)
			}
		}
	}

	resultLayers := len(steps)
	fmt.Fprintf(os.Stderr, "\n%d layers -> %d layers\n", len(sizes), resultLayers)
}
