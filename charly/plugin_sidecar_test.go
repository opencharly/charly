package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/spec"
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
	bc := uf.ProjectBundleConfig()
	if bc == nil || sidecarBodyImage(t, bc.Sidecar["mysidecar"]) != "example.com/mysidecar:1" {
		t.Fatalf("ProjectBundleConfig().Sidecar projection lost the sidecar; got %#v", bc)
	}
}

// TestSidecarResolve_LiveDispatch exercises the REAL production dispatch seam the
// sidecar de-type introduced (Cutover D): providerRegistry.ResolveKind("sidecar") →
// Invoke(OpResolve) → wire round-trip → reply, with the COMPILED-IN
// candy/plugin-sidecar provider — the exact path a live `charly config` takes. It
// proves env-routing, the embedded-template merge, and reply decoding all survive
// the real registry + wire, not just an in-package function call.
func TestSidecarResolve_LiveDispatch(t *testing.T) {
	if _, ok := providerRegistry.ResolveKind("sidecar"); !ok {
		t.Fatal("sidecar kind must resolve to the compiled-in candy/plugin-sidecar provider")
	}
	embedded, err := embeddedSidecarBodies()
	if err != nil {
		t.Fatalf("embeddedSidecarBodies: %v", err)
	}
	reply, err := resolveSidecarsViaPlugin(spec.SidecarResolveInput{
		EmbeddedTemplates: embedded,
		DeployOverrides:   map[string]json.RawMessage{"tailscale": json.RawMessage(`{"env":{"TS_HOSTNAME":"e2e"},"parameter":{"tailnet":"example.ts.net"}}`)},
		CliEnv:            []string{"TS_EXTRA_ARGS=--foo", "APP_VAR=x"},
		Box:               "e2e-app",
	})
	if err != nil {
		t.Fatalf("resolveSidecarsViaPlugin: %v", err)
	}
	// App-only env survives; the TS_ var routed to the sidecar through the wire.
	if len(reply.AppEnv) != 1 || reply.AppEnv[0] != "APP_VAR=x" {
		t.Errorf("AppEnv = %v, want [APP_VAR=x]", reply.AppEnv)
	}
	// The embedded tailscale template resolved through the compiled-in provider.
	if len(reply.Sidecars) != 1 || reply.Sidecars[0].Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Fatalf("resolved sidecars = %+v, want the tailscale image", reply.Sidecars)
	}
	if reply.Sidecars[0].Env["TS_HOSTNAME"] != "e2e" {
		t.Errorf("deploy env override lost through dispatch: %v", reply.Sidecars[0].Env)
	}
}
