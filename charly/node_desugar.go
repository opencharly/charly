package main

// node_desugar.go — the parse-time PLUGIN-VERB SUGAR desugar. An authored plan
// step carries one intent keyword (run/check/agent-run/agent-check/include) plus
// at most ONE verb-position key: a builtin install verb (an authored #Op field)
// or the generic plugin sugar `<word>: <input>`. The desugar rewrites the sugar
// key into the INTERNAL plugin/plugin_input pair — a map value verbatim, a
// scalar/list value via the plugin's declared PRIMARY input field — so every
// downstream consumer (the raw-value CUE gates, decodeNodeValue, buildBundleNode,
// the label baker) sees only the internal form. Deterministic without the
// provider registry: after removing the intent keyword and every authored #Op
// field (spec.AuthoringVerbs), EXACTLY ONE key may remain — that key IS the
// plugin word; word-exists validation stays at dispatch/`charly box validate`,
// same timing as before the cutover.
//
// Authoring plugin:/plugin_input: directly in a step is a HARD load error — the
// envelope became internal-only in the schema-compaction cutover.

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
