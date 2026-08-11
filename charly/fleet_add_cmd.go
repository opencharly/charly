package main

// fleet_add_cmd.go — the host-side M residue of `charly fleet add`/`del` after the K4-C SHAPE-2
// cutover. The CLI grammar + tree walk + per-node compile live in the command:fleet plugin; the
// DEL resolution moved to candy/plugin-fleet/del_resolve.go (K-wave 2 cone R2 bank C — the
// deployDelCmd struct, resolveDelNode, podDeploymentArtifactExists, and the "deploy-del-resolve"
// HostBuild seam are all DELETED). What stays here is floor-M host-only machinery a plugin (a
// separate module) cannot own:
//
//   - deriveChildExecutorForPath — the ancestor executor HOP derivation (registry-coupled;
//     deployTraitDescent needs the providerRegistry). Reached by the resolve-target-add seam's
//     reconstructParentExec + fleet_members.go + unified_targets.go.
//   - loadConfigForDeploy — BUILD-SHARED (LoadConfig → LoadUnified + the host-fs distro probes),
//     reached by the resolve-target-add seam AND by build_overlay.go's hostBuildOverlay.

import (
	"fmt"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// deriveChildExecutorForPath builds the child executor for a nested node: the flattened container
// name (dotted path) for a container target, vmChildExecutor for an ssh child, the parent for a
// none-transport child. Registry-coupled (deployTraitDescent), so it stays host-side; the plugin's
// walk only ever holds paths + nodes (a live DeployExecutor never crosses the wire), re-ran per
// ancestor by the resolve-target-add seam's reconstructParentExec.
func deriveChildExecutorForPath(path string, node *spec.FleetNode, parentExec spec.DeployExecutor) (spec.DeployExecutor, error) {
	if node == nil || !node.HasChildren() {
		return parentExec, nil
	}
	switch deployTraitDescent(spec.ClassifyNodeTarget(node, path)).Transport {
	case "none":
		if parentExec != nil {
			return parentExec, nil
		}
		return specexec.ShellExecutor{}, nil
	case "container-exec":
		// The pod lifecycle creates `charly-<flat-path>` — the nested executor must target that
		// exact name (omitting the prefix made a nested-child deploy exec a nonexistent bare-named
		// container, exit 125).
		name := "charly-" + specexec.NestedContainerName(path)
		engineJump := specexec.JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = specexec.JumpDockerExec
		}
		if parentExec == nil {
			parentExec = specexec.ShellExecutor{}
		}
		return &specexec.NestedExecutor{Parent: parentExec, Jump: specexec.NestedJump{Kind: engineJump, Target: name}}, nil
	case "ssh":
		return specexec.VmChildExecutor(parentExec, path)
	case "reject":
		return nil, fmt.Errorf("kubernetes targets cannot have children")
	}
	return parentExec, nil
}

// loadConfigForDeploy loads charly.yml + the embedded build vocabulary for the current project
// directory. Runs RegisterBuildVocabulary as a side effect since the candy scanner needs it.
func loadConfigForDeploy(dir string) (*spec.Config, *spec.DistroConfig, *spec.BuilderConfig, error) {
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
