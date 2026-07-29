package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

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
// downstream deploy/quadlet consumer is untouched. The binary-embedded `tailscale`
// template rides in via applyEmbeddedDefaults.
func TestLoadUnified_SidecarPluginKind(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mysidecar:
  sidecar:
    description: a project-declared sidecar
    image: example.com/mysidecar:1
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
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
	// The binary-embedded `tailscale` template is merged in.
	if _, ok := raw["tailscale"]; !ok {
		t.Errorf("embedded tailscale template missing from PluginKinds[sidecar] (applyEmbeddedDefaults merge broken); keys %v", raw)
	}

	// (2) The projections carry the same opaque library — the shape every deploy
	// consumer reads (Config.Sidecar / BundleConfig.Sidecar).
	cfg := uf.ProjectConfig()
	if cfg == nil || sidecarBodyImage(t, cfg.Sidecar["mysidecar"]) != "example.com/mysidecar:1" {
		t.Fatalf("ProjectConfig().Sidecar projection lost the sidecar; got %#v", cfg)
	}
	bc := deploykit.ProjectBundleConfig(uf)
	if bc == nil || sidecarBodyImage(t, bc.Sidecar["mysidecar"]) != "example.com/mysidecar:1" {
		t.Fatalf("ProjectBundleConfig().Sidecar projection lost the sidecar; got %#v", bc)
	}
}
