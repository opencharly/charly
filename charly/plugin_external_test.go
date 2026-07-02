package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

// TestExternalPluginEndToEnd proves the FULL out-of-tree plugin lifecycle on real
// code (the headline F1 capability): the reference external plugin (candy/
// plugin-example-external, its OWN Go module serving via plugin/sdk) is built on
// the host, connected OUT-OF-PROCESS over go-plugin gRPC via LocalTransport, and
// its `externalprobe` verb dispatched — with plugin_input.marker round-tripping
// author → wire → external process → result. It ALSO proves the SCHEMA travels
// over the same Describe channel (unit.Schema), the zero-distinction mechanism.
func TestExternalPluginEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-external")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("external plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-external-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}

	// 2. Connect OUT-OF-PROCESS via LocalTransport — providers AND schema arrive on
	//    the unit, both lifted from the Describe channel.
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassVerb || unit.Providers[0].Reserved() != "externalprobe" {
		t.Fatalf("providers = %+v, want exactly one verb:externalprobe", unit.Providers)
	}
	// The schema travelled over the wire (NOT read from disk).
	if unit.Schema.CueSource == "" || unit.Schema.InputDefs["verb:externalprobe"] != "#ExternalprobeInput" {
		t.Fatalf("unit schema = %+v, want non-empty CueSource + #ExternalprobeInput input def", unit.Schema)
	}

	// 3. Register the served schema through the SAME gate a builtin runs, then the
	//    providers, and resolve through the registry (the runPluginVerb path).
	if err := registerPluginUnitSchema("plugin-example-external-test", unit.Schema); err != nil {
		t.Fatalf("registerPluginUnitSchema (served schema): %v", err)
	}
	reg := newRegistry()
	if err := reg.RegisterPluginProviders(unit.Providers, "test:external", nil); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	prov, ok := reg.ResolveVerb("externalprobe")
	if !ok {
		t.Fatal("externalprobe not resolvable after registration")
	}

	// 4. Invoke across the process boundary; the marker must survive the round-trip.
	out, err := prov.Invoke(ctx, &Operation{
		Reserved: "externalprobe", Op: OpRun,
		Params: []byte(`{"plugin_input":{"marker":"external-plugin-ok"}}`), Env: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Invoke (out-of-proc): %v", err)
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		t.Fatalf("decode result: %v (%s)", err, out.JSON)
	}
	if pr.Status != "pass" || pr.Message != "external-plugin-ok" {
		t.Fatalf("result = %+v, want pass + external-plugin-ok (marker round-trip)", pr)
	}
}

// TestPluginSchemaSpliceValidation proves the VALIDATION half of the per-plugin
// CUE contract through the REAL gate + validator (no disk read at runtime — the
// schema source here is the fixture, fed in as a unit would serve it). The
// external plugin's self-contained schema splices onto the charly base
// (base ++ plugin) and an authored externalprobe plugin_input is validated against
// #ExternalprobeInput — a non-empty marker passes, an empty or missing marker is a
// hard error (the `& !=""` / required constraint).
func TestPluginSchemaSpliceValidation(t *testing.T) {
	src, err := os.ReadFile("../candy/plugin-example-external/schema/externalprobe.cue")
	if err != nil {
		t.Fatal(err)
	}
	schema := PluginSchema{
		CueSource: string(src),
		InputDefs: map[string]string{"verb:externalprobe": "#ExternalprobeInput"},
	}
	if err := registerPluginUnitSchema("externalprobe-schema-test", schema); err != nil {
		t.Fatalf("registerPluginUnitSchema: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "externalprobe", []byte(`{"marker":"external-plugin-ok"}`)); err != nil {
		t.Errorf("valid marker should pass base ++ plugin validation: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "externalprobe", []byte(`{"marker":""}`)); err == nil {
		t.Error("empty marker should FAIL (base ++ plugin enforces marker & !=\"\")")
	}
	if err := validateAuthoredPluginInput(ClassVerb, "externalprobe", []byte(`{}`)); err == nil {
		t.Error("missing marker should FAIL (required field)")
	}
}

// TestBuiltinPluginSchemasSplice is the deterministic CI gate for the symmetric
// builtin schema load gate: loadBuiltinPluginUnits must splice EVERY in-tree
// builtin unit's schema onto the base, so a broken builtin schema fails CI, not
// only a live run. It then validates the exampleprobe builtin's plugin_input
// through the same validator an external goes through (zero distinction).
func TestBuiltinPluginSchemasSplice(t *testing.T) {
	if err := loadBuiltinPluginUnits(); err != nil {
		t.Fatalf("builtin plugin schemas must splice onto the base: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "exampleprobe", []byte(`{"marker":"exampleprobe-ok"}`)); err != nil {
		t.Errorf("exampleprobe valid marker should pass: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "exampleprobe", []byte(`{"marker":""}`)); err == nil {
		t.Error("exampleprobe empty marker should FAIL (marker & !=\"\")")
	}
}

// TestExternalSchemaSelfContained proves the self-contained contract every plugin
// schema must honour: a schema that references a base def it does not itself define
// FAILS a standalone compile — the exact property `cue exp gengotypes` relies on to
// generate a plugin's Go params, and the reason the host's base ++ plugin splice is
// a collision check rather than a base-reference resolver.
func TestExternalSchemaSelfContained(t *testing.T) {
	if v := cuecontext.New().CompileString("#Bad: { x: #Step }\n"); v.Err() == nil {
		t.Error("a plugin schema referencing the base #Step must FAIL a standalone compile (not self-contained)")
	}
}
