package main

// deploy_tree.go — the recursive tree walker for schema v2 deployments.
//
// Every deployment is a BundleNode that may carry `children:`.
// This file owns the walk-and-dispatch logic that turns the tree into
// a sequence of per-target Emit() calls with the correct ParentExec
// threaded through.
//
// Apply order is pre-order (parents first): the parent's environment
// must exist before its children can run inside it. Delete order is
// post-order (children first): children tear down while the parent
// venue is still alive to accept teardown commands.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// DeployTreePhase indicates which lifecycle phase the walker is in.
// Pre-order for add; post-order for delete.
type DeployTreePhase int

const (
	DeployTreePhaseAdd DeployTreePhase = iota
	DeployTreePhaseDel
)

// DeployTreeVisitor is invoked once per node in the walk. It receives
// the node's dotted path, the node itself, and the parent executor
// (nil at the root). The return value is the DeployExecutor that this
// node's CHILDREN should use as their parent. A host-target node
// returns the same executor it was given (candies applied in-place on
// the parent venue); a container or vm node returns a NestedExecutor
// that drills into the newly-created environment.
//
// Returning (nil, nil) for a node with children is an error — it
// means "cannot compute child executor", which the walker surfaces
// with the offending path.
type DeployTreeVisitor func(path string, node *BundleNode, parentExec DeployExecutor) (childExec DeployExecutor, err error)

// WalkDeploymentTree performs a pre-order walk rooted at the given
// node, calling visit on each node. Dotted-path accumulation is
// handled internally: the root's `rootPath` argument seeds the
// identifier; children are rendered as `<parent>.<childKey>`.
//
// Errors short-circuit: as soon as any visit call returns a non-nil
// error, the walk stops and that error propagates.
func WalkDeploymentTree(rootPath string, root *BundleNode, parentExec DeployExecutor, visit DeployTreeVisitor) error {
	if root == nil {
		return nil
	}
	thisExec, err := visit(rootPath, root, parentExec)
	if err != nil {
		return err
	}
	if !root.HasChildren() {
		return nil
	}
	for _, k := range sortedNestedKeys(root.Children) {
		child := root.Children[k]
		childPath := k
		if rootPath != "" {
			childPath = rootPath + "." + k
		}
		if err := WalkDeploymentTree(childPath, child, thisExec, visit); err != nil {
			return err
		}
	}
	return nil
}

// WalkDeploymentTreePostOrder is the post-order analogue used by
// BundleDelCmd. Children are visited before their parent, so a
// parent's venue can still accept the teardown commands for its
// children.
func WalkDeploymentTreePostOrder(rootPath string, root *BundleNode, parentExec DeployExecutor, visit DeployTreeVisitor) error {
	if root == nil {
		return nil
	}
	// For post-order we need this node's child-executor BEFORE we
	// recurse. The visitor is called twice: once in "dry" mode to
	// yield the child executor (but not execute side effects), and
	// once after children have torn down (to actually emit this
	// node's delete). To keep the interface simple, we use a single
	// visitor call but require it to be idempotent for teardown —
	// the caller can record the child executor up front from a
	// lightweight `deriveChildExecutor` call.
	thisExec, err := deriveChildExecutor(root, parentExec, rootPath)
	if err != nil {
		return err
	}
	if root.HasChildren() {
		for _, k := range sortedNestedKeys(root.Children) {
			child := root.Children[k]
			childPath := k
			if rootPath != "" {
				childPath = rootPath + "." + k
			}
			if err := WalkDeploymentTreePostOrder(childPath, child, thisExec, visit); err != nil {
				return err
			}
		}
	}
	_, err = visit(rootPath, root, parentExec)
	return err
}

