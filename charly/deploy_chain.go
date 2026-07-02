package main

import (
	"fmt"
	"sort"
	"strings"
)

// deploy_chain.go — single source of truth for "given a dotted deployment
// path, what DeployExecutor chain reaches the leaf?".
//
// Pre-cutover (2026-04), four call sites built executor chains (or partial
// chains) independently:
//   - charly bundle add  → deriveChildExecutorForPath in deploy_add_cmd.go
//   - charly check live <name> → ad-hoc executor construction in check_cmd.go
//   - charly check live parent.child → resolveNestedNode + a *flat* VmTestExecutor
//                            (silent single-hop bug — leaf tests ran on the
//                            parent VM via SSH instead of inside the leaf pod)
//   - charly check     → hardcoded ContainerExecutor{ContainerName: "charly-"+pod}
//                      (single-hop only; could not reach pod-in-vm)
//
// Post-cutover, every call site routes through ResolveDeployChain. The
// function walks the deployment tree segment by segment and stacks one
// NestedExecutor hop per segment that needs a substrate change. Result:
// arbitrary-depth chains (host → ssh-vm → podman-exec-pod → podman-exec-
// nested-pod) work uniformly across deploy, test, and harness.

// ResolveDeployChain walks `dotted` through `roots` (typically the merged
// deployment tree from resolveTreeRoot) and returns the leaf
// BundleNode + a composed DeployExecutor chain that reaches it from
// `root`.
//
// `root` is typically &ShellExecutor{} (the operator's host, or
// the harness-sandbox context the harness loop runs in). Pass nil to
// substitute ShellExecutor.
//
// For each path segment, a single hop is added based on the node's
// target classification:
//
//	target: pod / container → NestedExecutor with JumpPodmanExec /
//	                          JumpDockerExec into "charly-<flat-path>".
//	                          Container name flattens dot-separated
//	                          paths to underscore-separated to remain
//	                          a legal podman container name.
//	target: vm              → plain SSHExecutor when the parent chain
//	                          is local (no wrapper overhead), otherwise
//	                          NestedExecutor with JumpSSH on top.
//	target: host            → no hop (host nodes share the parent
//	                          venue).
//	target: k8s             → error (k8s manifests are leaves; not
//	                          traversable as exec targets).
//
// Returns clear errors with available-name hints when a segment fails
// to resolve.
func ResolveDeployChain(roots map[string]BundleNode, dotted string, root DeployExecutor) (*BundleNode, DeployExecutor, error) {
	if dotted == "" {
		return nil, nil, fmt.Errorf("ResolveDeployChain: empty path")
	}
	if root == nil {
		root = ShellExecutor{}
	}
	parts := strings.Split(dotted, ".")

	// Resolve the root segment.
	rootEntry, ok := roots[parts[0]]
	if !ok {
		return nil, nil, fmt.Errorf("deployment %q not found%s", parts[0], didYouMeanDeploy(parts[0], roots))
	}

	chain := root
	current := &rootEntry

	// Hop into the root segment's substrate.
	next, err := appendHopForNode(chain, current, parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("entering %q: %w", parts[0], err)
	}
	chain = next

	// Walk remaining segments, stacking one hop per segment.
	for i, seg := range parts[1:] {
		traversed := strings.Join(parts[:i+1], ".")
		if len(current.Children) == 0 {
			return nil, nil, fmt.Errorf("path %q: %q has no nested children", dotted, traversed)
		}
		child, ok := current.Children[seg]
		if !ok || child == nil {
			return nil, nil, fmt.Errorf("path %q: nested child %q not found under %q%s",
				dotted, seg, traversed, didYouMeanNestedChild(seg, current.Children))
		}
		current = child
		// Container names flatten the FULL path so far (parts[:i+2]); seg is the
		// leaf segment, used for a pod deployed standalone inside a VM guest.
		flatPath := strings.Join(parts[:i+2], "_")
		next, err := appendHopForFlatPath(chain, current, flatPath, seg)
		if err != nil {
			return nil, nil, fmt.Errorf("entering %q: %w", strings.Join(parts[:i+2], "."), err)
		}
		chain = next
	}

	return current, chain, nil
}

