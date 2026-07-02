package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadUnified_SidecarPluginKind proves the sidecar kind→plugin extraction end-to-end
// through the REAL loader: a project `sidecar:` node (formerly the typed core map
// uf.Sidecar) lands in uf.PluginKinds["sidecar"], the Sidecars() accessor reconstructs
// the name-keyed map[string]SidecarDef, and the Config.Sidecar / BundleConfig.Sidecar
// projections (rewired to Sidecars()) stay intact — so every downstream deploy/quadlet
// consumer (which reads the PROJECTED fields) is untouched. The binary-embedded
// `tailscale` template rides in via applyEmbeddedDefaults.
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

	// (1) The entity lands in uf.PluginKinds["sidecar"], NAME-KEYED.
	raw := uf.PluginKinds["sidecar"]
	if _, ok := raw["mysidecar"]; !ok {
		t.Fatalf("sidecar entity not keyed by node name 'mysidecar'; keys %v", raw)
	}

	// (2) The Sidecars() accessor reconstructs the name-keyed library with the
	// authored fields; the binary-embedded `tailscale` template is merged in too.
	sidecars := uf.Sidecars()
	if sidecars["mysidecar"].Image != "example.com/mysidecar:1" {
		t.Errorf("mysidecar.Image = %q, want example.com/mysidecar:1", sidecars["mysidecar"].Image)
	}
	if _, ok := sidecars["tailscale"]; !ok {
		t.Errorf("embedded tailscale template missing from Sidecars() (applyEmbeddedDefaults merge broken); keys %v", sidecars)
	}

	// (3) The projections (rewired to Sidecars()) carry the same library — the shape
	// every deploy consumer reads (Config.Sidecar / BundleConfig.Sidecar).
	cfg := uf.ProjectConfig()
	if cfg == nil || cfg.Sidecar["mysidecar"].Image != "example.com/mysidecar:1" {
		t.Fatalf("ProjectConfig().Sidecar projection lost the sidecar; got %#v", cfg)
	}
	bc := uf.ProjectBundleConfig()
	if bc == nil || bc.Sidecar["mysidecar"].Image != "example.com/mysidecar:1" {
		t.Fatalf("ProjectBundleConfig().Sidecar projection lost the sidecar; got %#v", bc)
	}
}

// TestEmbeddedTailscaleSidecar_ResolvesEndToEnd proves the binary-embedded `tailscale`
// template (an authored `sidecar:` node in charly/charly.yml — the complex one carrying
// security caps + env + a volume + a secret) round-trips through the plugin path:
// #SidecarInput validates the assembled body, the plugin's Invoke canonicalises it via
// spec.Sidecar, and EmbeddedSidecarTemplates() / ResolveSidecarsForConfig read it back
// fully populated — so hasTailscaleSidecar's upstream still resolves "tailscale". A
// reproduction mismatch in #SidecarInput would fail this (or, earlier, embeddedDefaults()
// itself, since the embed is parsed through runPluginKind).
func TestEmbeddedTailscaleSidecar_ResolvesEndToEnd(t *testing.T) {
	templates, err := EmbeddedSidecarTemplates()
	if err != nil {
		t.Fatalf("EmbeddedSidecarTemplates: %v", err)
	}
	ts, ok := templates["tailscale"]
	if !ok {
		t.Fatalf("embedded tailscale template missing; keys %v", templates)
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("tailscale.Image = %q, want ghcr.io/tailscale/tailscale:latest", ts.Image)
	}
	// The complex children must all round-trip through #SidecarInput + spec.Sidecar.
	if ts.Security == nil || len(ts.Security.CapAdd) == 0 {
		t.Fatalf("tailscale security/cap_add lost in round-trip; got %#v", ts.Security)
	}
	if len(ts.Env) == 0 {
		t.Error("tailscale env lost in round-trip")
	}
	if len(ts.Volume) == 0 {
		t.Error("tailscale volume lost in round-trip")
	}
	if len(ts.Secret) == 0 {
		t.Error("tailscale secret lost in round-trip")
	}

	// The resolution path (what feeds hasTailscaleSidecar) still finds tailscale.
	resolved, err := ResolveSidecarsForConfig(nil, map[string]SidecarDef{"tailscale": {}})
	if err != nil {
		t.Fatalf("ResolveSidecarsForConfig: %v", err)
	}
	if _, ok := resolved["tailscale"]; !ok {
		t.Fatalf("tailscale not resolvable end-to-end (hasTailscaleSidecar upstream broken); got %v", resolved)
	}
}
