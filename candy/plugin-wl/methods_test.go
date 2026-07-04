package wl

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// methods_test.go covers the PLUGIN-side pure helpers ported out-of-process from
// charly/wl.go + charly/wl_overlay.go (the deleted host-side WlCmd/WlOverlayCmd): the
// required-modifier check + key/button/combo/scroll mappings + the sway-tree window-rect
// search. The venue-driving methods need a live executor reverse channel and are exercised
// by the R10 bed (the sway-browser-vnc `wl: sway-tree` + `wl: screenshot`), not these tests.

// TestCheckRequiredModifiers mirrors the in-tree wlMethods Required specs that moved here.
func TestCheckRequiredModifiers(t *testing.T) {
	cases := []struct {
		method  string
		op      spec.Op
		wantErr string // substring; "" means no error
	}{
		{"status", spec.Op{Wl: "status"}, ""},
		{"toplevel", spec.Op{Wl: "toplevel"}, ""},
		{"geometry", spec.Op{Wl: "geometry"}, "target"},
		{"geometry", spec.Op{Wl: "geometry", Target: "chrome"}, ""},
		{"screenshot", spec.Op{Wl: "screenshot"}, "artifact"},
		{"screenshot", spec.Op{Wl: "screenshot", Artifact: "/tmp/x.png"}, ""},
		{"click", spec.Op{Wl: "click"}, "x"},
		{"click", spec.Op{Wl: "click", X: 10, Y: 20}, ""},
		{"scroll", spec.Op{Wl: "scroll", X: 1, Y: 1}, "direction"},
		{"scroll", spec.Op{Wl: "scroll", X: 1, Y: 1, Direction: "down"}, ""},
		{"type", spec.Op{Wl: "type"}, "text"},
		{"key", spec.Op{Wl: "key"}, "key"},
		{"key-combo", spec.Op{Wl: "key-combo"}, "combo"},
		{"sway-tree", spec.Op{Wl: "sway-tree"}, ""},
		{"overlay-show", spec.Op{Wl: "overlay-show"}, "text"},
		{"overlay-show", spec.Op{Wl: "overlay-show", Text: "hello"}, ""},
	}
	for _, tc := range cases {
		err := sdk.RequireModifiers(tc.method, &tc.op, requiredModifiers)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.method, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.method, tc.wantErr, err)
		}
	}
}

// TestParseKeyCombo covers the wtype -M modifier mapping + the final key split.
func TestParseKeyCombo(t *testing.T) {
	mods, key, err := parseKeyCombo("ctrl+shift+t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "t" {
		t.Errorf("key = %q, want t", key)
	}
	if strings.Join(mods, ",") != "ctrl,shift" {
		t.Errorf("mods = %v, want [ctrl shift]", mods)
	}
	if _, _, err := parseKeyCombo("bogus+c"); err == nil {
		t.Errorf("expected error for unknown modifier")
	}
	mods, key, _ = parseKeyCombo("super+l")
	if key != "l" || strings.Join(mods, ",") != "logo" {
		t.Errorf("super+l → mods=%v key=%q, want [logo] l", mods, key)
	}
}

// TestWlScrollButton covers the direction → X11 button mapping.
func TestWlScrollButton(t *testing.T) {
	cases := map[string]int{"up": 4, "down": 5, "left": 6, "right": 7}
	for dir, want := range cases {
		got, err := wlScrollButton(dir)
		if err != nil || got != want {
			t.Errorf("wlScrollButton(%q) = %d, %v; want %d", dir, got, err, want)
		}
	}
	if _, err := wlScrollButton("sideways"); err == nil {
		t.Errorf("expected error for unknown direction")
	}
}

// TestWlButton covers the button-name mapping (empty defaults to left).
func TestWlButton(t *testing.T) {
	cases := map[string]string{"": "left", "left": "left", "right": "right", "middle": "middle", "x": ""}
	for in, want := range cases {
		if got := wlButton(in); got != want {
			t.Errorf("wlButton(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWlValidKey spot-checks the wtype -k key allowlist.
func TestWlValidKey(t *testing.T) {
	if !wlValidKey("Return") || !wlValidKey("F5") {
		t.Errorf("Return/F5 should be valid keys")
	}
	if wlValidKey("NotAKey") {
		t.Errorf("NotAKey should be invalid")
	}
}

// TestSearchSwayNode covers the focused/fullscreen/area window-rect preference logic.
func TestSearchSwayNode(t *testing.T) {
	root := swayNode{
		Nodes: []swayNode{
			{AppID: "chrome", Rect: SwayRect{X: 0, Y: 0, Width: 800, Height: 600}},
			{AppID: "chrome", Rect: SwayRect{X: 4, Y: 4, Width: 1912, Height: 1032}, Focused: true},
		},
	}
	rect, ok := searchSwayNode(&root, "chrome")
	if !ok {
		t.Fatalf("expected a match")
	}
	if rect.Width != 1912 || rect.X != 4 {
		t.Errorf("expected the focused window rect, got %+v", rect)
	}
	if _, ok := searchSwayNode(&root, "nonexistent"); ok {
		t.Errorf("expected no match for nonexistent app_id")
	}
}
