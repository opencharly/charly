package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// testProjectBundleConfig is a thin local port of sdk/deploykit.ProjectBundleConfig — an
// ASSERTION-TAIL projection over spec.UnifiedFile's already-loaded fields (Bundle/Provides/
// PluginKinds["sidecar"]), not a re-derivation of the loader itself. What this test asserts is
// the SELECTION (does the sidecar survive into the bundle-config projection), which this ~10
// line reader re-expresses directly over spec types with zero sdk import.
func testProjectBundleConfig(uf *spec.UnifiedFile) *spec.BundleConfig {
	if uf == nil {
		return nil
	}
	sidecars := uf.PluginKinds["sidecar"]
	if len(uf.Bundle) == 0 && uf.Provides == nil && len(sidecars) == 0 {
		return nil
	}
	return &spec.BundleConfig{
		Provides: uf.Provides,
		Bundle:   uf.Bundle,
		Sidecar:  sidecars,
	}
}

// sidecarBodyImage peeks the `image` field of an opaque sidecar body — the kernel
// stores sidecar defs untyped (the sidecar de-type, Cutover D), so tests decode.
func sidecarBodyImage(t *testing.T, body json.RawMessage) string {
	t.Helper()
	var s struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode sidecar body: %v", err)
	}
	return s.Image
}

// TestLoadUnified_SidecarPluginKind proves the sidecar kind→plugin extraction
// end-to-end through the REAL loader: a project `sidecar:` node lands in
// uf.PluginKinds["sidecar"] as an OPAQUE body, and the Config.Sidecar /
// BundleConfig.Sidecar projections carry the same opaque library — so every
// downstream deploy/quadlet consumer is untouched. The embedded `tailscale` default
// no longer rides in via applyEmbeddedDefaults (it moved to candy/plugin-deploy-pod's
// own go:embed, K-wave 2 cone R3).
func TestLoadUnified_SidecarPluginKind(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mysidecar:
  sidecar:
    description: a project-declared sidecar
    image: example.com/mysidecar:1
`
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified sidecar plugin kind: %v", err)
	}

	// (1) The entity lands in uf.PluginKinds["sidecar"], NAME-KEYED, opaque.
	raw := uf.PluginKinds["sidecar"]
	if _, ok := raw["mysidecar"]; !ok {
		t.Fatalf("sidecar entity not keyed by node name 'mysidecar'; keys %v", raw)
	}
	if img := sidecarBodyImage(t, raw["mysidecar"]); img != "example.com/mysidecar:1" {
		t.Errorf("mysidecar image = %q, want example.com/mysidecar:1", img)
	}
	// The binary-embedded `tailscale` template is NO LONGER merged in — it moved into
	// candy/plugin-deploy-pod's own go:embed (sidecar_embedded.go), K-wave 2 cone R3, so a
	// project's PluginKinds["sidecar"] carries only the project's OWN sidecar declarations.
	if _, ok := raw["tailscale"]; ok {
		t.Errorf("embedded tailscale template should NOT be in PluginKinds[sidecar] anymore (moved to plugin-deploy-pod's go:embed); keys %v", raw)
	}

	// (2) The projections carry the same opaque library — the shape every deploy
	// consumer reads (Config.Sidecar / BundleConfig.Sidecar).
	cfg := uf.ProjectConfig()
	if cfg == nil || sidecarBodyImage(t, cfg.Sidecar["mysidecar"]) != "example.com/mysidecar:1" {
		t.Fatalf("ProjectConfig().Sidecar projection lost the sidecar; got %#v", cfg)
	}
	bc := testProjectBundleConfig(uf)
	if bc == nil || sidecarBodyImage(t, bc.Sidecar["mysidecar"]) != "example.com/mysidecar:1" {
		t.Fatalf("ProjectBundleConfig().Sidecar projection lost the sidecar; got %#v", bc)
	}
}