// deriveChildExecutor computes the DeployExecutor that this node's
// children should use, given this node's target and the parent
// executor. Pure (no side effects) so it can be called from the
// post-order path before the node itself is dispatched.
//
// Semantics by target:
//
//	host:       children share the parent venue (host applies candies
//	            in-place). Child executor = parentExec or
//	            ShellExecutor at the root.
//	container:  wrap parent with NestedExecutor{JumpPodmanExec}.
//	vm:         wrap parent with NestedExecutor{JumpSSH} when parent
//	            is non-nil; otherwise the child executor is a plain
//	            SSHExecutor built from the VM's deploy state.
//	kubernetes: children are K8s manifests — not executed. Returns
//	            nil + error when non-empty Children.
//
// Returns (parentExec, nil) for nodes with no children — no
// composition needed.
func deriveChildExecutor(node *BundleNode, parentExec DeployExecutor, deployName string) (DeployExecutor, error) {
	if node == nil {
		return parentExec, nil
	}
	if !node.HasChildren() {
		return parentExec, nil
	}
	switch node.Target {
	case "local", "":
		// When Target is empty, fall back to "pod" (default for named
		// deploys). A local node with children → pass-through (children
		// use parentExec or localhost).
		if node.Target == "local" {
			if parentExec != nil {
				return parentExec, nil
			}
			return ShellExecutor{}, nil
		}
		return containerChildExecutor(node, parentExec)
	case "pod", "container":
		return containerChildExecutor(node, parentExec)
	case "android":
		// android shares the parent venue (device reached via adb). No hop.
		if parentExec != nil {
			return parentExec, nil
		}
		return ShellExecutor{}, nil
	case "vm":
		return vmChildExecutor(node, parentExec, deployName)
	case "k8s", "kubernetes":
		return nil, fmt.Errorf("target=k8s cannot have children (manifests are leaf artifacts)")
	default:
		return nil, fmt.Errorf("unknown target %q", node.Target)
	}
}

// containerChildExecutor wraps parentExec with a podman-exec jump
// into the container spawned by this node. The container name
// follows the `charly` convention of matching the deploy key — callers
// that need a custom name can set node.Engine or pass via the deploy
// entry's naming.
func containerChildExecutor(node *BundleNode, parentExec DeployExecutor) (DeployExecutor, error) {
	name := containerNameForNode(node)
	if name == "" {
		return nil, fmt.Errorf("container node: cannot determine container name for nested dispatch")
	}
	engineJump := JumpPodmanExec
	if node.Engine == "docker" {
		engineJump = JumpDockerExec
	}
	if parentExec == nil {
		parentExec = ShellExecutor{}
	}
	return &NestedExecutor{
		Parent: parentExec,
		Jump:   NestedJump{Kind: engineJump, Target: name},
	}, nil
}

// vmChildExecutor wraps parentExec with an SSH jump into the VM
// represented by this node. At the root (parentExec == nil or
// ShellExecutor), the child gets a plain SSHExecutor — no
// nesting overhead for the common case of a VM on localhost.
//
// The SSH alias keys off node.From (the kind:vm entity name), NOT
// deployName (the bed name) — `charly vm create <vm>` writes the managed
// stanza for `charly-<vm>`. Falling back to deployName here would produce
// a `charly-<bed>` alias with no matching stanza (e.g., bed `arch-vm` →
// `charly-arch-vm`, but the stanza is `charly-arch`). deployName is kept for
// log messages where the deployment identity matters.
func vmChildExecutor(node *BundleNode, parentExec DeployExecutor, deployName string) (DeployExecutor, error) {
	vmName := node.From
	if vmName == "" {
		vmName = deployName // fallback for legacy nodes without `vm:` set
	}
	ssh := sshParamsForVm(vmName)
	// If parent is localhost-equivalent, use a direct SSHExecutor —
	// no need to hop through a trivial wrapper.
	if parentExec == nil {
		return ssh, nil
	}
	if _, isLocal := parentExec.(ShellExecutor); isLocal {
		return ssh, nil
	}
	// Nested VM (inside a container, or inside another VM): compose
	// using the same alias as the JumpSSH target — ssh-config supplies
	// User/Port/IdentityFile.
	return &NestedExecutor{
		Parent: parentExec,
		Jump: NestedJump{
			Kind:   JumpSSH,
			Target: ssh.Host,
		},
	}, nil
}

