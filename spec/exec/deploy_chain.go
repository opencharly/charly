package exec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/spec/spec"
)

// deploy_chain.go — the executor-CHAIN constructors: pure fabric functions that, given a
// deployment node (spec.FleetNode) or a dotted deployment path, build the spec/exec DeployExecutor
// chain (ShellExecutor / SSHExecutor / NestedExecutor) that reaches the leaf. They belong beside the
// executors they construct (a floor primitive, #55 K4 — relocated from sdk/deploykit, which now
// RE-EXPORTS them for its plugin-side callers; kind-blind, no registry/loader/host-state coupling).
//
// Pre-cutover (2026-04), four call sites built executor chains (or partial
// chains) independently:
//   - charly fleet add  → deriveChildExecutorForPath in deploy_add_cmd.go
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
// deployment tree from ResolveMergedTreeViaExecutor) and returns the leaf
// FleetNode + a composed DeployExecutor chain that reaches it from
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
//	target: kubernetes      → error (kubernetes manifests are leaves; not
//	                          traversable as exec targets).
//
// Returns clear errors with available-name hints when a segment fails
// to resolve.
func ResolveDeployChain(roots map[string]spec.FleetNode, dotted string, root spec.DeployExecutor) (*spec.FleetNode, spec.DeployExecutor, error) {
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
		next, err := AppendHopForFlatPath(chain, current, flatPath, seg)
		if err != nil {
			return nil, nil, fmt.Errorf("entering %q: %w", strings.Join(parts[:i+2], "."), err)
		}
		chain = next
	}

	return current, chain, nil
}

// appendHopForNode is the root-segment variant — uses `name` for the
// container target (no flattening needed at the root).
func appendHopForNode(chain spec.DeployExecutor, node *spec.FleetNode, name string) (spec.DeployExecutor, error) {
	return AppendHopForFlatPath(chain, node, name, name)
}

// chainEntersVMGuest reports whether the executor chain so far terminates in a
// hop INTO a VM guest over SSH — either a plain SSHExecutor (the VM is the
// chain root) or a NestedExecutor whose last jump is JumpSSH (a VM nested in a
// parent). A pod hop stacked on such a chain lands inside the guest, where the
// pod was deployed standalone as "charly-<childKey>" (plugin-deploy-vm's PostApply), not
// under the host-side flatPath name.
func chainEntersVMGuest(chain spec.DeployExecutor) bool {
	switch c := chain.(type) {
	case *SSHExecutor:
		return true
	case *NestedExecutor:
		return c.Jump.Kind == JumpSSH
	}
	return false
}

// AppendHopForFlatPath stacks one executor hop so commands land inside
// `node`'s substrate. flatPath is the dotted path with dots replaced by
// underscores — the host-side container name suffix; leaf is the final path
// segment (the node's own key), used for a pod deployed STANDALONE inside a VM
// guest (which has no parent-path concept — see the pod case).
func AppendHopForFlatPath(chain spec.DeployExecutor, node *spec.FleetNode, flatPath, leaf string) (spec.DeployExecutor, error) {
	// The venue-hop is selected by the loader-stamped descent-descriptor's generic
	// TRANSPORT (the descent de-type, Cutover H) — never by switching on the
	// substrate kind word. A node reaching here without a descriptor was not folded
	// through the substrate loader (a bug at its fold site, surfaced loudly — R4).
	if node.Descent == nil {
		return nil, fmt.Errorf("node %q (target %q) has no descent descriptor — not folded through the substrate loader", flatPath, node.Target)
	}
	switch node.Descent.Transport {
	case "none":
		// local + android nodes share the parent venue: a local node's
		// venue IS the chain root (a ShellExecutor for host:local, or an
		// SSHExecutor for host:<remote> — selected by RootExecutorForDeployNode
		// and passed in as `root`), and an android device is reached via adb
		// over the parent pod's published port. No new hop.
		return chain, nil

	case "container-exec":
		// Container name convention: "charly-<flat-path>" — matches quadlet
		// emission, which deploys a HOST-side nested pod as "charly-<seg1>_<seg2>".
		// EXCEPTION — a pod nested inside a VM guest: it is deployed by the
		// guest's OWN `charly fleet from-box <ref> <childKey>`
		// (plugin-deploy-vm's PostApply), so the in-guest container is "charly-<childKey>"
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

	case "ssh":
		// VM SSH alias keys off the per-deploy DOMAIN IDENTITY
		// (charly-<VmDomainIdentity(flatPath)>), NOT node.From (the shared kind:vm
		// entity) — `charly vm create <entity> --domain <deploy>` writes the stanza
		// under charly-<deploy> (P33), so sibling beds sharing one entity get
		// distinct, non-colliding aliases matching what vm create actually wrote.
		ssh := SSHParamsForVm(spec.VmDomainIdentity(flatPath))
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

	case "reject":
		return nil, fmt.Errorf("kubernetes targets cannot be reached via the deploy chain (use kubectl)")
	}
	return nil, fmt.Errorf("unknown descent transport %q on node %q", node.Descent.Transport, flatPath)
}

