package migrate

import "gopkg.in/yaml.v3"

// reshape_strip_deploy_shell.go — the deploy-scope `shell:` overlay field removal
// (validation-correctness batch, folded into the K5-B wave). The deploy-scope
// `shell:` field (sdk/schema/deploy.cue #Deploy.shell → []#DeployShellOverlay) had
// ZERO live Go consumer: its only would-be merge, MergeDeployShell (fed by
// shellOverlayToEntry), never had a production call site anywhere in this repo's
// history — a never-wired half-feature, not a working thing that regressed (full
// git -S archaeology confirmed this back to genesis). This hook removes the field
// from an authored document so a config carrying it still loads after the schema
// stops declaring it.
//
// This is a DECLARATIVE-OP-UNSAFE removal, for the SAME reason
// reshape_strip_candy_libvirt.go's `libvirt:` removal was: `shell:` is ALSO a live,
// completely different field on a CANDY entity (sdk/schema/candy.cue #Candy.shell /
// candymodel.cue #CandyModel.shell, the #Shell intrinsic-init + per-shell-override
// MAPPING every candy may author) and on other unrelated entities (#Box.shell, a VM
// SSH `shell:` scalar, …). A blanket `{op: "delete_key", key: "shell", scope: "any"}`
// would strip ALL of those too.
//
// The two `shell:` uses this hook must tell apart are structurally DISTINGUISHABLE
// by VALUE SHAPE alone, with no need to track which kind discriminator a node is in:
// the retired deploy-scope overlay is authored as a SEQUENCE (`shell: [...]`, a list
// of #DeployShellOverlay entries), while every surviving `shell:` use (candy/box
// #Shell, a VM ssh `shell:` scalar) is either a MAPPING or a scalar, never a bare
// sequence. So this hook strips ONLY a direct `shell:` key whose value is a
// yaml.SequenceNode, wherever it appears in the document tree — never a mapping- or
// scalar-valued `shell:` key — reusing deleteDirectChildKey
// (reshape_strip_candy_libvirt.go) for the actual splice (R3, one comment-preserving
// deletion primitive).
func stripDeployShellOverlay(doc *yaml.Node) bool {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	return stripDeployShellOverlayRec(root)
}

// stripDeployShellOverlayRec walks the whole document tree (a deploy-scope `shell:`
// overlay can appear on any substrate node — pod/vm/k8s/local/android — at any
// nesting depth, e.g. a nested/peer member). For each mapping node found, it checks
// whether THAT mapping directly carries a sequence-valued `shell:` key and removes
// only that one, then unconditionally recurses into every child value (never a key,
// which is always a scalar) — mirroring stripCandyLibvirtFieldRec's iteration shape,
// so the deletion (on THIS mapping's own Content) never invalidates the loop that
// finds it, since the loop below re-reads n.Content fresh via len() each iteration.
func stripDeployShellOverlayRec(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if stripDeployShellOverlayRec(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "shell" && n.Content[i+1].Kind == yaml.SequenceNode {
				if deleteDirectChildKey(n, "shell") {
					changed = true
				}
				break // "shell" is a unique key per mapping — nothing left to find here
			}
		}
		for _, c := range n.Content {
			if stripDeployShellOverlayRec(c) {
				changed = true
			}
		}
	}
	return changed
}
