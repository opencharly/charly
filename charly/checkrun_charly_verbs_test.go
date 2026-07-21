package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// --- §F resolveLocalImageRef tests ---

// withLocalImages swaps kit.ListLocalImages for the duration of the test.
func withLocalImages(t *testing.T, images []kit.LocalImageInfo) {
	t.Helper()
	orig := kit.ListLocalImages
	kit.ListLocalImages = func(engine string) ([]kit.LocalImageInfo, error) {
		return images, nil
	}
	t.Cleanup(func() { kit.ListLocalImages = orig })
}

// withLocalImageExists swaps kit.LocalImageExists for the duration of the test.
// P12a: kit.ResolveLocalImageRef (moved from charly's resolveLocalImageRef) reads
// kit's OWN LocalImageExists var, not core's `LocalImageExists = kit.LocalImageExists`
// alias (a value-copy at init, not a live reference) — swap the one the callee reads.
func withLocalImageExists(t *testing.T, match func(engine, ref string) bool) {
	t.Helper()
	orig := kit.LocalImageExists
	kit.LocalImageExists = match
	t.Cleanup(func() { kit.LocalImageExists = orig })
}

func TestResolveLocalImageRef_FullRefPresent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool {
		return ref == "ghcr.io/opencharly/jupyter:latest"
	})
	got, err := kit.ResolveLocalImageRef("podman", "ghcr.io/opencharly/jupyter:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:latest" {
		t.Errorf("full ref should pass through; got %q", got)
	}
}

func TestResolveLocalImageRef_FullRefAbsent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool { return false })
	_, err := kit.ResolveLocalImageRef("podman", "ghcr.io/acme/missing:latest")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected kit.ErrImageNotLocal, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameLabelMatch(t *testing.T) {
	withLocalImages(t, []kit.LocalImageInfo{
		{
			Names:  []string{"ghcr.io/opencharly/jupyter:latest"},
			Labels: map[string]string{spec.LabelBox: "jupyter"},
		},
		{
			Names:  []string{"ghcr.io/opencharly/filebrowser:latest"},
			Labels: map[string]string{spec.LabelBox: "filebrowser"},
		},
	})
	got, err := kit.ResolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:latest" {
		t.Errorf("label match should return full ref; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameNameMatchFallback(t *testing.T) {
	// No charly label → falls back to repo-name trailing component match.
	withLocalImages(t, []kit.LocalImageInfo{
		{Names: []string{"ghcr.io/someone-else/jupyter:latest"}},
	})
	got, err := kit.ResolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/someone-else/jupyter:latest" {
		t.Errorf("name fallback should match trailing component; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameLabelPreferredOverName(t *testing.T) {
	// Both a label-matched image AND a name-matched image exist; label wins.
	withLocalImages(t, []kit.LocalImageInfo{
		{
			Names:  []string{"ghcr.io/someone-else/jupyter:latest"},
			Labels: map[string]string{}, // name-only
		},
		{
			Names:  []string{"ghcr.io/opencharly/jupyter:v2"},
			Labels: map[string]string{spec.LabelBox: "jupyter"},
		},
	})
	got, err := kit.ResolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/jupyter:v2" {
		t.Errorf("label-matched image should win; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameAmbiguousError(t *testing.T) {
	withLocalImages(t, []kit.LocalImageInfo{
		{Names: []string{"ghcr.io/one/jupyter:latest"}},
		{Names: []string{"ghcr.io/two/jupyter:latest"}},
	})
	_, err := kit.ResolveLocalImageRef("podman", "jupyter")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameNoMatch(t *testing.T) {
	withLocalImages(t, []kit.LocalImageInfo{
		{Names: []string{"ghcr.io/opencharly/jupyter:latest"}},
	})
	_, err := kit.ResolveLocalImageRef("podman", "filebrowser")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected kit.ErrImageNotLocal, got: %v", err)
	}
}

// --- live-container verb box-mode skip ---

func TestLiveVerb_SkipsUnderBoxMode(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeBox, "jupyter")
	// A live verb's runtime-context legality rides the AUTHORED `context:` since
	// the live-verb externalization (the generic `plugin` verb itself is
	// context-permissive) — a wl step authors context: [runtime], exactly as the
	// real candies do.
	res := r.Run(context.Background(), []spec.Op{{Plugin: "wl", PluginInput: map[string]any{"method": "status"}, Context: []string{"runtime"}}})
	if len(res) != 1 || res[0].Status != spec.StatusSkip {
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
		c    spec.Op
		verb string
	}{
		{"cdp", spec.Op{Plugin: "cdp", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"wl", spec.Op{Plugin: "wl", PluginInput: map[string]any{"method": "screenshot", "artifact": "/tmp/x"}}, "plugin"},
		{"dbus", spec.Op{Plugin: "dbus", PluginInput: map[string]any{"method": "list"}}, "plugin"},
		{"vnc", spec.Op{Plugin: "vnc", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"record", spec.Op{Plugin: "record", PluginInput: map[string]any{"method": "list"}}, "plugin"},
		{"spice", spec.Op{Plugin: "spice", PluginInput: map[string]any{"method": "status"}}, "plugin"},
		{"libvirt", spec.Op{Plugin: "libvirt", PluginInput: map[string]any{"method": "info"}}, "plugin"},
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

// TestShortNameMatchesRef moved to sdk/kit/local_image_test.go (P12a) — it tests
// kit's unexported shortNameMatchesRef, relocated with the rest of local_image.go.

// TestPosKubeRaw_JsonFlagThreaded was removed in the kube → external-plugin
// dep-shed: posKubeRaw (the `charly check kube raw` argv builder) left charly's core
// with the rest of the kube verb. The `json: true` step modifier is now read
// directly off the Op by candy/plugin-kube's runRaw (op.JSON → the full JSON List
// document vs the `<namespace>/<name>` line form).
