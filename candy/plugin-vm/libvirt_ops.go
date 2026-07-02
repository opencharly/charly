package main

// go-libvirt wrappers used by LibvirtCmd: screenshot drain + PPM→PNG,
// keyname → Linux keycode mapping for DomainSendKey.

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
)

// captureDomainScreenshot streams the VM framebuffer via libvirt's
// DomainScreenshot, decodes the returned PPM (QEMU's default format),
// and returns an in-memory image.
//
// QEMU's VNC screenshot returns PPM P6 (raw RGB) by default. We
// parse the header, then decode each pixel from three bytes.
//
// Some libvirt + virtio-gpu combinations return PNG via the RPC stream
// rather than PPM (the format the underlying QEMU monitor produces is
// driver-dependent). go-libvirt v0.0.0-20260217 has a known issue
// where the RPC stream-finish framing fails on these payloads with
// "xdr:DecodeUint: EOF while decoding 4 bytes" — the screenshot data
// arrives but the stream's terminator XDR doesn't decode. As a
// pragmatic fallback we shell out to `virsh -c qemu:///session
// screenshot <name>` (uses the C client's stream impl, which is
// stable) and decode whatever PNG/PPM it writes via image.Decode.
func captureDomainScreenshot(l *libvirt.Libvirt, dom libvirt.Domain, screen uint) (image.Image, error) {
	var buf bytes.Buffer
	mime, err := l.DomainScreenshot(dom, &buf, uint32(screen), 0)
	if err != nil {
		// Fallback path: virsh screenshot writes PNG/PPM by inspecting
		// the QEMU return MIME — works on virtio-gpu where go-libvirt's
		// stream framing breaks. Domain name comes from the handle's
		// Name field directly (no extra RPC needed).
		if dom.Name == "" {
			return nil, fmt.Errorf("DomainScreenshot: %w (and Domain.Name is empty)", err)
		}
		img, ferr := captureDomainScreenshotViaVirsh(dom.Name)
		if ferr != nil {
			return nil, fmt.Errorf("DomainScreenshot: %w (virsh fallback: %w)", err, ferr)
		}
		return img, nil
	}
	_ = mime // "image/x-portable-pixmap" normally; we parse PPM regardless

	r := bufio.NewReader(&buf)

	// PPM header: P6\n<w> <h>\n<max>\n<bytes>
	magic, err := readPPMToken(r)
	if err != nil {
		return nil, fmt.Errorf("read PPM magic: %w", err)
	}
	if magic != "P6" {
		peek := buf.Bytes()
		if len(peek) > 64 {
			peek = peek[:64]
		}
		return nil, fmt.Errorf("expected PPM P6, got %q (first 64 bytes: %x)",
			magic, peek)
	}
	wStr, err := readPPMToken(r)
	if err != nil {
		return nil, err
	}
	hStr, err := readPPMToken(r)
	if err != nil {
		return nil, err
	}
	maxStr, err := readPPMToken(r)
	if err != nil {
		return nil, err
	}
	w, _ := strconv.Atoi(wStr)
	h, _ := strconv.Atoi(hStr)
	maxval, _ := strconv.Atoi(maxStr)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", w, h)
	}
	if maxval != 255 {
		return nil, fmt.Errorf("unsupported PPM maxval %d (only 8-bit supported)", maxval)
	}
	// The single byte after the max value is the header terminator
	// (newline). Consume and proceed to raw pixel data.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			rByte, err := r.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("read pixel at (%d,%d): %w", x, y, err)
			}
			gByte, _ := r.ReadByte()
			bByte, _ := r.ReadByte()
			img.Set(x, y, color.RGBA{R: rByte, G: gByte, B: bByte, A: 255})
		}
	}
	return img, nil
}

// readPPMToken reads a whitespace-separated token from a PPM stream,
// skipping comments (# …) and leading whitespace.
func readPPMToken(r *bufio.Reader) (string, error) {
	var out []byte
	skipWhitespace := true
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if skipWhitespace && (b == ' ' || b == '\t' || b == '\n' || b == '\r') {
			continue
		}
		if b == '#' {
			// skip to end of line
			for {
				c, err := r.ReadByte()
				if err != nil {
					return "", err
				}
				if c == '\n' {
					break
				}
			}
			skipWhitespace = true
			continue
		}
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			if len(out) > 0 {
				return string(out), nil
			}
			continue
		}
		out = append(out, b)
		skipWhitespace = false
	}
}

