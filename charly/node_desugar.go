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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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

// pluginPrimaryFor returns word's declared primary input field.
func pluginPrimaryFor(word string) (string, bool) {
	f, ok := pluginPrimaries[word]
	return f, ok
}

// desugarEntityPlan desugars every step of the entity body's `plan:` list in
// place. A missing plan is a no-op; a non-sequence plan is a hard error.
func desugarEntityPlan(entity string, body *yaml.Node) error {
	plan := mapValue(body, "plan")
	if plan == nil {
		return nil
	}
	if plan.Kind != yaml.SequenceNode {
		return fmt.Errorf("node %q: plan must be a step LIST (got yaml kind %v); run: charly migrate", entity, plan.Kind)
	}
	for i, st := range plan.Content {
		if err := desugarStep(entity, i, st); err != nil {
			return err
		}
	}
	return nil
}

// desugarStep rewrites one authored step mapping in place.
func desugarStep(entity string, idx int, st *yaml.Node) error {
	if st.Kind != yaml.MappingNode {
		return fmt.Errorf("node %q: plan[%d] must be a mapping step", entity, idx)
	}
	intents := 0
	var sugarKeys []int
	for i := 0; i+1 < len(st.Content); i += 2 {
		k := st.Content[i].Value
		switch {
		case k == "plugin" || k == "plugin_input":
			return fmt.Errorf("node %q: plan[%d] authors %q — the plugin envelope is internal-only; author the verb as `<word>: <input>` sugar (run: charly migrate)", entity, idx, k)
		case stepKeywordSet[k]:
			intents++
		case authoredOpFieldSet[k]:
			// a builtin verb or shared step modifier — stays as-is
		default:
			sugarKeys = append(sugarKeys, i)
		}
	}
	if intents == 0 {
		return fmt.Errorf("node %q: plan[%d] has no intent keyword (run/check/agent-run/agent-check/include)", entity, idx)
	}
	if intents > 1 {
		return fmt.Errorf("node %q: plan[%d] has multiple intent keywords — a step has exactly one", entity, idx)
	}
	if len(sugarKeys) == 0 {
		return nil
	}
	if len(sugarKeys) > 1 {
		names := make([]string, 0, len(sugarKeys))
		for _, i := range sugarKeys {
			names = append(names, st.Content[i].Value)
		}
		sort.Strings(names)
		return fmt.Errorf("node %q: plan[%d] carries multiple non-#Op keys (%s) — a step takes at most ONE plugin-verb sugar key", entity, idx, strings.Join(names, ", "))
	}
	i := sugarKeys[0]
	wordNode, valNode := st.Content[i], st.Content[i+1]
	word := wordNode.Value
	var input *yaml.Node
	switch valNode.Kind {
	case yaml.MappingNode:
		input = valNode
	case yaml.ScalarNode, yaml.SequenceNode:
		prim, ok := pluginPrimaryFor(word)
		if !ok {
			return fmt.Errorf("node %q: plan[%d] plugin verb %q takes a MAP input (it declares no primary field for the scalar shorthand)", entity, idx, word)
		}
		input = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: prim},
			valNode,
		}}
	default:
		// a null value is an input-less verb
		input = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	st.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plugin",
		HeadComment: wordNode.HeadComment, LineComment: wordNode.LineComment, FootComment: wordNode.FootComment}
	st.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: word}
	st.Content = append(st.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plugin_input"}, input)
	return nil
}

// resugarPlan is the desugar's INVERSE, used by the deploy-state WRITER
// (deploy_nodeform.go): each step's internal plugin/plugin_input pair rewrites
// back to the authored `<word>: <input>` sugar (collapsing a single-primary map
// to the scalar shorthand), so a written file round-trips through the
// parse-time desugar instead of tripping its authored-envelope ban.
func resugarPlan(plan *yaml.Node) {
	if plan == nil || plan.Kind != yaml.SequenceNode {
		return
	}
	for _, st := range plan.Content {
		if st.Kind != yaml.MappingNode {
			continue
		}
		pluginIdx, inputIdx := -1, -1
		for i := 0; i+1 < len(st.Content); i += 2 {
			switch st.Content[i].Value {
			case "plugin":
				pluginIdx = i
			case "plugin_input":
				inputIdx = i
			}
		}
		if pluginIdx < 0 {
			continue
		}
		word := st.Content[pluginIdx+1].Value
		input := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if inputIdx >= 0 {
			input = st.Content[inputIdx+1]
		}
		// scalar-collapse: input == {<primary>: <scalar>}
		if prim, ok := pluginPrimaryFor(word); ok && input.Kind == yaml.MappingNode &&
			len(input.Content) == 2 && input.Content[0].Value == prim &&
			input.Content[1].Kind == yaml.ScalarNode {
			input = input.Content[1]
		}
		nc := make([]*yaml.Node, 0, len(st.Content))
		for i := 0; i+1 < len(st.Content); i += 2 {
			switch i {
			case pluginIdx:
				nc = append(nc, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: word,
					HeadComment: st.Content[i].HeadComment}, input)
			case inputIdx:
				// dropped — folded into the sugar key's value
			default:
				nc = append(nc, st.Content[i], st.Content[i+1])
			}
		}
		st.Content = nc
	}
}
