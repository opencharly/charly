package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// testFleetDoc is a fixture decode target matching deploykit.FleetConfig's shape
// (Fleet map[string]spec.FleetNode, yaml key "deploy") without importing deploykit.
type testFleetDoc struct {
	Fleet map[string]spec.FleetNode `yaml:"deploy" json:"deploy"`
}

// Note: vmshared.VmSpec carries no Disposable / Lifecycle fields and
// the IsDisposableFields helper — disposability is now a DEPLOY
// property only (see /charly-internals:disposable). The former
// TestVmSpec_DisposableRoundTrip / TestVmSpec_LifecycleAloneDoesNotAuthorize
// tests moved to the FleetNode-level equivalents below.

// TestDeployBoxConfig_DisposableRoundTrip — same invariants for
// the container-deploy side.
func TestDeployBoxConfig_DisposableRoundTrip(t *testing.T) {
	yamlStr := `
disposable: true
lifecycle: dev
`
	var c spec.FleetNode
	if err := decodeViaCUEForTest(t, yamlStr, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.IsDisposable() {
		t.Error("FleetNode.IsDisposable() = false; want true")
	}
	if got := c.LifecycleTag(); got != "dev" {
		t.Errorf("LifecycleTag = %q; want dev", got)
	}
}

// TestDeployBoxConfig_LifecycleAloneDoesNotAuthorize — container
// mirror of the critical anti-derivation test.
func TestDeployBoxConfig_LifecycleAloneDoesNotAuthorize(t *testing.T) {
	yamlStr := `lifecycle: dev`
	var c spec.FleetNode
	if err := decodeViaCUEForTest(t, yamlStr, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.IsDisposable() {
		t.Fatal("FleetNode{Lifecycle: dev}.IsDisposable() = true; want false.")
	}
}

// TestMultipleInstances_IndependentFlags — two deploy entries for
// the same image with different disposable values must behave
// independently (the multi-instance requirement).
func TestMultipleInstances_IndependentFlags(t *testing.T) {
	// Under singular kinds, FleetConfig.Deploy is keyed
	// off the `deployment:` (singular) YAML map, not the legacy plural
	// `images:`. The resolver renamed both files (deploy.yml carries
	// `deployment:`) — fixture follows suit.
	yamlStr := `
provides:
  registry: localhost
deploy:
  fedora-coder:
    lifecycle: prod
  fedora-coder-dev:
    lifecycle: dev
  fedora-coder-qa:
    lifecycle: qa
    disposable: true
  fedora-coder-scratch:
    disposable: true
`
	var cfg testFleetDoc
	if err := decodeViaCUEForTest(t, yamlStr, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tests := []struct {
		key  string
		want bool
	}{
		{"fedora-coder", false},        // lifecycle: prod, no disposable → false
		{"fedora-coder-dev", false},    // lifecycle: dev, no disposable → STILL false
		{"fedora-coder-qa", true},      // explicit disposable: true
		{"fedora-coder-scratch", true}, // explicit disposable: true
	}
	for _, tc := range tests {
		e, ok := cfg.Fleet[tc.key]
		if !ok {
			t.Fatalf("image %q missing", tc.key)
		}
		if got := e.IsDisposable(); got != tc.want {
			t.Errorf("%s.IsDisposable() = %v; want %v", tc.key, got, tc.want)
		}
	}
}
