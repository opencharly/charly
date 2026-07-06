package main

// node_parse.go — the generic parser for the COMPACT node-form document tree (the
// ONE clean forward model; NO legacy child-node parse). Every charly.yml element
// is a name-first entity `<name>: {<kind>: <FULL BODY>, <member-name>: <entity>…}`:
//   - the ONE discriminator is a KIND word — core-registered, plugin-served
//     (recognizedKind), or an external deploy substrate — and its value is the
//     COMPLETE per-kind body: scalars, collections, and the ordered `plan:` step
//     list all INLINE;
//   - the only other children are sub-ENTITY members, legal only under a
//     deployable (#ResourceKind) kind or an external STRUCTURAL plugin kind; a
//     member's NAME is load-bearing (tree-position venue, `${HOST:member}`), so
//     it must not collide with a kind word;
//   - every plan step is DESUGARED at parse time (node_desugar.go): the generic
//     `<word>: <input>` plugin sugar rewrites into the internal
//     plugin/plugin_input pair BEFORE any raw-bytes CUE validation sees the step.
// The former named data/step child-node layer was DELETED in the
// schema-compaction cutover — a residual old-shape child is a hard load error
// pointing at `charly migrate`.

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// genericNode is one parsed node-form entity.
type genericNode struct {
	name      string         // the node's key (its name)
	disc      string         // the kind discriminator
	discClass string         // always "entity" (kept for downstream switches)
	discValue *yaml.Node     // the kind value: the COMPLETE entity body
	raw       *yaml.Node     // the WHOLE node mapping
	children  []*genericNode // sub-ENTITY members (deployable kinds only)
}

// classifyKind reports whether k is a recognized KIND word in this position.
// At the top level every registered kind (plugin-served via recognizedKind,
// incl. the build-vocab kinds) and every external deploy substrate classifies;
// as a member child only the deployable resource kinds do (a member's own kind).
func classifyKind(k string, asChild bool) bool {
	if asChild {
		return resourceKindSet[k]
	}
	if kindWordSet[k] || stepKeywordSet[k] {
		// stepKeywordSet at the top level means an old-shape STEP child leaked to
		// the top level — impossible in a valid document; treated as not-a-kind so
		// the caller's no-discriminator error (with the migrate hint) fires.
		return kindWordSet[k]
	}
	// A plugin-contributed kind: a registered ClassKind provider (or a
	// pre-scanned external kind word, F4) whose word is not a core kind keyword.
	if recognizedKind(k) {
		return true
	}
	// An external DEPLOY substrate word (e.g. `exampledeploy`): recognized so a
	// deploy/bed using it as its discriminator parses as an entity even before
	// the out-of-process provider connects (loadProjectPlugins).
	return recognizedDeploySubstrate(k)
}

// parseNode builds a genericNode from a node mapping (the value under `name:`).
// asChild is true when the node is a member of another node (vs top-level).
func parseNode(name string, m *yaml.Node, asChild bool) (*genericNode, error) {
	if m.Kind == yaml.DocumentNode && len(m.Content) == 1 {
		m = m.Content[0]
	}
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("node %q: expected a mapping value, got yaml kind %v", name, m.Kind)
	}
	gn := &genericNode{name: name, discClass: "entity", raw: m}
	type kv struct{ k, v *yaml.Node }
	var memberPairs []kv
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		if classifyKind(key.Value, asChild) {
			if gn.disc != "" {
				return nil, fmt.Errorf("node %q: two kind discriminators (%q and %q) — an entity has exactly one, and a member child must not be NAMED like a kind word", name, gn.disc, key.Value)
			}
			gn.disc, gn.discValue = key.Value, val
			continue
		}
		memberPairs = append(memberPairs, kv{key, val})
	}
	if gn.disc == "" {
		return nil, fmt.Errorf("node %q: no kind discriminator — collections and plan steps live INLINE in the kind value (the named child-node shape was removed); run: charly migrate", name)
	}
	// Desugar the body's plan steps in place (plugin sugar → plugin/plugin_input)
	// BEFORE any consumer — including the raw-value CUE gates — sees the body.
	if gn.discValue != nil && gn.discValue.Kind == yaml.MappingNode {
		if err := desugarEntityPlan(name, gn.discValue); err != nil {
			return nil, err
		}
	}
	for _, c := range memberPairs {
		if !resourceKindSet[gn.disc] && !externalKindMayNestMembers(gn.disc) {
			return nil, fmt.Errorf("node %q (kind %q): child %q is not allowed — only deployable kinds (pod/vm/k8s/local/android/group) or an external structural plugin kind nest sub-entity members; an old-shape data/step child must be migrated (run: charly migrate)", name, gn.disc, c.k.Value)
		}
		child, err := parseNode(c.k.Value, c.v, true)
		if err != nil {
			return nil, err
		}
		gn.children = append(gn.children, child)
	}
	return gn, nil
}

// parseNodeTree walks a whole node-form document mapping into its reserved
// directives and its top-level entity nodes.
func parseNodeTree(doc *yaml.Node) (directives map[string]*yaml.Node, nodes []*genericNode, err error) {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("node-form document: expected a top-level mapping, got yaml kind %v", doc.Kind)
	}
	directives = map[string]*yaml.Node{}
	// A single document's top-level node names are GLOBALLY UNIQUE (Key Rules):
	// yaml.Node keeps duplicate keys as separate pairs, so without this gate a
	// duplicate name of a DIFFERENT kind would silently load both entities (and
	// an identical-body same-kind duplicate would pass unnoticed).
	seen := map[string]string{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, val := doc.Content[i], doc.Content[i+1]
		if docDirectiveSet[key.Value] {
			directives[key.Value] = val
			continue
		}
		gn, e := parseNode(key.Value, val, false)
		if e != nil {
			return nil, nil, e
		}
		if prior, dup := seen[gn.name]; dup {
			return nil, nil, fmt.Errorf("node %q: duplicate top-level entity name (already declared as a %q node) — a single document's top-level node names are globally unique; rename one (keep the user-facing deploy name, suffix the template)", gn.name, prior)
		}
		seen[gn.name] = gn.disc
		nodes = append(nodes, gn)
	}
	return directives, nodes, nil
}