// captureDomainScreenshotViaVirsh shells out to `virsh -c qemu:///session
// screenshot <name> <tmpfile>`, decodes whatever PNG/PPM the C client
// writes, and returns the resulting image.Image. Fallback for the
// go-libvirt DomainScreenshot RPC-stream issue described above.
func captureDomainScreenshotViaVirsh(domName string) (image.Image, error) {
	tmp, err := os.CreateTemp("", "charly-libvirt-screenshot-*.png")
	if err != nil {
		return nil, fmt.Errorf("creating tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	RegisterTempCleanup(tmpPath)
	defer func() { _ = os.Remove(tmpPath); UnregisterTempCleanup(tmpPath) }()
	defer os.Remove(filepath.Join(filepath.Dir(tmpPath), "charly-libvirt-screenshot-temp.ppm")) //nolint:errcheck

	cmd := exec.Command("virsh", "-c", "qemu:///session", "screenshot", domName, tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("virsh screenshot %s: %w (output: %s)", domName, err, strings.TrimSpace(string(out)))
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("opening virsh screenshot output: %w", err)
	}
	defer f.Close() //nolint:errcheck
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding virsh screenshot output: %w", err)
	}
	return img, nil
}

// ---------------- keymap ----------------

// mapLibvirtKeys converts a list of friendly key names to Linux
// keycodes (keycode set 1). Supports single keys and chord notation
// ("ctrl+alt+F2"). Names are case-insensitive.
//
// The subset covered here is what the `charly check libvirt send-key` CLI
// needs — enough for common test cases (login sequences, TTY
// switches, ctrl-c). The full Linux keycode set is in
// <linux/input-event-codes.h>; extending here is a small addition.
func mapLibvirtKeys(keys []string) ([]uint32, error) {
	var codes []uint32
	for _, k := range keys {
		parts := strings.SplitSeq(k, "+")
		for p := range parts {
			code, ok := libvirtKeyMap[strings.ToLower(strings.TrimSpace(p))]
			if !ok {
				return nil, fmt.Errorf("unknown key: %q", p)
			}
			codes = append(codes, code)
		}
	}
	return codes, nil
}

// libvirtKeyMap is the Linux keycode subset we expose. From
// /usr/include/linux/input-event-codes.h KEY_* definitions.
var libvirtKeyMap = map[string]uint32{
	// Letters (lowercase)
	"a": 30, "b": 48, "c": 46, "d": 32, "e": 18, "f": 33, "g": 34, "h": 35,
	"i": 23, "j": 36, "k": 37, "l": 38, "m": 50, "n": 49, "o": 24, "p": 25,
	"q": 16, "r": 19, "s": 31, "t": 20, "u": 22, "v": 47, "w": 17, "x": 45,
	"y": 21, "z": 44,

	// Digits
	"0": 11, "1": 2, "2": 3, "3": 4, "4": 5, "5": 6, "6": 7, "7": 8, "8": 9, "9": 10,

	// Function keys
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64,
	"f7": 65, "f8": 66, "f9": 67, "f10": 68, "f11": 87, "f12": 88,

	// Navigation
	"up": 103, "down": 108, "left": 105, "right": 106,
	"home": 102, "end": 107, "pgup": 104, "pgdn": 109,
	"insert": 110, "delete": 111,

	// Modifiers
	"shift": 42, "leftshift": 42, "rightshift": 54,
	"ctrl": 29, "leftctrl": 29, "rightctrl": 97,
	"alt": 56, "leftalt": 56, "rightalt": 100,
	"meta": 125, "super": 125, "leftmeta": 125, "rightmeta": 126,

	// Whitespace & common
	"space": 57, "enter": 28, "return": 28, "tab": 15,
	"backspace": 14, "escape": 1, "esc": 1,
	"capslock": 58, "numlock": 69, "scrolllock": 70,

	// Punctuation
	"minus": 12, "equal": 13, "leftbrace": 26, "rightbrace": 27,
	"semicolon": 39, "apostrophe": 40, "grave": 41,
	"backslash": 43, "comma": 51, "dot": 52, "slash": 53,
}
