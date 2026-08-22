package migrate

import "gopkg.in/yaml.v3"

// reshape_install_template_to_phases.go — the #Format.install_template /
// #Builder.install_template field removal (strict-cleanup cutover, Unit 3b). The
// legacy top-level `install_template:` field — the (install, container) fallback
// the resolvers fell back to when `phase:` lacked the cell — is REMOVED from the
// closed CUE defs (#Format / #Builder, spec/schema/distro.cue / builder.cue); its
// content migrates into `phase.install.container`, the phase: block's single
// source of truth. This hook MOVES (not deletes) an authored `install_template:`
// scalar into its def's `phase.install.container` cell, so a config carrying the
// old field still loads after the schema stops declaring it.
//
// This is a DECLARATIVE-OP-UNSAFE move: the relocation format.<fmt>.install_template
// → format.<fmt>.phase.install.container is a NESTED move (install_template sits
// directly on the format def, while the target is two levels down under `phase:`),
// which rename_key / move_key (single-level reparents) cannot express — the spec
// side's version.cue comment states exactly this ("the nested move ... cannot be
// expressed as rename_key/move_key ops — see the charly-side migration entry").
// `builder:` defs get the same treatment (builder.<name>.install_template →
// builder.<name>.phase.install.container).
//
// `install_template` is otherwise unique per def: the OTHER `install_template`
// uses in the schema (distro `local_pkg:`.install_template and distro
// `bootloader:`.install_template, both RETAINED) live under DIFFERENT parent keys,
// so this hook only ever touches the direct `install_template:` child of a
// `format:`/`builder:` def's OWN mapping — never descending to look for a second,
// unrelated `install_template:` key.
func installTemplateToPhases(doc *yaml.Node) bool {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	return moveInstallTemplateRec(root)
}

// moveInstallTemplateRec walks the whole document tree (format:/builder: vocab
// blocks are ordinarily top-level project-config mappings, but member nesting is a
// general document capability, so this recurses defensively) looking for any
// mapping carrying a direct `format:` or `builder:` key whose value is a mapping of
// defs. For each def found, the def's direct `install_template:` child is moved into
// `phase.install.container`, then the outer walk continues into every child (so a
// nested vocab elsewhere in the tree is still found).
func moveInstallTemplateRec(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if moveInstallTemplateRec(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if (key.Value == "format" || key.Value == "builder") && val.Kind == yaml.MappingNode {
				if moveVocabDefsInstallTemplate(val) {
					changed = true
				}
			}
			if moveInstallTemplateRec(val) {
				changed = true
			}
		}
	}
	return changed
}

// moveVocabDefsInstallTemplate moves a `format:`/`builder:` mapping's def
// install_template(s) into each def's `phase.install.container` cell, handling both
// authored shapes the removed field appears in:
//
//   - a defs-MAP (`format: {apk: {...}}` / `builder: {cargo: {...}}`): children are
//     NAMED defs, each a mapping, whose direct `install_template:` is moved;
//   - a SINGLE def (`cargo: {builder: {detect_file: ..., install_template: ...}}`):
//     the mapping IS one def, its fields direct children, and a direct
//     `install_template:` is moved.
//
// The discriminator is the direct-child key set: a single def carries its own
// `install_template:` directly; a defs-map only nests it under named defs. The
// single-def form is the per-entity builder vocabulary shape (`<entity>:
// {builder: {<def fields>}}`), which the unified loader routes into the `builder`
// plugin-kind.
func moveVocabDefsInstallTemplate(defs *yaml.Node) bool {
	if defs.Kind != yaml.MappingNode {
		return false
	}
	if childMappingIndex(defs, "install_template") >= 0 {
		return moveInstallTemplateToPhase(defs)
	}
	changed := false
	for i := 0; i+1 < len(defs.Content); i += 2 {
		def := defs.Content[i+1]
		if def.Kind == yaml.MappingNode && moveInstallTemplateToPhase(def) {
			changed = true
		}
	}
	return changed
}

// moveInstallTemplateToPhase moves ONE def's direct `install_template:` scalar into
// its `phase.install.container` cell, comment-preserving:
//
//   - no `phase:` block → the pair becomes a fresh `phase: {install: {container: <v>}}`
//     block inserted at the old install_template position, with the retired key's
//     head comment riding onto the new container key;
//   - `phase:` present with an `install:` mapping → a `container:` key is inserted
//     into it (same comment transfer);
//   - `phase.install.container` already present (a partially-migrated config) → the
//     cell's value is replaced by the moved scalar.
//
// The original pair is spliced out first in every case (a def's `install_template:`
// key is unique, so the first match is the only one).
func moveInstallTemplateToPhase(def *yaml.Node) bool {
	for i := 0; i+1 < len(def.Content); i += 2 {
		key, val := def.Content[i], def.Content[i+1]
		if key.Value != "install_template" || val.Kind != yaml.ScalarNode {
			continue
		}
		keyComment := key.HeadComment
		// Splice the pair out first so `val` is free to be re-parented.
		def.Content = append(def.Content[:i], def.Content[i+2:]...)

		containerKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "container"}
		containerKey.HeadComment = keyComment
		installVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{containerKey, val}}

		phaseIdx := childMappingIndex(def, "phase")
		if phaseIdx < 0 {
			phaseKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "phase"}
			installKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "install"}
			phaseVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{installKey, installVal}}
			// Insert the fresh phase block at the old install_template position.
			def.Content = append(def.Content[:i], append([]*yaml.Node{phaseKey, phaseVal}, def.Content[i:]...)...)
			return true
		}

		// phase exists: find/create install under it.
		phaseVal := def.Content[phaseIdx+1]
		if instIdx := childMappingIndex(phaseVal, "install"); instIdx >= 0 {
			instVal := phaseVal.Content[instIdx+1]
			// install exists: insert/replace container.
			if cIdx := childMappingIndex(instVal, "container"); cIdx >= 0 {
				instVal.Content[cIdx+1] = val
				return true
			}
			instVal.Content = append(instVal.Content, containerKey, val)
			return true
		}
		installKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "install"}
		phaseVal.Content = append(phaseVal.Content, installKey, installVal)
		return true
	}
	return false
}

// childMappingIndex returns the index of the KEY node for the named key in a
// mapping, or -1 when absent.
func childMappingIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}
