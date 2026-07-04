package spice

import (
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/opencharly/sdk/spec"
)

// methods.go is the spice method dispatcher: the 7-method surface moved from
// charly/spice.go, refactored from CLI Run() methods that PRINTED to stdout into
// functions that RETURN the captured output string (status) or WRITE a PNG artifact
// + return a one-line confirmation (screenshot/cursor) — so provider.go can feed the
// output through the shared sdk matcher pipeline + sdk.RunArtifactValidators (the
// host-side matcher step does not run for an out-of-process verb). The
// SPICE wire behaviour, the PC-AT scancode tables, and the status tokens are
// unchanged, so a bed authored against the in-tree verb passes unchanged.

// requiredModifiers mirrors the in-tree spiceMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc
// live-verb seam, which an external verb is not — so the check moves HERE, at
// dispatch). click/mouse need x+y, type needs text, key needs the key name,
// screenshot/cursor need the artifact output path.
var requiredModifiers = map[string][]string{
	"screenshot": {"artifact"},
	"cursor":     {"artifact"},
	"click":      {"x", "y"},
	"mouse":      {"x", "y"},
	"type":       {"text"},
	"key":        {"key"},
}

func modifierZero(op *spec.Op, name string) bool {
	switch name {
	case "artifact":
		return op.Artifact == ""
	case "x":
		return op.X == 0
	case "y":
		return op.Y == 0
	case "text":
		return op.Text == ""
	case "key":
		return op.KeyName == ""
	}
	return false
}

func checkRequiredModifiers(method string, op *spec.Op) error {
	var missing []string
	for _, f := range requiredModifiers[method] {
		if modifierZero(op, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// dispatch runs one spice method against the pre-dialed session and returns its
// captured output. A returned error is the verb FAILING (the in-tree CLI Run()
// returning an error → exit 1); provider.go maps it through the exit_status / stderr
// matchers.
func dispatch(s *SpiceSession, op *spec.Op) (string, error) {
	method := string(op.Spice)
	if err := checkRequiredModifiers(method, op); err != nil {
		return "", err
	}
	switch method {
	case "status":
		return runStatus(s)
	case "screenshot":
		return runScreenshot(s, op.Artifact)
	case "cursor":
		return runCursor(s, op.Artifact)
	case "click":
		return runClick(s, op)
	case "mouse":
		return runMouse(s, op)
	case "type":
		return runType(s, op.Text)
	case "key":
		return runKey(s, op.KeyName)
	}
	return "", fmt.Errorf("unknown spice method %q", method)
}

// runStatus completes the handshake, enumerates channels, and returns a multi-line
// status report. The first line is "SPICE:     ok" so a `stdout: contains: ok`
// matcher passes — mirroring the cdp/wl/vnc status convention.
func runStatus(s *SpiceSession) (string, error) {
	_ = s.WaitForInputs(2 * time.Second)
	var b strings.Builder
	fmt.Fprintln(&b, "SPICE:     ok")
	fmt.Fprintf(&b, "connected: %s\n", s.addr)
	if s.Display() != nil {
		bb := s.Display().Bounds()
		fmt.Fprintf(&b, "display:   %dx%d\n", bb.Dx(), bb.Dy())
	} else {
		fmt.Fprintln(&b, "display:   not yet received")
	}
	if s.Inputs() != nil {
		fmt.Fprintln(&b, "inputs:    ready")
	} else {
		fmt.Fprintln(&b, "inputs:    not yet ready")
	}
	if img, x, y := s.Cursor(); img != nil {
		fmt.Fprintf(&b, "cursor:    visible at (%d,%d)\n", x, y)
	} else {
		fmt.Fprintln(&b, "cursor:    not yet received")
	}
	return b.String(), nil
}

// runScreenshot waits for the first display frame, encodes it as PNG, and writes it
// to the host artifact path. Returns a one-line confirmation.
func runScreenshot(s *SpiceSession, artifact string) (string, error) {
	if err := s.WaitForDisplay(5 * time.Second); err != nil {
		return "", err
	}
	img := s.Display()
	if img == nil {
		return "", fmt.Errorf("no display frame available")
	}
	f, err := os.Create(artifact)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", artifact, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("encoding PNG: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	b := img.Bounds()
	return fmt.Sprintf("Screenshot saved to %s (%dx%d, native SPICE display decode)", artifact, b.Dx(), b.Dy()), nil
}

// runCursor polls for cursor data, encodes the bitmap as PNG, and writes it to the
// host artifact path. Returns a one-line confirmation.
func runCursor(s *SpiceSession, artifact string) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if img, x, y := s.Cursor(); img != nil {
			f, err := os.Create(artifact)
			if err != nil {
				return "", fmt.Errorf("creating %s: %w", artifact, err)
			}
			if err := png.Encode(f, img); err != nil {
				_ = f.Close()
				return "", err
			}
			if err := f.Close(); err != nil {
				return "", err
			}
			b := img.Bounds()
			return fmt.Sprintf("Cursor saved to %s (%dx%d at position %d,%d)", artifact, b.Dx(), b.Dy(), x, y), nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("no cursor data within %s", 5*time.Second)
}

// runClick presses + releases a mouse button at (x,y).
func runClick(s *SpiceSession, op *spec.Op) (string, error) {
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return "", err
	}
	btn, err := spiceButtonCode(op.Button)
	if err != nil {
		return "", err
	}
	in := s.Inputs()
	in.MousePosition(uint32(op.X), uint32(op.Y))
	in.MouseDown(btn, uint32(op.X), uint32(op.Y))
	time.Sleep(50 * time.Millisecond)
	in.MouseUp(btn, uint32(op.X), uint32(op.Y))
	return fmt.Sprintf("clicked %s at (%d,%d)", buttonName(op.Button), op.X, op.Y), nil
}

// runMouse moves the pointer to (x,y) without clicking.
func runMouse(s *SpiceSession, op *spec.Op) (string, error) {
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return "", err
	}
	s.Inputs().MousePosition(uint32(op.X), uint32(op.Y))
	return fmt.Sprintf("moved pointer to (%d,%d)", op.X, op.Y), nil
}

// runType types text as a sequence of PC-AT scancodes.
func runType(s *SpiceSession, text string) (string, error) {
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return "", err
	}
	in := s.Inputs()
	for _, r := range text {
		scancode, shift := runeToScancode(r)
		if scancode == 0 {
			continue
		}
		if shift {
			in.OnKeyDown(encodeScancode(42)) // LeftShift
		}
		in.OnKeyDown(encodeScancode(scancode))
		time.Sleep(10 * time.Millisecond)
		in.OnKeyUp(encodeScancode(scancode))
		if shift {
			in.OnKeyUp(encodeScancode(42))
		}
	}
	return fmt.Sprintf("typed %d characters", len([]rune(text))), nil
}