// appendHopForNode is the root-segment variant — uses `name` for the
// container target (no flattening needed at the root).
func appendHopForNode(chain DeployExecutor, node *BundleNode, name string) (DeployExecutor, error) {
	return appendHopForFlatPath(chain, node, name, name)
}

// chainEntersVMGuest reports whether the executor chain so far terminates in a
// hop INTO a VM guest over SSH — either a plain SSHExecutor (the VM is the
// chain root) or a NestedExecutor whose last jump is JumpSSH (a VM nested in a
// parent). A pod hop stacked on such a chain lands inside the guest, where the
// pod was deployed standalone as "charly-<childKey>" (deployNestedPodsInGuest), not
// under the host-side flatPath name.
func chainEntersVMGuest(chain DeployExecutor) bool {
	switch c := chain.(type) {
	case *SSHExecutor:
		return true
	case *NestedExecutor:
		return c.Jump.Kind == JumpSSH
	}
	return false
}

// appendHopForFlatPath stacks one executor hop so commands land inside
// `node`'s substrate. flatPath is the dotted path with dots replaced by
// underscores — the host-side container name suffix; leaf is the final path
// segment (the node's own key), used for a pod deployed STANDALONE inside a VM
// guest (which has no parent-path concept — see the pod case).
func appendHopForFlatPath(chain DeployExecutor, node *BundleNode, flatPath, leaf string) (DeployExecutor, error) {
	switch classifyTarget(node) {
	case "local", "android":
		// local + android nodes share the parent venue: a local node's
		// venue IS the chain root (a ShellExecutor for host:local, or an
		// SSHExecutor for host:<remote> — selected by rootExecutorForDeployNode
		// and passed in as `root`), and an android device is reached via adb
		// over the parent pod's published port. No new hop.
		return chain, nil

	case "pod":
		// Container name convention: "charly-<flat-path>" — matches quadlet
		// emission, which deploys a HOST-side nested pod as "charly-<seg1>_<seg2>".
		// EXCEPTION — a pod nested inside a VM guest: it is deployed by the
		// guest's OWN `charly bundle from-box <ref> <childKey>`
		// (deployNestedPodsInGuest), so the in-guest container is "charly-<childKey>"
		// (the leaf). The guest never sees the host-side bed/VM-entity prefix, so
		// once the chain has crossed into a VM guest the podman-exec hop must
		// target the leaf name — otherwise it exec's a container that doesn't
		// exist (the silent failure the nested-pod-in-VM check hit: probes ran
		// against "charly-<bed>_<child>" which the guest never created).
		podName := flatPath
		if chainEntersVMGuest(chain) {
			podName = leaf
		}
		name := "charly-" + podName
		engineJump := JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = JumpDockerExec
		}
		return &NestedExecutor{
			Parent: chain,
			Jump:   NestedJump{Kind: engineJump, Target: name},
		}, nil

	case "vm":
		// VM SSH alias keys off node.From (the kind:vm entity name) which
		// matches the stanza written by `charly vm create`. Falling back to
		// flatPath (the deploy bed name) would produce an charly-<bed> alias
		// for which no stanza exists.
		vmName := node.From
		if vmName == "" {
			vmName = flatPath
		}
		ssh := sshParamsForVm(vmName)
		// If the parent chain is just ShellExecutor, return a
		// plain SSHExecutor — no NestedExecutor wrapper needed.
		if _, isLocal := chain.(ShellExecutor); isLocal {
			return ssh, nil
		}
		// Nested VM (inside a container or another VM): stack a JumpSSH
		// using the VM's managed ssh-config alias as the target.
		return &NestedExecutor{
			Parent: chain,
			Jump: NestedJump{
				Kind:   JumpSSH,
				Target: ssh.Host,
			},
		}, nil

	case "k8s":
		return nil, fmt.Errorf("k8s targets cannot be reached via the deploy chain (use kubectl)")
	}
	return nil, fmt.Errorf("unknown target %q on node", classifyTarget(node))
}

