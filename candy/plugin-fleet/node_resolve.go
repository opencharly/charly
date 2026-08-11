package fleet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// node_resolve.go — the W4 pure-helpers relocation: classifyNodeTarget
// (deploykit.ClassifyNodeTarget — shared with charly core's ancestor-executor
// derivation, R3), resolveVmEntity, resolveNodeOverlays, and
// resolveNodeTemplate are (almost entirely) pure functions of (node, path)
// with no executor dependency, so they run plugin-side in the walk
// (walk.go's dispatchOne). resolveNodeTemplate's ONE genuinely host-only piece — the
// kind:local template lookup — used to reach back over the "deploy-entity-
// resolve" seam's kind="local" case (a per-node LoadUnified call). K4 unit A
// (core-min wave 3) retired that case: the SAME data already rides the
// "resolved-project" envelope's Templates.Local RawBody map (proven,
// SHIPPED precedent — candy/plugin-box's validateLocalTemplates decodes that
// exact map with zero LoadUnified access), and the raw→ResolvedLocal
// PROJECTION is a pure decode candy/plugin-substrate's own OpResolve leg
// already performs (resolve.go's Local arm — copy fields + stash Raw, no
// host dependency of its own) — reached here via the SAME generic
// plugin↔plugin sdk.Executor.InvokeProvider(class:"kind", word:"local",
// OpResolve, …) leg candy/plugin-check/agent.go already uses for its own
// kind resolve. Net effect: zero new seam, and one fewer host round-trip
// class (the former "deploy-entity-resolve" HostBuild seam's "local" arm was already dead
// before this; the whole seam is deleted now, K-wave W3a A3-phase-2).

// emitOpts mirrors charly/fleet_add_cmd.go's deployAddCmd.emitOpts() — the
// CLI-flags-to-EmitOpts mapping — MINUS ParentExec/Path/ParentNode, which a
// live DeployExecutor can never cross the wire to carry: the host fills
// those in after reconstructing the ancestor executor chain host-side
// (the resolve-target-add seam's reconstructParentExec). ParentNode itself is dead code today (grep-
// confirmed: no reader anywhere in the tree) — never populated pre- or
// post-relocation, so its omission here changes nothing.
func (c *FleetAddCmd) emitOpts() deploykit.EmitOpts {
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
		DevLocalPkg:          c.DevLocalPkg,
	}
}

// resolveNodeOverlays computes the per-node emit opts (minus ParentExec/
// Path — see emitOpts above), ref string, add-candy list, and tag, applying
// the charly.yml entry's field overlays on top of the CLI flags. On the
// root this matches the pre-v2 behavior; on children the fields come from
// the child node (not c.Name's top-level entry). Mutates node.Version in
// place when an explicit --tag is given and the node had none, so
// downstream host-side resolvers that read node.Version (the kubernetes
// preresolver, the pod overlay build) pin the EXACT tag — node crosses the
// wire in the request, so this mutation is visible host-side too. Returns
// an error only when neither a <ref> nor a charly.yml entry resolves a ref.
func (c *FleetAddCmd) resolveNodeOverlays(path string, node *spec.FleetNode) (deploykit.EmitOpts, string, []string, string, error) {
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
			return opts, "", addCandies, tag, fmt.Errorf("charly fleet add: no <ref> and charly.yml has no entry for %q", path)
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
// gaps. The template lookup fetches the resolved-project envelope (the
// SAME InvokeProvider("build","project") seam fleet-compile already calls) and
// reads its Templates.Local RawBody map — the map is already
// namespace-qualified (`ns.tmpl`) by the host's fillNamespacedTemplates, so
// a plain map lookup on node.From covers both a bare and a qualified ref
// with no extra recursion. An absent key means "no template by that name"
// (a real error, distinct from an envelope-fetch failure, which surfaces as
// an actual error from lookupLocalTemplate).
func resolveNodeTemplate(target, path string, node *spec.FleetNode, addCandies []string, opts deploykit.EmitOpts) ([]string, deploykit.EmitOpts, error) {
	if target != "local" || node == nil || node.From == "" {
		return addCandies, opts, nil
	}
	tmpl, err := lookupLocalTemplate(node.From)
	if err != nil {
		return addCandies, opts, fmt.Errorf("deployment %q: resolving kind:local template %q: %w", path, node.From, err)
	}
	if tmpl == nil {
		return addCandies, opts, fmt.Errorf("deployment %q: unknown kind:local template %q", path, node.From)
	}
	// Prepend template candies; deployment add_candy are appended.
	merged := append([]string(nil), tmpl.Candy...)
	merged = append(merged, addCandies...)
	addCandies = merged
	// Fill install_opts gaps from the template.
	opts = deploykit.InstallOptsApplyTo(tmpl.InstallOpts, opts)
	return addCandies, opts, nil
}

// lookupLocalTemplate resolves a (possibly namespace-qualified) kind:local template name into its
// *spec.ResolvedLocal, entirely via existing generic seams — no LoadUnified, no new HostBuild kind:
//  1. fetch the resolved-project envelope (InvokeProvider("build","project") — the established seam
//     compileDeployPlans already calls) and read Templates.Local[name] (the RawBody the host's
//     fillNamespacedTemplates already qualifies by namespace prefix);
//  2. project that raw body into a *ResolvedLocal via the "local" kind provider's own OpResolve leg
//     (sdk.Executor.InvokeProvider(class:"kind", word:"local", …) — the SAME plugin↔plugin dispatch
//     candy/plugin-check/agent.go already uses for its own kind resolve), reusing
//     candy/plugin-substrate's existing resolve.go Local arm rather than re-deriving the projection
//     here (R3).
//
// Returns (nil, nil) when the envelope carries no template by that name (never present distinct
// from empty raw bytes, matched by len(raw)==0 as project_resolved_host.go's cp() never inserts an
// empty entry).
func lookupLocalTemplate(name string) (*spec.ResolvedLocal, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("no host reverse channel (command not compiled-in?)")
	}
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{})
	if err != nil {
		return nil, err
	}
	envJSON, err := cmdExec.InvokeProvider(cmdCtx, "build", "project", sdk.OpResolve, envReq, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, fmt.Errorf("fetch resolved-project envelope: %w", err)
	}
	var rp struct {
		Templates *spec.ProjectTemplates `json:"templates,omitempty"`
	}
	if err := json.Unmarshal(envJSON, &rp); err != nil {
		return nil, fmt.Errorf("decode resolved-project envelope: %w", err)
	}
	if rp.Templates == nil {
		return nil, nil
	}
	raw, ok := rp.Templates.Local[name]
	if !ok || len(raw) == 0 {
		return nil, nil
	}

	reqJSON, err := json.Marshal(spec.SubstrateTemplateResolveRequest{Local: &spec.LocalResolveInput{Local: raw}})
	if err != nil {
		return nil, err
	}
	resJSON, err := cmdExec.InvokeProvider(cmdCtx, "kind", "local", sdk.OpResolve, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, fmt.Errorf("resolving kind:local template %q via kind:local provider: %w", name, err)
	}
	var reply spec.LocalResolveReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return nil, fmt.Errorf("decode kind:local resolve reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// resolveVmEntity returns the kind:vm entity name this node targets, so the
// candy compiler builds plans against the GUEST's distro/format rather than
// the operator host's. node.From (a tree-backed vm: node's cross-ref) wins;
// otherwise a "vm:<name>"-prefixed deploy name (the CLI `charly fleet add
// vm:<name>` form) is checked. Empty means no vm entity applies — a valid
// resolved value, not a sentinel.
func resolveVmEntity(deployName string, node *spec.FleetNode) string {
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