// runKey presses + releases one named key.
func runKey(s *SpiceSession, key string) (string, error) {
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return "", err
	}
	scancode, ok := spiceKeyNameToScancode[strings.ToLower(key)]
	if !ok {
		return "", fmt.Errorf("unknown key: %s", key)
	}
	in := s.Inputs()
	in.OnKeyDown(encodeScancode(scancode))
	time.Sleep(50 * time.Millisecond)
	in.OnKeyUp(encodeScancode(scancode))
	return fmt.Sprintf("pressed key %s", key), nil
}

// ---------------- scancode helpers (ported verbatim from charly/spice.go) ----------------

func buttonName(name string) string {
	if name == "" {
		return "left"
	}
	return strings.ToLower(name)
}

func spiceButtonCode(name string) (uint8, error) {
	switch strings.ToLower(name) {
	case "", "left":
		return 1, nil
	case "right":
		return 2, nil
	case "middle":
		return 3, nil
	}
	return 0, fmt.Errorf("invalid button: %s (left/right/middle)", name)
}

// encodeScancode converts a PC AT scancode (1-byte for most keys) to the byte slice
// form Shells-com/spice's OnKeyDown/OnKeyUp expect.
func encodeScancode(code uint8) []byte {
	return []byte{code}
}

func runeToScancode(r rune) (uint8, bool) {
	if r >= 'a' && r <= 'z' {
		return letterScancode[r-'a'], false
	}
	if r >= 'A' && r <= 'Z' {
		return letterScancode[r-'A'], true
	}
	if r >= '0' && r <= '9' {
		return digitScancode[r-'0'], false
	}
	switch r {
	case ' ':
		return 57, false
	case '.':
		return 52, false
	case ',':
		return 51, false
	case '/':
		return 53, false
	case '-':
		return 12, false
	case '=':
		return 13, false
	case ';':
		return 39, false
	case '\'':
		return 40, false
	case '\n':
		return 28, false
	}
	return 0, false
}

// PC AT scancodes (set 1). Indexed by 0-based letter/digit offset.
var letterScancode = [26]uint8{
	30, 48, 46, 32, 18, 33, 34, 35, 23, 36, 37, 38, 50,
	49, 24, 25, 16, 19, 31, 20, 22, 47, 17, 45, 21, 44,
}

var digitScancode = [10]uint8{
	11, 2, 3, 4, 5, 6, 7, 8, 9, 10,
}

// spiceKeyNameToScancode maps friendly key names to scancodes for the key verb.
var spiceKeyNameToScancode = map[string]uint8{
	"return": 28, "enter": 28, "tab": 15,
	"escape": 1, "esc": 1,
	"backspace": 14, "space": 57, "capslock": 58,
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64,
	"f7": 65, "f8": 66, "f9": 67, "f10": 68, "f11": 87, "f12": 88,
	"up": 72, "down": 80, "left": 75, "right": 77,
	"home": 71, "end": 79, "pgup": 73, "pgdn": 81,
	"insert": 82, "delete": 83,
	"shift": 42, "ctrl": 29, "alt": 56, "meta": 125,
}
