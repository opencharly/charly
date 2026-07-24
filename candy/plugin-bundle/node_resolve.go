package bundle

import (
	"strings"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// node_resolve.go — the W4 pure-helpers relocation: classifyNodeTarget
// (deploykit.ClassifyNodeTarget — shared with charly core's ancestor-executor
// derivation, R3) and resolveVmEntity are pure functions of (node, path) with
// no LoadUnified / executor / host-only dependency, so they run plugin-side
// in the walk (walk.go's dispatchOne) instead of behind the coarse
// "deploy-node-dispatch" HostBuild seam. Their results (target/vm_entity)
// ride the spec.DeployNodeDispatchRequest across the wire; the host-side
// dispatch trusts them as sent rather than recomputing.

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
