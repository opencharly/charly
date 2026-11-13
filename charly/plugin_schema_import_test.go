package main

// plugin_schema_import_test.go — sdk#490's acceptance tests on the HOST side.
//
// The plugin half of the concat→package cutover: a plugin's served schema may now IMPORT
// `github.com/opencharly/spec/schema`. Every host compile site used to be a text compile
// with no CUE module in scope, so a consumer that added the import passed its own repo gate
// and then failed at start-up. These tests pin the two host-side halves:
//
//   - one unit that imports spec composes AND the imported definition is enforced, through
//     the host's own gate (registerPluginUnitSchema) and the ONE input validator
//     (validateAuthoredPluginInput);
//   - TWO such units register in one process, which is the SECOND, independent failure the
//     issue measured: N units joined into one compilation unit put N import clauses in one
//     file ("expected label or ':', found 'STRING'"), so the gate must compose per unit and
//     unify the values;
//   - the host's composed unit is byte-for-byte the value the SERVE side composes from the
//     same source (schemacompose.ServedSchema), because serve-side and host-side judge the same
//     schema with the same composer.

import (
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"

	"github.com/opencharly/spec/schemacompose"
)

// importUnitSource is a plugin unit in the shape the cutover exists for: it imports the
// spec schema package and cites the definition spec owns instead of restating it.
const importUnitSource = `import "github.com/opencharly/spec/schema"

#ImportUnitTunnelInput: schema.#TunnelConfig & {
	provider: "tailscale" | "cloudflare"
}
`

// TestPluginUnitSchemaImportsSpecDefinition is the R7 arm on the host: the unit composes
// through the load gate, and the IMPORTED definition is live — a violating authored input
// fails with spec's own constraint error, a conforming one passes. Fails before the change:
// the gate's text compile refuses the import ("package ... imported but not defined").
func TestPluginUnitSchemaImportsSpecDefinition(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	if err := registerPluginUnitSchema("importing-unit", PluginSchema{
		CueSource: importUnitSource,
		InputDefs: map[string]string{"verb:importunittunnel": "#ImportUnitTunnelInput"},
	}); err != nil {
		t.Fatalf("a unit that imports the spec schema must pass the load gate: %v", err)
	}

	err := validateAuthoredPluginInput(ClassVerb, "importunittunnel", []byte(`{"provider": 5}`))
	if err == nil {
		t.Fatal("an input violating the IMPORTED spec def must be rejected")
	}
	if !strings.Contains(err.Error(), "provider") || !strings.Contains(err.Error(), "string") {
		t.Fatalf("the rejection must be spec's own constraint on provider/string; got: %v", err)
	}
	t.Logf("violating spec.#TunnelConfig -> %v", err)

	if err := validateAuthoredPluginInput(ClassVerb, "importunittunnel", []byte(`{"provider":"tailscale"}`)); err != nil {
		t.Fatalf("a conforming input must pass: %v", err)
	}
}

