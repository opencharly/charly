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
// (clause M — plugin loading). Neither half can move without moving the registry.
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
// plugin_input: {file: …}). Compiled-in plugins seed it at init via
// registerPluginPrimary (their capability manifest); the byte-gated prescan
// registers an external plugin's declared primary before parse.
var pluginPrimaries = map[string]string{
	// The 11 live-container verbs' scalar shorthand (`cdp: status`) must desugar
	// at PARSE time — before any out-of-process provider can connect and serve
	// its ProvidedCapability.Primary — so their shared `method` primary is a
	// FROZEN CONVENTION seeded here (the same determinism rationale as the
	// migrate hook's frozen table). A connected plugin's declared primary
	// re-registers the same value; a NEW external verb declares its primary in
	// its candy manifest's plugin.primary map (prescanned pre-parse) instead of
	// extending this table.
	"cdp": "method", "wl": "method", "dbus": "method", "vnc": "method",
	"mcp": "method", "record": "method", "spice": "method", "libvirt": "method",
	"kube": "method", "adb": "method", "appium": "method",
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

// (resugarPlan — the save-side desugar inverse — moved to deploykit.MarshalBundleNode's own
// resugarPlan in the deploy_nodeform convergence: it reads the primaries D-fact as DATA, so it is
// plugin-reachable. pluginPrimaryFor above stays here as the host LOAD-path schema-gate consult.)
