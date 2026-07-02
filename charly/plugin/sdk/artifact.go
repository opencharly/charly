package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig / image.Decode
	_ "image/png"  // register PNG decoder for image.DecodeConfig / image.Decode
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// ---------------------------------------------------------------------------
// Artifact validators
//
// The post-run artifact-reality assertions (min_bytes / min_dimensions /
// not_uniform / min_cast_events) are the SINGLE implementation (R3) every
// out-of-tree verb plugin that produces an artifact (appium screenshot, adb
// screencap, cdp/wl/vnc/record captures) calls — the same property motivating
// MatchAll's home here. Every live-container verb is now served out-of-process,
// so this SDK copy is the sole implementation (the former host duplicate in
// charly's core check runner was deleted with the in-proc live-verb runtime).
// ---------------------------------------------------------------------------

// RunArtifactValidators runs every artifact assertion the Op declares against
// the file at op.Artifact: min_bytes, min_dimensions (WxH), not_uniform, and
// min_cast_events. Returns nil when every declared validator passes, or the
// first validator's error. A plugin that produces an artifact calls this after
// writing the file as the post-run validation pipeline.
func RunArtifactValidators(op *spec.Op) error {
	if op.ArtifactMinBytes > 0 {
		info, err := os.Stat(op.Artifact)
		if err != nil {
			return fmt.Errorf("artifact %q not found: %w", op.Artifact, err)
		}
		if info.Size() < int64(op.ArtifactMinBytes) {
			return fmt.Errorf("artifact %q size %d < required min_bytes %d", op.Artifact, info.Size(), op.ArtifactMinBytes)
		}
	}
	if op.ArtifactMinDimensions != "" {
		if err := assertArtifactMinDimensions(op.Artifact, op.ArtifactMinDimensions); err != nil {
			return err
		}
	}
	if op.ArtifactNotUniform {
		if err := assertArtifactNotUniform(op.Artifact); err != nil {
			return err
		}
	}
	if op.ArtifactMinCastEvents > 0 {
		if err := assertArtifactMinCastEvents(op.Artifact, op.ArtifactMinCastEvents); err != nil {
			return err
		}
	}
	return nil
}

// assertArtifactMinDimensions decodes the artifact's image header (PNG/JPEG) and
// fails if width or height is below the "WxH" requirement. Cheap — reads only
// the header via image.DecodeConfig, not the full pixel data.
func assertArtifactMinDimensions(path, wxh string) error {
	parts := strings.SplitN(wxh, "x", 2)
	if len(parts) != 2 {
		return fmt.Errorf("artifact_min_dimensions: bad format %q (want WxH)", wxh)
	}
	wantW, err := strconv.Atoi(parts[0])
	if err != nil || wantW <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad width %q", parts[0])
	}
	wantH, err := strconv.Atoi(parts[1])
	if err != nil || wantH <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad height %q", parts[1])
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode-config: %w", path, err)
	}
	if cfg.Width < wantW || cfg.Height < wantH {
		return fmt.Errorf("artifact %q dimensions %dx%d < required min %dx%d", path, cfg.Width, cfg.Height, wantW, wantH)
	}
	return nil
}

// assertArtifactNotUniform decodes the full image and samples pixels at 100
// deterministic positions; fails if every sampled pixel shares the same RGBA.
// Catches all-black / all-white / blank-canvas captures that a byte-size check
// alone would pass.
func assertArtifactNotUniform(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode: %w", path, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("artifact %q has zero-size bounds %dx%d", path, w, h)
	}
	stepX := max(w/10, 1)
	stepY := max(h/10, 1)
	var firstR, firstG, firstB, firstA uint32
	first := true
	for py := bounds.Min.Y; py < bounds.Max.Y; py += stepY {
		for px := bounds.Min.X; px < bounds.Max.X; px += stepX {
			r, g, b, a := img.At(px, py).RGBA()
			if first {
				firstR, firstG, firstB, firstA = r, g, b, a
				first = false
				continue
			}
			if r != firstR || g != firstG || b != firstB || a != firstA {
				return nil // found a varying pixel — not uniform
			}
		}
	}
	return fmt.Errorf("artifact %q is uniformly one color (RGBA=%d,%d,%d,%d) — likely a blank/black/white capture",
		path, firstR>>8, firstG>>8, firstB>>8, firstA>>8)
}

// assertArtifactMinCastEvents validates an asciinema .cast file has at least
// minEvents event lines after a valid v2 header line.
func assertArtifactMinCastEvents(path string, minEvents int) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !scan.Scan() {
		return fmt.Errorf("artifact %q is empty (expected asciinema cast header on line 1)", path)
	}
	var header map[string]any
	if err := json.Unmarshal(scan.Bytes(), &header); err != nil {
		return fmt.Errorf("artifact %q line 1: not a JSON object (asciinema header expected): %w", path, err)
	}
	if _, ok := header["version"]; !ok {
		return fmt.Errorf("artifact %q line 1: JSON object missing %q field (not an asciinema cast header)", path, "version")
	}
	events := 0
	for scan.Scan() {
		if len(strings.TrimSpace(scan.Text())) == 0 {
			continue
		}
		events++
		if events >= minEvents {
			return nil
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("artifact %q scan: %w", path, err)
	}
	return fmt.Errorf("artifact %q has %d events, want >= %d", path, events, minEvents)
}
