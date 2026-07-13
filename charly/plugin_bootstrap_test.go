package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
)

// zzWireTestBootstrapProvider is a MARKER-GATED bootstrap provider for the F9-wiring test: it
// bumps `version: 2026.001.0001` → HEAD ONLY for a config carrying the unique sentinel, so it
// transforms nothing else in the test process (no global-pollution despite the global registry).
type zzWireTestBootstrapProvider struct{}

func (zzWireTestBootstrapProvider) Reserved() string     { return "zzf9wiretest" }
func (zzWireTestBootstrapProvider) Class() ProviderClass { return ClassVerb }
func (zzWireTestBootstrapProvider) pluginPhase() string  { return sdk.PhaseBootstrap }
func (zzWireTestBootstrapProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	var in struct {
		Config string `json:"config"`
	}
	_ = json.Unmarshal(op.Params, &in)
	out := in.Config
	if strings.Contains(out, "CHARLY_F9_WIRE_TEST") {
		out = strings.Replace(out, "version: 2026.001.0001", "version: "+LatestSchemaVersion().String(), 1)
	}
	j, err := marshalJSON(map[string]string{"config": out})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

// TestBootstrapTransformReachesParse proves the F9 WIRING FIX: a bootstrap plugin's rewrite of the
// root config bytes reaches the actual PARSE (threaded into hostWalkProject as the walk's root
// override) + the post-merge version gate — not just the early version gate. The on-disk config has
// a stale `version:` the bootstrap bumps to HEAD; WITHOUT the fix, the walk re-reads the stale disk
// bytes and the post-merge gate rejects → LoadUnified errors. WITH the fix, LoadUnified succeeds +
// merged.Version is HEAD.
func TestBootstrapTransformReachesParse(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	RegisterBuiltinProvider(zzWireTestBootstrapProvider{}) // global but marker-gated → no pollution
	dir := t.TempDir()
	cfg := "# CHARLY_F9_WIRE_TEST\nversion: 2026.001.0001\n"
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, found, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified errored — the bootstrap version-bump did NOT reach the parse/post-merge gate (F9 wiring defect): %v", err)
	}
	if !found {
		t.Fatal("LoadUnified did not find the config")
	}
	if uf.Version != LatestSchemaVersion().String() {
		t.Errorf("merged.Version=%q, want HEAD %q — the bootstrap transform did not reach the parse", uf.Version, LatestSchemaVersion().String())
	}
}

// TestBootstrapPhase_ExampleEnumeratedAndNoOp proves F9 (phase machinery + bootstrap set): the
// compiled-in candy/plugin-example-bootstrap declares Phase=="bootstrap", so providersInPhase
// enumerates it, and runBootstrapPhase invokes its OpBootstrap returning the config UNCHANGED (the
// no-op). The phase flag travels over Describe (buildUnitInProc) onto the inprocProvider.
func TestBootstrapPhase_ExampleEnumeratedAndNoOp(t *testing.T) {
	found := false
	for _, p := range providerRegistry.providersInPhase(sdk.PhaseBootstrap) {
		if p.Reserved() == "examplebootstrap" {
			found = true
		}
	}
	if !found {
		t.Fatal("compiled-in examplebootstrap not enumerated in PhaseBootstrap (phase flag / pluginsgen / compiled_plugins)")
	}
	in := []byte("version: " + LatestSchemaVersion().String() + "\nfoo: bar\n")
	out := runBootstrapPhase(in)
	if string(out) != string(in) {
		t.Fatalf("no-op bootstrap mutated the config: %q -> %q", in, out)
	}
}

// zzFakeBootstrapProvider is a test bootstrap provider that TRANSFORMS the config — proving
// runBootstrapPhase APPLIES a transforming bootstrap plugin's returned bytes, not just the
// no-op. Unique word so it never collides with a real provider.
type zzFakeBootstrapProvider struct{}

func (zzFakeBootstrapProvider) Reserved() string     { return "zzfakebootstrap" }
func (zzFakeBootstrapProvider) Class() ProviderClass { return ClassVerb }
func (zzFakeBootstrapProvider) pluginPhase() string  { return sdk.PhaseBootstrap }
func (zzFakeBootstrapProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	out, err := marshalJSON(map[string]string{"config": "TRANSFORMED-BY-BOOTSTRAP"})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: out}, nil
}

// TestBootstrapPhase_TransformApplied proves the bootstrap pre-pass APPLIES a bootstrap plugin's
// returned (transformed) bytes. Uses runBootstrapPhaseWith with a fixed
// provider list (NOT the global registry), so the transforming fake cannot pollute the hot-path
// runBootstrapPhase that every other test's LoadUnified runs.
func TestBootstrapPhase_TransformApplied(t *testing.T) {
	if phaseOfProvider(zzFakeBootstrapProvider{}) != sdk.PhaseBootstrap {
		t.Fatal("fake bootstrap provider not classified PhaseBootstrap")
	}
	out := runBootstrapPhaseWith([]byte("version: 1\n"), []Provider{zzFakeBootstrapProvider{}})
	if !strings.Contains(string(out), "TRANSFORMED-BY-BOOTSTRAP") {
		t.Fatalf("runBootstrapPhaseWith did not apply the bootstrap transform: %q", out)
	}
}
