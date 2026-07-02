package sdk

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The artifact validators are the SINGLE implementation (R3) shared by every
// out-of-tree verb plugin (appium screenshot, adb screencap, …) via
// RunArtifactValidators. These tests, ported from the former host-side copy
// (deleted with the dead in-proc live-verb runtime), keep the surviving SDK
// implementation covered.

// TestArtifactMinDimensions_PassFail synthesizes a 1024x768 PNG and asserts
// the dimension validator passes when the requirement is below or equal,
// fails when it's above, and surfaces input-format errors clearly.
func TestArtifactMinDimensions_PassFail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.png")
	writePNG(t, path, 1024, 768, color.RGBA{R: 100, G: 150, B: 200, A: 255})

	tests := []struct {
		name      string
		spec      string
		path      string
		wantError bool
		errSubstr string
	}{
		{"pass-equal", "1024x768", path, false, ""},
		{"pass-below", "800x600", path, false, ""},
		{"fail-above", "1500x1500", path, true, "1024x768 < required min 1500x1500"},
		{"bad-format-no-x", "100", path, true, "bad format"},
		{"bad-format-zero", "0x100", path, true, "bad width"},
		{"missing-file", "100x100", filepath.Join(dir, "nope.png"), true, "open"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := assertArtifactMinDimensions(tc.path, tc.spec)
			if tc.wantError {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("want error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestArtifactNotUniform_DetectsBlackAndWhite builds three PNGs:
//   - all-black 800x600       → uniform → must fail
//   - all-white 800x600       → uniform → must fail
//   - black with one red pixel → non-uniform → must pass
//
// Defends the failure mode that artifact_min_bytes silently passes:
// a 100KB all-black PNG has the same byte profile as a real screenshot
// of similar dimensions; only the pixel-sampling check distinguishes them.
func TestArtifactNotUniform_DetectsBlackAndWhite(t *testing.T) {
	dir := t.TempDir()

	black := filepath.Join(dir, "black.png")
	writePNG(t, black, 800, 600, color.RGBA{0, 0, 0, 255})
	if err := assertArtifactNotUniform(black); err == nil {
		t.Fatalf("all-black PNG: want uniform-color error, got nil")
	} else if !strings.Contains(err.Error(), "uniformly one color") {
		t.Fatalf("all-black PNG: want %q, got %q", "uniformly one color", err.Error())
	}

	white := filepath.Join(dir, "white.png")
	writePNG(t, white, 800, 600, color.RGBA{255, 255, 255, 255})
	if err := assertArtifactNotUniform(white); err == nil {
		t.Fatalf("all-white PNG: want uniform-color error, got nil")
	}

	mixed := filepath.Join(dir, "mixed.png")
	writeMixedPNG(t, mixed, 800, 600, color.RGBA{0, 0, 0, 255}, color.RGBA{255, 0, 0, 255}, 400, 300)
	if err := assertArtifactNotUniform(mixed); err != nil {
		t.Fatalf("mixed PNG: want no error, got %v", err)
	}

	if err := assertArtifactNotUniform(filepath.Join(dir, "nope.png")); err == nil {
		t.Fatalf("missing file: want open error, got nil")
	}
}

// TestArtifactMinCastEvents_ParsesCast verifies the asciinema .cast event
// counter accepts well-formed casts at or above the threshold, rejects
// counts below the threshold, and surfaces clear errors on malformed
// headers or empty files.
func TestArtifactMinCastEvents_ParsesCast(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.cast")
	writeCast(t, good, 5)

	if err := assertArtifactMinCastEvents(good, 5); err != nil {
		t.Fatalf("5-event cast at threshold 5: want no error, got %v", err)
	}
	if err := assertArtifactMinCastEvents(good, 1); err != nil {
		t.Fatalf("5-event cast at threshold 1: want no error, got %v", err)
	}
	if err := assertArtifactMinCastEvents(good, 10); err == nil {
		t.Fatalf("5-event cast at threshold 10: want shortfall error, got nil")
	}

	badHeader := filepath.Join(dir, "badheader.cast")
	if err := os.WriteFile(badHeader, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := assertArtifactMinCastEvents(badHeader, 1); err == nil {
		t.Fatalf("malformed header: want error, got nil")
	}

	noVersion := filepath.Join(dir, "noversion.cast")
	if err := os.WriteFile(noVersion, []byte(`{"width":80}`+"\n"+`[0,"o","x"]`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := assertArtifactMinCastEvents(noVersion, 1); err == nil {
		t.Fatalf("header missing version: want error, got nil")
	}

	empty := filepath.Join(dir, "empty.cast")
	if err := os.WriteFile(empty, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := assertArtifactMinCastEvents(empty, 1); err == nil {
		t.Fatalf("empty file: want error, got nil")
	}

	if err := assertArtifactMinCastEvents(filepath.Join(dir, "nope.cast"), 1); err == nil {
		t.Fatalf("missing file: want open error, got nil")
	}
}

// writePNG writes a uniformly-colored PNG of the given size at path.
func writePNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMixedPNG writes a PNG whose background is bg and which has one pixel
// of fg at (markX, markY). Forces non-uniform pixel sampling.
func writeMixedPNG(t *testing.T, path string, w, h int, bg, fg color.RGBA, markX, markY int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, bg)
		}
	}
	img.Set(markX, markY, fg)
	// Also vary one of the sampled grid cells (the sampler hits stepX*N, stepY*N)
	// so the sampler's deterministic stride sees the variation.
	img.Set(0, 0, fg)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCast writes a minimal asciinema cast file with the given number of
// event lines. Header is the asciinema v2 shape; events are simple "o"
// (output) lines with monotonically increasing timestamps.
func writeCast(t *testing.T, path string, numEvents int) {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString(`{"version":2,"width":80,"height":24}` + "\n")
	for i := range numEvents {
		buf.WriteString(`[`)
		buf.WriteString(`0.`)
		buf.WriteString(itoa(i))
		buf.WriteString(`,"o","x"]`)
		buf.WriteString("\n")
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// itoa converts a small non-negative int to its decimal string. Avoids the
// strconv import in this test fixture function.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