// containerNameForNode derives the container name for a node's
// container target. Today `charly bundle add <name>` uses the deploy key
// as the container name; we preserve that convention for the root
// level. For nested container children, the fully-qualified path is
// flattened with `_` to produce a unique podman-compatible name
// (e.g. `stack.web.db` → `stack_web_db`).
//
// Callers provide the path via node.pathHint when set; absent that,
// we fall back to parsing the node's fields. Because BundleNode
// doesn't carry its own key (the map above owns the key), we embed
// the dotted path into EmitOpts.Path upstream; deriveChildExecutor
// reads that when available.
func containerNameForNode(_ *BundleNode) string {
	// Placeholder: the real path is known only by the walker that
	// tracks it. When invoked from the walker's DeployTreeVisitor,
	// callers pass the name via the NestedJump.Target directly and
	// bypass this helper. Kept as a defensive default so standalone
	// unit tests can exercise the function.
	return ""
}

// sshParamsForVm returns an SSHExecutor pointing at the VM's managed
// ssh-config alias (charly-<deployName>). All connection details — User,
// Port, IdentityFile, host-key checking — live in the Host stanza
// that `charly vm create` / `charly bundle add` published into
// ~/.config/charly/ssh_config; ssh(1) reads them from there. Our
// SSHExecutor needs only the alias as Host.
func sshParamsForVm(deployName string) *SSHExecutor {
	return &SSHExecutor{
		Host:           VmSshAlias(deployName),
		ConnectTimeout: 10,
	}
}

// classifyTarget normalizes the Target field for dispatch. Empty Target
// falls back to "pod" (the default for named deploys); otherwise Target is
// the canonical source of truth (pod|vm|k8s|local|android — set from the
// node-form kind by bundleTargetForDisc; no name-prefix heuristic).
func classifyTarget(node *BundleNode) string {
	if node == nil || node.Target == "" {
		return "pod"
	}
	return node.Target
}

// stampBundleDescents stamps every deploy node's venue-hop descent-descriptor via
// the shared kit mapping (the descent de-type, Cutover H). Applied uniformly by the
// loader AFTER all structural kinds have folded into uf.Bundle — so every
// substrate/group/custom structural kind gets a descriptor without each plugin
// remembering to stamp — and the kernel's deploy chain (appendHopForFlatPath)
// descends by TRANSPORT, never by a kind-word switch. The word→transport MAPPING
// lives in sdk/kit, never the kernel; StampDescent recurses the nested/peer subtree.
func stampBundleDescents(uf *UnifiedFile) {
	if uf == nil {
		return
	}
	for name, node := range uf.Bundle {
		n := node
		kit.StampDescent(&n)
		uf.Bundle[name] = n
	}
}

// NestedContainerName computes the podman container name used when
// a container node is nested under a dotted path. Path segments are
// joined with underscores so the result is a legal podman name.
// Called by the walker when it knows the full dotted path.
func NestedContainerName(path string) string {
	return strings.ReplaceAll(path, ".", "_")
}

// resolveTreeRoot returns the DeploymentsSection's Images map from
// the merged UnifiedFile + local overlay, ready for dotted-path
// traversal. Handles the project charly.yml + local overlay merge
// the same way BundleAddCmd.Run does today.
func resolveTreeRoot(dir string) (map[string]BundleNode, error) {
	var projectDC *BundleConfig
	if uf, ok, err := LoadUnified(dir); err != nil {
		return nil, err
	} else if ok && uf != nil {
		projectDC = uf.ProjectBundleConfig()
	}
	localDC, _ := LoadBundleConfig()
	merged := MergeDeployConfigs(projectDC, localDC)
	if merged == nil || merged.Bundle == nil {
		return nil, nil
	}
	return merged.Bundle, nil
}

// Suppressor for imports only used in doc comments / future
// expansion. Keeps `go vet` quiet and documents the intent.
var _ = filepath.Join
var _ = os.Getenv
