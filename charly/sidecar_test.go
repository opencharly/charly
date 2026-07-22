package main

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestFindPodSidecarQuadlets_* DELETED (Cutover B unit 2, R1 dead-code catch): findPodSidecarQuadlets
// itself was deleted from sidecar.go as zero-production-caller dead code — these were its only
// exercise, so they moved with it (i.e. nowhere; the function is gone, not relocated, since the
// production sweep it purported to test was already superseded by resolveSidecarNames).

// TestEmbeddedSidecarTemplates verifies the binary-embedded tailscale template
// (charly.yml `sidecar:`) is well-formed. The kernel stores it as an opaque body;
// this test decodes it purely to assert the embedded vocab is correct.
func TestEmbeddedSidecarTemplates(t *testing.T) {
	bodies, err := embeddedSidecarBodies()
	if err != nil {
		t.Fatal(err)
	}
	if bodies == nil {
		t.Fatal("expected non-nil templates")
	}
	body, ok := bodies["tailscale"]
	if !ok {
		t.Fatal("expected tailscale sidecar in embedded templates")
	}
	var ts spec.Sidecar
	if err := json.Unmarshal(body, &ts); err != nil {
		t.Fatalf("decode tailscale template: %v", err)
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("image = %q, want ghcr.io/tailscale/tailscale:latest", ts.Image)
	}
	if ts.Env["TS_USERSPACE"] != "false" {
		t.Errorf("TS_USERSPACE = %q, want false", ts.Env["TS_USERSPACE"])
	}
	if ts.Env["TS_DEBUG_FIREWALL_MODE"] != "nftables" {
		t.Errorf("TS_DEBUG_FIREWALL_MODE = %q, want nftables", ts.Env["TS_DEBUG_FIREWALL_MODE"])
	}
	if len(ts.Volume) != 1 || ts.Volume[0].Name != "state" {
		t.Errorf("volumes = %v, want [{state /var/lib/tailscale}]", ts.Volume)
	}
	if len(ts.Security.CapAdd) != 2 {
		t.Errorf("cap_add = %v, want [NET_ADMIN SYS_MODULE]", ts.Security.CapAdd)
	}
	if len(ts.Secret) != 1 || ts.Secret[0].Env != "TS_AUTHKEY" {
		t.Errorf("secrets = %v, want [{ts-authkey TS_AUTHKEY}]", ts.Secret)
	}
}