// rootExecutorForDeployNode selects the ROOT DeployExecutor for a
// `target: local` deployment node from its `host:` field — the single source
// of truth for "where does a local deploy's work run?", shared by
// `charly bundle add` (the local deploy target.Add) and `charly check live`
// (runLocalCheck) so neither re-implements the selection (R3):
//
//	host: ""  / "local"        → ShellExecutor{} (this machine, direct shell)
//	host: "<user>@<machine>"   → &SSHExecutor{User, Host, Port, …}
//	host: "<machine>" + user:  → SSHExecutor with User from node.User
//
// It does NOT handle the nested-inside-a-parent case (opts.ParentExec); that
// stays in the local deploy target.Add because it's deploy-execution-specific.
// Returns ShellExecutor{} for a nil node.
func rootExecutorForDeployNode(node *BundleNode) (DeployExecutor, error) {
	if node == nil {
		return ShellExecutor{}, nil
	}
	hostField := strings.TrimSpace(node.Host)
	if hostField == "" || hostField == "local" {
		return ShellExecutor{}, nil
	}
	sshTarget, err := ParseSSHTarget(hostField)
	if err != nil {
		return nil, fmt.Errorf("invalid host %q: %w", hostField, err)
	}
	user := ""
	if strings.Contains(hostField, "@") {
		user = sshTarget.User
	} else if node.User != "" {
		user = node.User
	}
	return &SSHExecutor{
		User:           user,
		Host:           sshTarget.Host,
		Port:           sshTarget.Port,
		Args:           append([]string(nil), node.SSHArgs...),
		ConnectTimeout: 10,
	}, nil
}

// ContainerChain returns a one-hop chain that exec's into a single named
// running container (`<engine> exec <name> bash`). Convenience for the
// simple `charly check live <name>` path where there is no nested dotted path to
// walk — equivalent to ResolveDeployChain on a single-segment dotted
// path that resolves to a pod node, but skips the tree lookup.
func ContainerChain(engine, containerName string) DeployExecutor {
	jumpKind := JumpPodmanExec
	if engine == "docker" {
		jumpKind = JumpDockerExec
	}
	return &NestedExecutor{
		Parent: ShellExecutor{},
		Jump:   NestedJump{Kind: jumpKind, Target: containerName},
	}
}

// ImageChain builds a one-hop chain that runs commands inside a freshly-
// spawned disposable container (`<engine> run --rm <imageRef> bash`).
// Used by `charly check box --section box` to replace the deleted
// ImageExecutor — same semantics, expressed via the unified chain
// primitive.
//
// engine defaults to "podman" when empty. imageRef is passed verbatim
// (full registry refs and short local names both work as long as
// `<engine> run --rm` accepts them).
func ImageChain(engine, imageRef string) DeployExecutor {
	jumpKind := JumpPodmanRun
	if engine == "docker" {
		jumpKind = JumpDockerRun
	}
	return &NestedExecutor{
		Parent: ShellExecutor{},
		Jump:   NestedJump{Kind: jumpKind, Target: imageRef},
	}
}

// didYouMeanDeploy returns a "; available deployments: a, b, c" hint
// listing top-level deploy names sorted alphabetically. Empty when no
// candidates exist.
func didYouMeanDeploy(missed string, roots map[string]BundleNode) string {
	_ = missed // reserved for future fuzzy-matching
	if len(roots) == 0 {
		return ""
	}
	names := make([]string, 0, len(roots))
	for k := range roots {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = append(names[:8], "…")
	}
	return "; available deployments: " + strings.Join(names, ", ")
}

// didYouMeanNestedChild renders a hint listing nested child keys under
// a given node. Empty when the parent has no nested children.
func didYouMeanNestedChild(missed string, nested map[string]*BundleNode) string {
	_ = missed
	if len(nested) == 0 {
		return ""
	}
	names := make([]string, 0, len(nested))
	for k := range nested {
		names = append(names, k)
	}
	sort.Strings(names)
	return "; available nested children: " + strings.Join(names, ", ")
}
