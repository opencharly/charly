package migrate

import "gopkg.in/yaml.v3"

// reshape_strip_candy_libvirt.go — the candy-level `libvirt:` field removal (R5 claim-keyed
// sweep, Cutover B unit 3+4). The candy-level `libvirt: [...raw XML snippet strings...]` field
// (sdk/schema/candy.cue #Candy.libvirt + candymodel.cue #CandyModel.libvirt, the
// spec.CandyReader.Libvirt()/HasLibvirt() accessors) had ZERO live Go consumers: its ONLY
// reader, charly/libvirt.go's CollectLibvirtSnippets, was ALREADY dead on main before this
// cutover (box-level libvirt snippets were retired in the VM hard-cutover — the paired `kind:
// vm` entity's `spec.libvirt.snippets:` is the live surface now). This hook removes the field
// from an authored document so a config carrying it still loads after the schema stops
// declaring it.
//
// This is a DECLARATIVE-OP-UNSAFE removal: `libvirt` is ALSO a live check-verb WORD (the
// `libvirt: <input>` step sugar, e.g. `{check: "...", libvirt: {method: screenshot, ...}}`,
// dispatched to candy/plugin-vm's verb:libvirt) that can legally appear inside ANY candy's
// `plan:` list, and a `vm:`-kind entity's OWN `libvirt: {devices: ..., features: ...}` domain
// config (an OBJECT, sdk/schema/vm.cue #Vm.libvirt) is a completely different, live field. A
// blanket `{op: "delete_key", key: "libvirt", under_kind: "candy", scope: "any"}` would ALSO
// strip both of those, since under_kind's subtree match includes the candy's own `plan:` steps.
// So this hook targets ONLY the direct, immediate child `libvirt:` key of a `candy:` entity's
// value mapping — never descending into that candy's `plan:` (or anything else) to look for a
// second, unrelated `libvirt:` key.
func stripCandyLibvirtField(doc *yaml.Node) bool {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	return stripCandyLibvirtFieldRec(root)
}

// stripCandyLibvirtFieldRec walks the WHOLE document tree (candy nodes are ordinarily
// top-level, name-first entities, but member nesting is a general document capability, so this
// recurses defensively) looking for any mapping carrying a direct `candy:` key. For each one
// found, it removes a direct `libvirt:` child of THAT candy value's own mapping (one level —
// never recursing past it for this specific delete) then continues the outer walk into every
// child (so a nested candy elsewhere in the tree is still found).
func stripCandyLibvirtFieldRec(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if stripCandyLibvirtFieldRec(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Value == "candy" && val.Kind == yaml.MappingNode {
				if deleteDirectChildKey(val, "libvirt") {
					changed = true
				}
			}
			// Keep walking every child regardless (nested members, namespaces, …) —
			// deleteDirectChildKey above only ever touches the candy value's OWN
			// immediate children, so this outer recursion never re-visits what it
			// already stripped from.
			if stripCandyLibvirtFieldRec(val) {
				changed = true
			}
		}
	}
	return changed
}

// deleteDirectChildKey removes ONE direct key/value pair from mapping m by key name (no
// recursion into m's own children beyond that one pair) — the same comment-preserving splice
// applyOpToMapping's "delete_key" case uses, factored out so this hook doesn't duplicate it.
func deleteDirectChildKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if i+2 < len(m.Content) && m.Content[i].HeadComment != "" && m.Content[i+2].HeadComment == "" {
				m.Content[i+2].HeadComment = m.Content[i].HeadComment
			}
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}
