package main

// bundle_add_cmd.go — the host-side RESIDUE of `charly bundle add`/`charly bundle del` after the
// K4-C SHAPE-2 cutover. The CLI GRAMMAR + the whole deploy-tree WALK + the per-node COMPILE all
// live in the command:bundle plugin (candy/plugin-bundle) now — the plugin compiles the InstallPlans
// IN-PROC and drives the terminal add via the ONE thin HostBuild("resolve-target-add") seam
// (host_build_resolve_target_add.go). What STAYS here is the floor-M host-only machinery a plugin
// (a separate module) cannot own:
//
//   - deriveChildExecutorForPath — the ancestor executor HOP derivation (registry-coupled;
//     deployTraitDescent needs the providerRegistry). Reached by the resolve-target-add seam's
//     reconstructParentExec + bundle_members.go + unified_targets.go. DEPLOY-ONLY residue,
//     DELIBERATELY untouched by #55 step3 3-II (out of that cutover's scope, which relocates only
//     the pod-overlay BUILD envelope): its eventual plugin relocation is tracked under task #66.
//   - loadConfigForDeploy / detectHostContext / resolveDistroDef — BUILD-SHARED: LoadConfig →
//     LoadUnified (K1-loader-family-coupled) + the host-fs distro probes, reached by the
//     resolve-target-add seam + deploy_target_unified.go AND by build_overlay.go's hostBuildOverlay
//     (confirmed live, #55 step3 3-II) — kept here unchanged, serving both the deploy-del/add seams
//     AND the pod-overlay build prep.
//   - deployDelCmd + resolveDelNode + podDeploymentArtifactExists — the `charly bundle del` host
//     resolution the deploy-del-resolve seam drives. DEPLOY-ONLY residue, DELIBERATELY untouched by
//     #55 step3 3-II for the same reason as deriveChildExecutorForPath above (task #66).
//     deployDelArgv itself moved to spec.BundleDelArgv (R3 hoist, coneB P13 slice; the value
//     vocabulary relocated deploykit → spec in #55 Cone V) — it was byte-identically duplicated
//     here, in candy/plugin-bundle, and in candy/plugin-substrate.
//
// The former deployAddCmd struct + its dispatchNode/compileNodePlans/emitOpts/printPlans/
// compileHostContext methods (and the whole bundle_compile_seam.go + host_build_deploy_node_
// dispatch.go + deploy_ref.go) were DELETED in the shape-2 cutover — the plugin owns that logic now.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
)

// deployDelCmd resolves a `charly bundle del <name>` target node — the deploy-del-resolve
// host seam's ONE responsibility (resolveDelNode). The CLI GRAMMAR moved to the
// command:bundle plugin (candy/plugin-bundle); this struct is reconstructed from
// spec.DeployDelRequest by hostBuildDeployDelResolve, which populates only Name — the actual
// teardown EXECUTION lives in host_build_deploy_node_del_dispatch.go's hostBuildDeployNodeDelDispatch.
type deployDelCmd struct {
	Name string
}