// RootExecutorForDeployNode selects the ROOT DeployExecutor for a
// `target: local` deployment node from its `host:` field — the single source
// of truth for "where does a local deploy's work run?", shared by
// `charly fleet add` (the local deploy target.Add) and `charly check live`
// (runLocalCheck) so neither re-implements the selection (R3):
//
//	host: ""  / "local"        → ShellExecutor{} (this machine, direct shell)
//	host: "<user>@<machine>"   → &SSHExecutor{User, Host, Port, …}
//	host: "<machine>" + user:  → SSHExecutor with User from node.User
//
// It does NOT handle the nested-inside-a-parent case (opts.ParentExec); that
// stays in the local deploy target.Add because it's deploy-execution-specific.
// Returns ShellExecutor{} for a nil node.
func RootExecutorForDeployNode(node *spec.FleetNode) (spec.DeployExecutor, error) {
	if node == nil {
		return ShellExecutor{}, nil
	}
	hostField := strings.TrimSpace(node.Host)
	if hostField == "" || hostField == "local" {
		return ShellExecutor{}, nil
	}
	sshTarget, err := spec.ParseSSHTarget(hostField)
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

// VmChildExecutor wraps parentExec with an SSH jump into the VM
// represented by this node. At the root (parentExec == nil or
// ShellExecutor), the child gets a plain SSHExecutor — no
// nesting overhead for the common case of a VM on localhost.
//
// The SSH alias keys off the per-deploy DOMAIN IDENTITY
// (charly-<VmDomainIdentity(deployName)>), NOT node.From (the shared kind:vm
// entity) — `charly vm create <entity> --domain <deploy>` writes the managed
// stanza under `charly-<deploy>` (P33). Several beds may share one entity via
// `from:`, so an entity-keyed alias collides them on ONE stanza (the R10 defect
// where sibling beds both derived `charly-eval-vm`); keying by the deploy makes
// the alias distinct per bed and matches the stanza vm create actually wrote. A
// direct create (deploy == entity) resolves to `charly-<entity>` naturally, and
// VmDomainIdentity flattens a dotted member path consistently with the domain the
// lifecycle named (fleet_members.go's `vmDomainIdentity(memberKey)`).
func VmChildExecutor(parentExec spec.DeployExecutor, deployName string) (spec.DeployExecutor, error) {
	ssh := SSHParamsForVm(spec.VmDomainIdentity(deployName))
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

// SSHParamsForVm returns an SSHExecutor pointing at the VM's managed
// ssh-config alias (charly-<domainID>) — the caller passes the per-deploy
// DOMAIN IDENTITY (VmDomainIdentity of the deploy), NOT the shared kind:vm
// entity (P33). All connection details — User, Port, IdentityFile, host-key
// checking — live in the Host stanza that `charly vm create` / `charly fleet
// add` published into ~/.config/charly/ssh_config; ssh(1) reads them from there.
// Our SSHExecutor needs only the alias as Host.
func SSHParamsForVm(domainID string) *SSHExecutor {
	return &SSHExecutor{
		Host:           spec.VmSshAlias(domainID),
		ConnectTimeout: 10,
	}
}

// didYouMeanDeploy returns a "; available deployments: a, b, c" hint
// listing top-level deploy names sorted alphabetically. Empty when no
// candidates exist.
func didYouMeanDeploy(missed string, roots map[string]spec.FleetNode) string {
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
func didYouMeanNestedChild(missed string, nested map[string]*spec.FleetNode) string {
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