// TestTwoImportingUnitsComposePerUnit pins the SECOND failure the issue measured: with two
// importing units in one process, a gate that joins the sources into one compilation unit
// fails before resolution ("expected label or ':', found 'STRING'"). Per-unit compose then
// unify accepts both, and each unit still validates its OWN input.
func TestTwoImportingUnitsComposePerUnit(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	unitA := `import "github.com/opencharly/spec/schema"

#ImportUnitAInput: schema.#TunnelConfig & {
	provider: "tailscale"
}
`
	unitB := `import "github.com/opencharly/spec/schema"

#ImportUnitBInput: schema.#TunnelPort & {
	port: int & >0
}
`
	if err := registerPluginUnitSchema("importing-unit-a", PluginSchema{
		CueSource: unitA, InputDefs: map[string]string{"verb:importunita": "#ImportUnitAInput"},
	}); err != nil {
		t.Fatalf("unit A (importing) must register: %v", err)
	}
	if err := registerPluginUnitSchema("importing-unit-b", PluginSchema{
		CueSource: unitB, InputDefs: map[string]string{"verb:importunitb": "#ImportUnitBInput"},
	}); err != nil {
		t.Fatalf("unit B (importing) must register after unit A: %v", err)
	}

	if err := validateAuthoredPluginInput(ClassVerb, "importunita", []byte(`{"provider":"tailscale"}`)); err != nil {
		t.Fatalf("unit A must validate its own input: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "importunitb", []byte(`{"port":8080}`)); err != nil {
		t.Fatalf("unit B must validate its own input: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "importunitb", []byte(`{"port":0}`)); err == nil {
		t.Fatal("unit B's own constraint (port > 0) must still be enforced")
	}
}

// TestHostAndServeComposeTheSameUnit proves the two halves of the contract agree byte for
// byte: the host composes the package-less body that travelled over the wire, the serve side
// composes the plugin's schema FS, and the two values render identically and judge documents
// identically.
func TestHostAndServeComposeTheSameUnit(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	// The serve side: a plugin's embedded FS carrying the SAME schema file.
	pluginFS := fstest.MapFS{"schema/tunnel.cue": &fstest.MapFile{Data: []byte(importUnitSource)}}
	served, err := schemacompose.ServedSchema(nil, pluginFS, "schema")
	if err != nil {
		t.Fatalf("serve-side compose: %v", err)
	}

	// The host side: the unit's text reaches the gate exactly as it travelled on
	// Capabilities.schema_cue.
	if err := registerPluginUnitSchema("agreement-unit", PluginSchema{
		CueSource: importUnitSource,
		InputDefs: map[string]string{"verb:agreementunit": "#ImportUnitTunnelInput"},
	}); err != nil {
		t.Fatalf("host-side registration: %v", err)
	}
	pluginSchemas.mu.Lock()
	hostUnit := pluginSchemas.units[len(pluginSchemas.units)-1]
	pluginSchemas.mu.Unlock()

	if a, b := renderComposedUnit(t, served), renderComposedUnit(t, hostUnit); a != b {
		t.Fatalf("serve-side and host-side must compose the same unit:\n--- serve ---\n%s\n--- host ---\n%s", a, b)
	}

	for _, doc := range []string{`{"provider":"tailscale"}`, `{"provider":5}`, `{"ports":[{"port":"eighty"}]}`} {
		if a, b := judgeUnitDoc(t, served, "#ImportUnitTunnelInput", doc), judgeUnitDoc(t, hostUnit, "#ImportUnitTunnelInput", doc); a != b {
			t.Fatalf("document %s judged differently by the two halves:\n serve: %s\n host: %s", doc, a, b)
		}
	}
}

// renderComposedUnit renders a composed unit as canonical CUE. A cue.Value is only valid in
// the context that built it, so the comparison is what the value SAYS.
func renderComposedUnit(t *testing.T, v cue.Value) string {
	t.Helper()
	n := v.Syntax(cue.Final(), cue.Concrete(false), cue.Definitions(true), cue.Hidden(true),
		cue.Optional(true), cue.Attributes(true), cue.Docs(false))
	data, err := format.Node(n)
	if err != nil {
		t.Fatalf("rendering the composed unit: %v", err)
	}
	return string(data)
}

// judgeUnitDoc unifies a document with a def of a composed unit and reports the verdict as a
// string ("<nil>" when the unit accepts the document).
func judgeUnitDoc(t *testing.T, unit cue.Value, def, doc string) string {
	t.Helper()
	d := unit.LookupPath(cue.ParsePath(def))
	if d.Err() != nil {
		t.Fatalf("composed unit has no %s: %v", def, d.Err())
	}
	in := cuecontext.New().CompileString(doc)
	if in.Err() != nil {
		t.Fatalf("test document does not compile: %v", in.Err())
	}
	if err := d.Unify(in).Validate(cue.Concrete(true)); err != nil {
		return err.Error()
	}
	return "<nil>"
}
