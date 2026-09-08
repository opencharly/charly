package main

// node_desugar.go — the plugin-verb PRIMARY-input registry. NOT the desugar itself.
//
// The desugar MECHANISM (rewriting an authored step's `<word>: <input>` sugar into the internal
// plugin/plugin_input pair) relocated to sdk/loaderkit with the rest of the parse —
// loaderkit/parse.go's desugarEntityPlan/desugarStep — and it reads the primaries it needs as DATA
// off the spec.Threaded snapshot, never from a registry. What is left here, and what this file is,
// is the host-side TABLE that snapshot is built from.
//
// It stays kernel on two clauses, not one: the table is kind-recognition DATA consulted by word
// (clause D — loaderThreaded() projects it into spec.Threaded.Primaries before every parse), and
// registerPluginPrimary MUTATES it from the provider registry at capability-registration time
// (clause M — plugin loading) — the compiled-in units seed it AT INIT from their declared
// capability primaries (the ONE seeding source, parser consolidation F2.6; the frozen 11-entry
// shorthand table is deleted, its expectation pinned by a parity-assertion test). Neither half
// can move without moving the registry.
//
// The scalar sugar it serves: `file: /usr/bin/xterm` desugars to plugin_input: {file: …} via the
// word's declared PRIMARY field; a map value passes through verbatim. Authoring
// plugin:/plugin_input: directly in a step is a HARD load error — the envelope became
// internal-only in the schema-compaction cutover.
//
// A byte-identical COPY of this file lived at sdk/kit/plugin_primary.go, whose header claimed
// charly's two registration call sites called kit.RegisterPluginPrimary directly. They never did —
// charly core cannot import sdk/kit (import purity), so the K4 relocation was authored but never
// wired, leaving two SEPARATE mutable maps of the same registry. The sdk copy had zero consumers
// anywhere and is DELETED (K-wave 2 cone R1 unit C); this is the one live copy.

import (
	"fmt"
)

// pluginPrimaries maps a plugin verb word to its declared PRIMARY input field —
// the target of the scalar sugar shorthand (`file: /usr/bin/xterm` →
// plugin_input: {file: …}). Seeded at init from the COMPILED-IN units' declared
// capability primaries (RegisterBuiltinPluginUnit/RegisterBuiltinProvider →
// register()'s primaryCarrier hook — the ONE compiled-in seeding path, parser
// consolidation F2.6); the byte-gated prescan registers an external plugin's
// declared primary before parse, and a connected out-of-process provider's
// served ProvidedCapability.Primary re-registers the same value at connect.
// The former FROZEN 11-entry table (the live-container verbs' shared `method`
// shorthand) is DELETED — it was a hard-coded duplicate of the declared-primary
// universe the same registration hook serves; the expected set is now a PARITY
// ASSERTION in node_desugar_test.go, so a drift between the served primaries and
// the parse-time expectation fails a test, never silently desugars wrong.
var pluginPrimaries = map[string]string{}

// init seeds the parse-time desugar table from the COMPILED-IN declared capability primaries
// (parser consolidation F2.6): the platform live-container verbs' `method` primary is carried
// by the embedded charly.yml's `verb_primaries:` directive (the binary's OWN compiled-in
// declaration — their serving plugins are out-of-process, so their served
// ProvidedCapability.Primary cannot be read at init). The register() primaryCarrier hook then
// adds every compiled-in verb candy's declared primary at its registration, and the byte-gated
// prescan + connected external providers mutate the same table — ONE table, seeded from DATA,
// never a hard-coded literal.
func init() {
	var doc struct {
		VerbPrimaries map[string]string `yaml:"verb_primaries"`
	}
	unmarshalEmbeddedDefaults(&doc)
	for w, f := range doc.VerbPrimaries {
		if f != "" {
			pluginPrimaries[w] = f
		}
	}
}

// registerPluginPrimary declares word's primary input field. A verb word that
// collides with an authored #Op field is rejected at registration — the sugar
// rule could never reach it (the field would classify as a builtin modifier).
func registerPluginPrimary(word, field string) error {
	if authoredOpFieldSet[word] {
		return fmt.Errorf("plugin verb word %q collides with an authored #Op field — pick a non-colliding word", word)
	}
	pluginPrimaries[word] = field
	return nil
}

// pluginPrimaryFor returns word's declared primary input field. Used by the
// plugin-load schema gate's primary cross-check (a host-side registry consult,
// distinct from the deploy-state writer's resugar, which reads primaries as DATA).
func pluginPrimaryFor(word string) (string, bool) {
	f, ok := pluginPrimaries[word]
	return f, ok
}

// (resugarPlan — the save-side desugar inverse — moved to deploykit.MarshalDeployNode's own
// resugarPlan in the deploy_nodeform convergence: it reads the primaries D-fact as DATA, so it is
// plugin-reachable. pluginPrimaryFor above stays here as the host LOAD-path schema-gate consult.)
