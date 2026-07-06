package main

import (
	"context"
	"strings"
	"testing"
)

// --- §F resolveLocalImageRef tests ---

// withLocalImages swaps ListLocalImages for the duration of the test.
func withLocalImages(t *testing.T, images []LocalImageInfo) {
	t.Helper()
	orig := ListLocalImages
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		return images, nil
	}
	t.Cleanup(func() { ListLocalImages = orig })
}

// withLocalImageExists swaps LocalImageExists for the duration of the test.
func withLocalImageExists(t *testing.T, match func(engine, ref string) bool) {
	t.Helper()
	orig := LocalImageExists
	LocalImageExists = match
	t.Cleanup(func() { LocalImageExists = orig })
}

func TestResolveLocalImageRef_FullRefPresent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool {
		return ref == "ghcr.io/opencharly/jupyter:latest"
	})
	got, err := resolveLocalImageRef("podman", "ghcr.io/opencharly/jupyter:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:latest" {
		t.Errorf("full ref should pass through; got %q", got)
	}
}

func TestResolveLocalImageRef_FullRefAbsent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool { return false })
	_, err := resolveLocalImageRef("podman", "ghcr.io/acme/missing:latest")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected ErrImageNotLocal, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameLabelMatch(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{
			Names:  []string{"ghcr.io/opencharly/jupyter:latest"},
			Labels: map[string]string{LabelBox: "jupyter"},
		},
		{
			Names:  []string{"ghcr.io/opencharly/filebrowser:latest"},
			Labels: map[string]string{LabelBox: "filebrowser"},
		},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:latest" {
		t.Errorf("label match should return full ref; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameNameMatchFallback(t *testing.T) {
	// No charly label → falls back to repo-name trailing component match.
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/someone-else/jupyter:latest"}},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/someone-else/jupyter:latest" {
		t.Errorf("name fallback should match trailing component; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameLabelPreferredOverName(t *testing.T) {
	// Both a label-matched image AND a name-matched image exist; label wins.
	withLocalImages(t, []LocalImageInfo{
		{
			Names:  []string{"ghcr.io/someone-else/jupyter:latest"},
			Labels: map[string]string{}, // name-only
		},
		{
			Names:  []string{"ghcr.io/opencharly/jupyter:v2"},
			Labels: map[string]string{LabelBox: "jupyter"},
		},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:v2" {
		t.Errorf("label-matched image should win; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameAmbiguousError(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/one/jupyter:latest"}},
		{Names: []string{"ghcr.io/two/jupyter:latest"}},
	})
	_, err := resolveLocalImageRef("podman", "jupyter")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameNoMatch(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/opencharly/jupyter:latest"}},
	})
	_, err := resolveLocalImageRef("podman", "filebrowser")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected ErrImageNotLocal, got: %v", err)
	}
}

// --- live-container verb box-mode skip ---

func TestLiveVerb_SkipsUnderBoxMode(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeBox)
	r.Box = "jupyter"
	// A live verb's runtime-context legality rides the AUTHORED `context:` since
	// the live-verb externalization (the generic `plugin` verb itself is
	// context-permissive) — a wl step authors context: [runtime], exactly as the
	// real candies do.
	res := r.Run(context.Background(), []Op{{Plugin: "wl", PluginInput: map[string]any{"method": "status"}, Context: []string{"runtime"}}})
	if len(res) != 1 || res[0].Status != TestSkip {
		t.Fatalf("expected skip under RunModeBox, got %+v", res[0])
	}
	// A runtime-context step is skipped in box mode by the context-vs-mode
	// gate (the unified-Op replacement for the per-verb "needs a running
	// container" skip).
	if !strings.Contains(res[0].Message, "not active in box mode") {
		t.Errorf("expected context-not-active skip message, got %q", res[0].Message)
	}
}

// --- §D validation tests ---

// The former in-proc live-verb validation tests were removed when every live verb
// externalized into its candy/plugin-* and the compiled-in live-verb runtime was
// deleted: unknown-method + build-context rejection are CUE concerns (the per-verb
// #*Method enums + the #Op context rules — see TestCueTightening_RejectsAndAccepts
// "candy cdp bogus method rejected" and the mcp/spice/libvirt bogus-method cases),
// and the required-modifier check lives in the plugin (candy/plugin-wl/methods_test.go's
// TestCheckRequiredModifiers). The box-mode skip stays covered by
// TestLiveVerb_SkipsUnderBoxMode above.

// --- Check.Kind() classifies every live verb via the generic plugin envelope ---

func TestCheckKind_NewVerbsDispatched(t *testing.T) {
	cases := []struct {
		name string
		c    Op
		verb string
	}{
		{"cdp", Op{Plugin: "cdp", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"wl", Op{Plugin: "wl", PluginInput: map[string]any{"method": "screenshot", "artifact": "/tmp/x"}}, "plugin"},
		{"dbus", Op{Plugin: "dbus", PluginInput: map[string]any{"method": "list"}}, "plugin"},
		{"vnc", Op{Plugin: "vnc", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"record", Op{Plugin: "record", PluginInput: map[string]any{"method": "list"}}, "plugin"},
		{"spice", Op{Plugin: "spice", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"libvirt", Op{Plugin: "libvirt", PluginInput: map[string]any{"method": "info"}}, "plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.Kind()
			if err != nil {
				t.Fatalf("Kind() error: %v", err)
			}
			if got != tc.verb {
				t.Errorf("expected verb %q, got %q", tc.verb, got)
			}
		})
	}
}

// --- shortNameMatchesRef edge cases ---

func TestShortNameMatchesRef(t *testing.T) {
	cases := []struct {
		fullRef string
		short   string
		want    bool
	}{
		{"ghcr.io/opencharly/jupyter:latest", "jupyter", true},
		{"ghcr.io/opencharly/jupyter", "jupyter", true}, // no tag
		{"localhost/jupyter:v2", "jupyter", true},
		{"jupyter:latest", "jupyter", true}, // no registry
		{"ghcr.io/opencharly/jupyter:latest", "filebrowser", false},
		{"ghcr.io/opencharly/something-jupyter:latest", "jupyter", false}, // not a trailing match
	}
	for _, tc := range cases {
		got := shortNameMatchesRef(tc.fullRef, tc.short)
		if got != tc.want {
			t.Errorf("shortNameMatchesRef(%q, %q) = %v, want %v", tc.fullRef, tc.short, got, tc.want)
		}
	}
}

// TestPosKubeRaw_JsonFlagThreaded was removed in the kube → external-plugin
// dep-shed: posKubeRaw (the `charly check kube raw` argv builder) left charly's core
// with the rest of the kube verb. The `json: true` step modifier is now read
// directly off the Op by candy/plugin-kube's runRaw (op.JSON → the full JSON List
// document vs the `<namespace>/<name>` line form).