// deriveChildExecutorForPath builds the child executor for a nested node:
// it supplies the current node's flattened container name (derived from the
// dotted path) for a container target, hops through vmChildExecutor for a vm
// child, and otherwise shares the parent executor.
//
// E/M/D VERIFIED (P13-KERNEL): the outer switch dispatches on
// deployTraitDescent(...).Transport — a small DECLARED closed vocabulary
// (none|container-exec|ssh|reject) every substrate provider maps itself onto, NOT a
// switch on the concrete kind word (vm/pod/local/k8s/android never appear here) — so
// this is legitimate D-data-driven dispatch, not an incomplete per-kind seam. Each case
// CONSTRUCTS a live spec.DeployExecutor from that transport — structurally the SAME
// shape as a Lifecycle:true substrate's already-sanctioned OpPrepareVenue->VenueDescriptor
// pattern, just for a NESTED hop instead of the root venue.
//
// K4-C WALK PORT (landed): the tree WALK runs plugin-side (candy/plugin-bundle/walk.go).
// This function's BODY is UNCHANGED and stays host-side — it is registry-coupled
// (deployTraitDescent needs the providerRegistry) — but its CALL SITE is the resolve-target-add
// seam's reconstructParentExec (host_build_resolve_target_add.go), which re-runs it once per
// ANCESTOR, reconstructing the WHOLE parentExec chain from the ancestor path/node lists the
// plugin's walk sends, rather than the caller passing a live parentExec through directly. A
// live DeployExecutor never crosses the wire — the plugin only ever holds paths + nodes.
func deriveChildExecutorForPath(path string, node *spec.BundleNode, parentExec spec.DeployExecutor) (spec.DeployExecutor, error) {
	if node == nil {
		return parentExec, nil
	}
	if !node.HasChildren() {
		return parentExec, nil
	}
	// P9: deploykit.ClassifyNodeTarget produces the child's substrate WORD (dispatch
	// classification, with the ref-based host/local PathLeaf fallback — W4 pure-helpers
	// relocation moved this pure function to sdk/deploykit, shared with candy/plugin-bundle's
	// own classification of the CURRENT node, R3); the executor HOP is then selected by that
	// word's DECLARED descent transport (the same closed nesting vocabulary AppendHopForFlatPath
	// consumes), never by a second switch on the kind word.
	switch deployTraitDescent(spec.ClassifyNodeTarget(node, path)).Transport {
	case "none":
		// local (host-rooted shell) + android (parent venue) share the parent venue: android
		// reaches the device via published ports / the endpoint; no executor hop.
		if parentExec != nil {
			return parentExec, nil
		}
		return specexec.ShellExecutor{}, nil
	case "container-exec":
		// The podman container `charly start`/the pod lifecycle creates is
		// `charly-<flat-path>` (containerName's `charly-` prefix), so the nested
		// executor MUST target that exact name — every other NestedContainerName
		// consumer (build_overlay.go, candy/plugin-adb/preresolve.go)
		// prepends `charly-`; omitting it here made a nested-child deploy exec into a
		// nonexistent bare-named container (exit 125 "no such container").
		name := "charly-" + specexec.NestedContainerName(path)
		engineJump := specexec.JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = specexec.JumpDockerExec
		}
		if parentExec == nil {
			parentExec = specexec.ShellExecutor{}
		}
		return &specexec.NestedExecutor{
			Parent: parentExec,
			Jump:   specexec.NestedJump{Kind: engineJump, Target: name},
		}, nil
	case "ssh":
		return deploykit.VmChildExecutor(parentExec, path)
	case "reject":
		return nil, fmt.Errorf("k8s targets cannot have children")
	}
	return parentExec, nil
}

