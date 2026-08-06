package fleet

// del_resolve.go — the `charly fleet del` target resolution, relocated from the deleted
// charly/fleet_add_cmd.go's deployDelCmd + the deleted charly/host_build_deploy_del_resolve.go
// "deploy-del-resolve" HostBuild seam (K-wave 2 cone R2 bank C). The plugin already threads the
// merged deploy tree plugin-side (resolveTreeViaLoader); resolveDelNode consumes it here instead
// of round-tripping through a host seam. The host's deploy-node-del-dispatch seam (the terminal
// ResolveTarget + Del step) stays registry-coupled.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// resolveDelNode resolves the FleetNode + canonical kind for a
// `charly fleet del` invocation. Precedence:
//   - literal "host" name → synthetic local node (legacy)
//   - "vm:<name>" prefix  → synthetic vm node (legacy ref-based del)
//   - charly.yml entry    → the merged node (canonical target)
//   - no entry, pod artifact present → synthetic pod node (ref-based pod del)
//   - no entry, nothing present      → "no such deployment" error
//
// The returned node always carries a non-empty Target so the host's ResolveTarget can
// dispatch. For a ref-based pod deploy with no charly.yml entry (e.g. the entry
// was removed while the deploy is still up) the node is synthesized — but ONLY
// when a real pod artifact exists (a quadlet unit, or a live container for a
// direct-mode deploy). A mistyped/unknown name has no artifact and is rejected
// loudly, instead of being silently synthesized into a pod del that tears down
// nothing and then fails with a misleading "unknown target pod".
func resolveDelNode(name string, tree map[string]spec.FleetNode) (*spec.FleetNode, string, error) {
	if name == "host" {
		return &spec.FleetNode{Target: "local"}, "local", nil
	}
	// Try the REAL tree resolution FIRST — "vm:"-prefix-aware via resolveDeployNodeByPath's own
	// spec.SplitVmAddress use (RCA #9). tree is threaded PLUGIN-SIDE (resolveTreeViaLoader); a
	// nil/empty tree falls through to the "vm:"-prefix / pod-artifact fallbacks below.
	if tree != nil {
		if node, ok := resolveDeployNodeByPath(tree, name); ok && node.Target != "" {
			n := *node
			return &n, n.Target, nil
		}
	}
	if _, isVm := spec.SplitVmAddress(name); isVm {
		// Fallback ONLY for a genuine tree-absence: a "vm:"-prefixed address with no matching
		// tree entry (the deploy was removed from charly.yml, or never had one). The synthetic
		// Target-only placeholder is all we can offer; the host dispatch's own name normalization
		// still targets the right domain identity regardless.
		return &spec.FleetNode{Target: "vm"}, "vm", nil
	}
	if podDeploymentArtifactExists(name) {
		return &spec.FleetNode{Target: "pod"}, "pod", nil
	}
	return nil, "", fmt.Errorf("no such deployment %q — run `charly fleet show` to see "+
		"deployments (a VM deploy is torn down as `charly fleet del vm:%s`)", name, name)
}

// resolveDeployNodeByPath walks the merged deploy tree by dotted path (root.child.child). Ported
// from the deleted charly/plugin_loader.go helper — a pure spec-native walk the plugin can run on
// the tree it already resolved.
func resolveDeployNodeByPath(tree map[string]spec.FleetNode, name string) (*spec.FleetNode, bool) {
	name, _ = spec.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

// podDeploymentArtifactExists reports whether a pod deploy named `name` has a persisted artifact on
// this host: a quadlet unit (`.container`/`.pod`, written by `charly config`/`charly start`) OR a
// live container (a direct-mode `engine.run=direct` deploy has no quadlet). It is the discriminator
// that lets a ref-based `charly fleet del <name>` with no charly.yml entry still tear a real pod
// down, while a mistyped name (no artifact) is rejected.
func podDeploymentArtifactExists(name string) bool {
	cn := specexec.NestedContainerName(name)
	if dir, err := spec.QuadletDir(); err == nil {
		for _, suffix := range []string{".container", ".pod"} {
			if _, err := os.Stat(filepath.Join(dir, "charly-"+cn+suffix)); err == nil {
				return true
			}
		}
	}
	return kit.ContainerExists("", "charly-"+cn)
}
