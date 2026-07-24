package bundle

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// node_resolve.go — the W4 pure-helpers relocation: classifyNodeTarget
// (deploykit.ClassifyNodeTarget — shared with charly core's ancestor-executor
// derivation, R3), resolveVmEntity, resolveNodeOverlays, and
// resolveNodeTemplate are (almost entirely) pure functions of (node, path)
// with no executor dependency, so they run plugin-side in the walk
// (walk.go's dispatchOne) instead of behind the coarse "deploy-node-dispatch"
// HostBuild seam. Their results (target/vm_entity/ref/add_candy/tag/the
// resolved gate opts) ride the spec.DeployNodeDispatchRequest across the
// wire; the host-side dispatch trusts them as sent rather than recomputing.
// resolveNodeTemplate's ONE genuinely host-only piece — the LoadUnified-
// coupled kind:local template lookup — reaches back over the EXISTING
// generic "deploy-entity-resolve" seam's kind="local" case (R3: reuse a
// seam, don't invent one).

// emitOpts mirrors charly/bundle_add_cmd.go's deployAddCmd.emitOpts() — the
// CLI-flags-to-EmitOpts mapping — MINUS ParentExec/Path/ParentNode, which a
// live DeployExecutor can never cross the wire to carry: the host fills
// those in after reconstructing the ancestor executor chain host-side
// (runDeployNodeDispatch). ParentNode itself is dead code today (grep-
// confirmed: no reader anywhere in the tree) — never populated pre- or
// post-relocation, so its omission here changes nothing.
func (c *BundleAddCmd) emitOpts() deploykit.EmitOpts {
	return deploykit.EmitOpts{
		DryRun:               c.DryRun,
		FormatJSON:           c.Format == "json",
		AllowRepoChanges:     c.AllowRepoChanges,
		AllowRootTasks:       c.AllowRootTasks,
		WithServices:         c.WithServices,
		SkipIncompatible:     c.SkipIncompatible,
		AssumeYes:            c.AssumeYes,
		Verify:               c.Verify,
		Pull:                 c.Pull,
		BuilderImageOverride: c.BuilderImage,
	}
}

// resolveNodeOverlays computes the per-node emit opts (minus ParentExec/
// Path — see emitOpts above), ref string, add-candy list, and tag, applying
// the charly.yml entry's field overlays on top of the CLI flags. On the
// root this matches the pre-v2 behavior; on children the fields come from
// the child node (not c.Name's top-level entry). Mutates node.Version in
// place when an explicit --tag is given and the node had none, so
// downstream host-side resolvers that read node.Version (the k8s
// preresolver, the pod overlay build) pin the EXACT tag — node crosses the
// wire in the request, so this mutation is visible host-side too. Returns
// an error only when neither a <ref> nor a charly.yml entry resolves a ref.
func (c *BundleAddCmd) resolveNodeOverlays(path string, node *spec.BundleNode) (deploykit.EmitOpts, string, []string, string, error) {
	opts := c.emitOpts()

	refStr := c.Ref
	addCandies := append([]string(nil), c.AddCandy...)
	tag := c.Tag
	if node != nil {
		if node.Version != "" {
			tag = node.Version
		} else if tag != "" {
			node.Version = tag
		}
		if node.InstallOpts != nil {
			opts = deploykit.InstallOptsApplyTo(node.InstallOpts, opts)
		}
		if len(addCandies) == 0 && len(node.AddCandy) > 0 {
			addCandies = append([]string(nil), node.AddCandy...)
		}
	}
	if refStr == "" {
		if node == nil {
			return opts, "", addCandies, tag, fmt.Errorf("charly bundle add: no <ref> and charly.yml has no entry for %q", path)
		}
		switch {
		case node.Image != "":
			refStr = node.Image
		default:
			refStr = deploykit.PathLeaf(path)
		}
	}
	return opts, refStr, addCandies, tag, nil
}

// resolveNodeTemplate merges a referenced kind:local template into
// addCandies and opts. Template fields merge BENEATH deployment-level
// overrides — the precedence is CLI > deployment > template — because
// InstallOptsApplyTo is fill-empty, so applying the template's opts after
// the deployment's leaves the deployment's values intact and only fills the
// gaps. The template lookup itself is LoadUnified-coupled, so it reaches
// back to the host over the "deploy-entity-resolve" seam (kind="local") —
// an EMPTY reply (no EntityJSON, no error) means "no template by that
// name" (a real error, distinct from a host-side load failure, which the
// seam surfaces as an actual error).
func resolveNodeTemplate(target, path string, node *spec.BundleNode, addCandies []string, opts deploykit.EmitOpts) ([]string, deploykit.EmitOpts, error) {
	if target != "local" || node == nil || node.From == "" {
		return addCandies, opts, nil
	}
	var er spec.DeployEntityResolveReply
	if err := hostDeploySeamJSON("deploy-entity-resolve", spec.DeployEntityResolveRequest{
		Kind: "local",
		Name: node.From,
	}, &er); err != nil {
		return addCandies, opts, fmt.Errorf("deployment %q: resolving kind:local template %q: %w", path, node.From, err)
	}
	if len(er.EntityJSON) == 0 {
		return addCandies, opts, fmt.Errorf("deployment %q: unknown kind:local template %q", path, node.From)
	}
	var tmpl spec.ResolvedLocal
	if err := json.Unmarshal(er.EntityJSON, &tmpl); err != nil {
		return addCandies, opts, fmt.Errorf("deployment %q: decoding kind:local template %q: %w", path, node.From, err)
	}
	// Prepend template candies; deployment add_candy are appended.
	merged := append([]string(nil), tmpl.Candy...)
	merged = append(merged, addCandies...)
	addCandies = merged
	// Fill install_opts gaps from the template.
	opts = deploykit.InstallOptsApplyTo(tmpl.InstallOpts, opts)
	return addCandies, opts, nil
}

// resolveVmEntity returns the kind:vm entity name this node targets, so the
// candy compiler builds plans against the GUEST's distro/format rather than
// the operator host's. node.From (a tree-backed vm: node's cross-ref) wins;
// otherwise a "vm:<name>"-prefixed deploy name (the CLI `charly bundle add
// vm:<name>` form) is checked. Empty means no vm entity applies — a valid
// resolved value, not a sentinel.
func resolveVmEntity(deployName string, node *spec.BundleNode) string {
	if node != nil && node.From != "" {
		return node.From
	}
	if strings.HasPrefix(deployName, "vm:") {
		if vmName, err := vmshared.VmNameFromDeployName(deployName); err == nil {
			return vmName
		}
	}
	return ""
}