// resolveDelNode resolves the BundleNode + canonical kind for a
// `charly bundle del` invocation. Precedence:
//   - literal "host" name → synthetic local node (legacy)
//   - "vm:<name>" prefix  → synthetic vm node (legacy ref-based del)
//   - charly.yml entry    → the merged node (canonical target)
//   - no entry, pod artifact present → synthetic pod node (ref-based pod del)
//   - no entry, nothing present      → "no such deployment" error
//
// The returned node always carries a non-empty Target so ResolveTarget can
// dispatch. For a ref-based pod deploy with no charly.yml entry (e.g. the entry
// was removed while the deploy is still up) the node is synthesized — but ONLY
// when a real pod artifact exists (a quadlet unit, or a live container for a
// direct-mode deploy). A mistyped/unknown name has no artifact and is rejected
// loudly, instead of being silently synthesized into a pod del that tears down
// nothing and then fails with a misleading "unknown target pod".
func (c *deployDelCmd) resolveDelNode(tree map[string]spec.BundleNode) (*spec.BundleNode, string, error) {
	if c.Name == "host" {
		return &spec.BundleNode{Target: "local"}, "local", nil
	}
	// RCA #9 (FINAL/K5 unit 6a, live-probe-caught): try the REAL tree resolution FIRST — now
	// "vm:"-prefix-aware via resolveDeployNodeByPath's own spec.SplitVmAddress use (RCA #8) — instead
	// of unconditionally short-circuiting to a synthetic Target-only placeholder for ANY
	// "vm:"-prefixed name. The old unconditional shortcut meant a "vm:"-prefixed del NEVER saw
	// the tree at all: it "resolved" successfully with a bare node (no From, no children), which
	// masked the SEPARATE connect-preamble bug RCA #8 fixed (resolveDeployNodeByPath used to also
	// fail to find the node) until dispatch itself failed. A real node also lets Del's teardown
	// hooks see the deploy's actual From/Children, which the synthetic placeholder never carried.
	// tree is threaded PLUGIN-SIDE by command:bundle (resolveTreeViaLoader) — the #55 Cone A Unit 3a
	// tree-threading that replaced this function's former host merged-tree read (cwd); a
	// nil/empty tree falls through to the "vm:"-prefix / pod-artifact fallbacks below, exactly as a
	// nil host-tree-read result did.
	if tree != nil {
		if node, ok := resolveDeployNodeByPath(tree, c.Name); ok && node.Target != "" {
			n := *node
			return &n, n.Target, nil
		}
	}
	if _, isVm := spec.SplitVmAddress(c.Name); isVm {
		// Fallback ONLY for a genuine tree-absence: a "vm:"-prefixed address with no matching
		// tree entry (the deploy was removed from charly.yml, or never had one — e.g. a bare
		// `charly vm create --domain` with no deploy entry). The synthetic Target-only
		// placeholder is all we can offer; hostBuildDeployNodeDelDispatch's own name
		// normalization (RCA #9) still targets the right domain identity regardless.
		return &spec.BundleNode{Target: "vm"}, "vm", nil
	}
	if podDeploymentArtifactExists(c.Name) {
		return &spec.BundleNode{Target: "pod"}, "pod", nil
	}
	return nil, "", fmt.Errorf("no such deployment %q — run `charly bundle list` to see "+
		"deployments (a VM deploy is torn down as `charly bundle del vm:%s`)", c.Name, c.Name)
}

// podDeploymentArtifactExists reports whether a pod deploy named `name` has a persisted artifact on
// this host: a quadlet unit (`.container`/`.pod`, written by `charly config`/`charly start`) OR a
// live container (a direct-mode `engine.run=direct` deploy has no quadlet). It is the discriminator
// that lets a ref-based `charly bundle del <name>` with no charly.yml entry still tear a real pod
// down, while a mistyped name (no artifact) is rejected.
func podDeploymentArtifactExists(name string) bool {
	cn := specexec.NestedContainerName(name)
	if dir, err := kit.QuadletDir(); err == nil {
		for _, suffix := range []string{".container", ".pod"} {
			if _, err := os.Stat(filepath.Join(dir, "charly-"+cn+suffix)); err == nil {
				return true
			}
		}
	}
	return containerExists("", "charly-"+cn)
}

// ---------------------------------------------------------------------------
// Host-context + config helpers (shared with build_overlay.go + the
// resolve-target-add / deploy_target_unified.go host paths).
// ---------------------------------------------------------------------------

// detectHostContext builds the HostContext struct used by the compiler
// for host-target deploys. Returns a zero-value struct for container
// deploys (the compiler ignores host-only fields there). Consumed by
// build_overlay.go (the pod-overlay build) host-side; the plugin computes its
// own twin (candy/plugin-bundle/dispatch.go detectHostContext) for the deploy compile.
func detectHostContext() deploykit.HostContext {
	hd, _ := hostenv.DetectHostDistro()
	glibc, _ := hostenv.DetectHostGlibc()
	if hd == nil {
		return deploykit.HostContext{}
	}
	return deploykit.HostContext{
		MachineVenue: true,
		Distro:       hd.PrimaryTag(),
		GlibcVersion: glibc,
	}
}

// resolveDistroDef returns the DistroDef for a given distro tag.
func resolveDistroDef(cfg *spec.DistroConfig, distroTag string) *spec.ResolvedDistro {
	if cfg == nil || distroTag == "" {
		return nil
	}
	return cfg.ResolveDistro([]string{distroTag})
}

// loadConfigForDeploy loads charly.yml + the embedded build vocabulary for the
// current project directory. Runs RegisterBuildVocabulary as a side effect since
// the candy scanner needs it.
func loadConfigForDeploy(dir string) (*Config, *spec.DistroConfig, *spec.BuilderConfig, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	RegisterBuildVocabulary(distroCfg)
	return cfg, distroCfg, builderCfg, nil
}
