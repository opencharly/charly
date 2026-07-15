package main

// k3s_post.go — post-provision finalization for deploys whose candies
// included k3s-server. Runs after RetrieveCandyArtifacts has pulled the
// kubeconfig to ~/.cache/charly/clusters/<deploy>/kubeconfig.yaml.
//
// One thing happens here that the generic artifact-retricheck pipeline cannot:
// merge the retrieved kubeconfig into ~/.kube/config under a context named after
// the deploy, so `kubectl --context <deploy> …` and a `kube:` check addressing the
// deploy (cluster: ${DEPLOY_NAME}) both work immediately. The clientcmd merge — and
// therefore the client-go dependency — lives in the out-of-tree
// candy/plugin-kube provider (invokeKubePlugin), not in charly's core.
//
// Called from deploy_add_cmd.go and deploy_add_cmd_vm.go (both via
// deploy_add_shared.go) after the artifact retricheck step when the deploy's candy
// list contains "k3s-server". `charly bundle add` loads the deploy's composed
// external plugins first (loadProjectPlugins), so candy/plugin-kube — required by
// the k3s-server candy — is connected before this merge dispatches.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/opencharly/sdk/deploykit"
)

// k3sServerURLRe matches an `https://<host>:<port>` server URL in a kubeconfig.
var k3sServerURLRe = regexp.MustCompile(`(https?://)([^:/\s]+):(\d+)`)

// rewriteK3sServerToForward rewrites the retrieved kubeconfig's server URL, mapping
// the guest-local k3s API port to the host-forwarded port declared on the deploy's VM
// (network.port_forwards "<host>:<guest>"). No-op when the deploy has no matching VM
// forward — a bare-metal / already-host-reachable k3s needs no rewrite.
func rewriteK3sServerToForward(retrievedPath, entityRef, deployName string) error {
	forwards, err := deployVMForwards(entityRef, deployName)
	if err != nil {
		return err
	}
	if len(forwards) == 0 {
		return nil
	}
	guestToHost := map[string]string{}
	for _, pf := range forwards {
		if host, guest, ok := strings.Cut(pf, ":"); ok {
			guestToHost[strings.TrimSpace(guest)] = strings.TrimSpace(host)
		}
	}
	data, err := os.ReadFile(retrievedPath)
	if err != nil {
		return err
	}
	out := rewriteServerPorts(string(data), guestToHost)
	if out == string(data) {
		return nil
	}
	return os.WriteFile(retrievedPath, []byte(out), 0o600)
}

// rewriteServerPorts rewrites every `https://<host>:<guestPort>` in data to
// `https://127.0.0.1:<hostPort>` for each guest→host mapping (the QEMU user-mode
// forward lives on the host loopback). Pure; unit-tested.
func rewriteServerPorts(data string, guestToHost map[string]string) string {
	return k3sServerURLRe.ReplaceAllStringFunc(data, func(m string) string {
		p := k3sServerURLRe.FindStringSubmatch(m)
		if hport, ok := guestToHost[p[3]]; ok && hport != p[3] {
			return p[1] + "127.0.0.1:" + hport
		}
		return m
	})
}

// deployVMForwards returns the network.port_forwards of the VM the named deploy runs
// on (the deploy node's `from:` VM template), or nil when the deploy is not a VM.
// deployVMForwards resolves the RESOLVED "<host>:<guest>" forwards for the VM a deploy
// runs on. The two identities are DISTINCT and must not be conflated (the #65 bug):
//   - entityRef (the ENTITY-scoped artifact key, e.g. "vm:k3s-vm") resolves the VM SPEC —
//     one shared k3s cluster per VM; reliable via the "vm:" prefix, no foldMembers dependency.
//   - deployName (the real per-DEPLOY / domain identity, e.g. "check-k8s-deploy-cluster")
//     keys the VmState port-forward LEDGER: "vm:"+VmDomainIdentity(deployName) is the EXACT
//     key the orchestrator persisted under (vm_create_orchestrate.go domainID = the bed
//     runner's --domain = VmDomainIdentity(deployName)). Keying off entityRef instead was
//     the mismatch that silently dropped the allocation for every P33 bed (deploy != entity).
func deployVMForwards(entityRef, deployName string) ([]string, error) {
	uf, ok, err := LoadUnified(".")
	if err != nil || !ok || uf == nil {
		return nil, nil
	}
	// entityRef is either a "vm:<entity>" reference (the VM-deploy artifact key) or a
	// bundle key whose node carries `from: <vm entity>`. Resolve the VM entity either way.
	vmEntity := ""
	if e, cut := strings.CutPrefix(entityRef, "vm:"); cut {
		vmEntity = e
	} else if node := findBundleNodeByName(uf.Bundle, entityRef); node != nil {
		vmEntity = node.From
	}
	if vmEntity == "" {
		return nil, nil
	}
	vm, _ := resolveVmViaPlugin(uf.VM[vmEntity])
	if vm == nil || vm.Network == nil {
		return nil, nil
	}
	key := "vm:" + vmDomainIdentity(deployName)
	var alloc map[string]int
	if entry, ok := deploykit.LoadDeployConfigForRead("k3s kubeconfig forward").LookupKey(key); ok && entry.VmState != nil {
		alloc = entry.VmState.PortForwards
	}
	resolved, rerr := resolveDeployForwards(vm.Network.PortForwards, alloc)
	if rerr != nil {
		return nil, fmt.Errorf("deploy %q (vm_state key %q): %w", deployName, key, rerr)
	}
	return resolved, nil
}

// resolveDeployForwards maps authored network.port_forwards entries to concrete
// "<host>:<guest>" strings: an `auto:<guest>` entry resolves to its persisted
// auto-allocated host port, and a fixed "<host>:<guest>" passes through unchanged.
// An `auto:<guest>` with NO persisted allocation is a LOUD ERROR, never a silent drop:
// this runs only POST-vm-create (K3sPostProvision), where the allocation MUST exist, so a
// miss means a persist/read key mismatch — surfacing it here turns a confusing downstream
// `connection refused` into a diagnostic that names the unresolved entry (R1/R4). Pure;
// unit-tested (k3s_post_test.go).
func resolveDeployForwards(authored []string, alloc map[string]int) ([]string, error) {
	out := make([]string, 0, len(authored))
	for _, pf := range authored {
		host, guest, ok := strings.Cut(pf, ":")
		if !ok || guest == "" {
			continue
		}
		if host == "auto" {
			h, hit := alloc[guest]
			if !hit || h <= 0 {
				return nil, fmt.Errorf("auto port_forward %q has no persisted host-port allocation (the vm-create allocation must exist post-create)", pf)
			}
			out = append(out, fmt.Sprintf("%d:%s", h, guest))
			continue
		}
		out = append(out, pf)
	}
	return out, nil
}

// findBundleNodeByName locates a deploy node by key across the tree (top-level +
// nested children + peer members).
func findBundleNodeByName(bundle map[string]BundleNode, name string) *BundleNode {
	for k := range bundle {
		n := bundle[k]
		if k == name {
			return &n
		}
		if r := findBundleNodePtrByName(n.Children, name); r != nil {
			return r
		}
		if r := findBundleNodePtrByName(n.Members, name); r != nil {
			return r
		}
	}
	return nil
}

func findBundleNodePtrByName(m map[string]*BundleNode, name string) *BundleNode {
	for k, n := range m {
		if k == name {
			return n
		}
		if r := findBundleNodePtrByName(n.Children, name); r != nil {
			return r
		}
		if r := findBundleNodePtrByName(n.Members, name); r != nil {
			return r
		}
	}
	return nil
}

// sanitizeDeployName turns a deploy name like "vm:arch" or "stack.web.db"
// into a shell-safe, path-safe, kubeconfig-context-safe identifier.
// Colons and dots are replaced with dashes; that keeps the semantics
// identifiable ("vm:arch" → "vm-arch") without breaking file paths.
func sanitizeDeployName(s string) string {
	r := strings.NewReplacer(":", "-", ".", "-", "/", "-")
	return r.Replace(s)
}

// K3sPostProvision runs the post-provision steps for a k3s-server deploy.
// No-op when the retrieved kubeconfig path does not exist (e.g. because
// the candy did not actually include k3s-server, or the artifact
// retricheck was skipped by --dry-run).
// artifactKey is the ENTITY-scoped identity (the shared per-VM cluster cache dir +
// kubeconfig context — one k3s cluster per VM, reached by several beds); deployName is
// the real per-deploy (domain) identity the port-forward lookup keys off.
func K3sPostProvision(artifactKey, deployName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home: %w", err)
	}
	safe := sanitizeDeployName(artifactKey)
	retrieved := filepath.Join(home, ".cache", "charly", "clusters", safe, "kubeconfig.yaml")
	if _, err := os.Stat(retrieved); err != nil {
		// Not a k3s-server deploy, or retricheck was skipped. Nothing to do.
		return nil
	}

	// The retrieved kubeconfig carries k3s's GUEST-local server URL (127.0.0.1:6443);
	// the host reaches the in-guest API only through the VM's host:guest port-forward.
	// Rewrite the server to the host-forwarded port so `kubectl`/`kube:` checks work
	// host-side (without this, kubectl dials 127.0.0.1:6443 → connection refused). The
	// port-forward allocation is keyed by the DEPLOY identity; the entity (artifactKey)
	// resolves the VM spec.
	if err := rewriteK3sServerToForward(retrieved, artifactKey, deployName); err != nil {
		return fmt.Errorf("rewriting k3s kubeconfig server to the forwarded port: %w", err)
	}

	contextName := safe
	if err := mergeKubeconfig(retrieved, contextName); err != nil {
		return fmt.Errorf("merging kubeconfig into ~/.kube/config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "k3s cluster %q registered — kubectl --context=%s get nodes\n", artifactKey, contextName)
	return nil
}

// mergeKubeconfig merges the retrieved kubeconfig into the operator's
// ~/.kube/config under the chosen context name. The clientcmd merge itself — and
// therefore the client-go clientcmd dependency — lives in the
// out-of-tree candy/plugin-kube provider; this host-side wrapper just dispatches a
// synthetic `kube: merge-kubeconfig` #Op to it (invokeKubePlugin). Existing entries
// with the same context/cluster/user name are OVERWRITTEN by the plugin —
// deploy-add is the single source of truth for clusters it manages, so a rebuild
// cleanly picks up a fresh admin cert without stale entries.
func mergeKubeconfig(retrievedPath, contextName string) error {
	op := &Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "merge-kubeconfig", "kubeconfig": retrievedPath, "kube_context": contextName,
	}}
	if _, err := invokeKubePlugin(op); err != nil {
		return err
	}
	return nil
}

// deployHasCandy returns true when the deploy's candy list includes the
// given candy name. Used to gate whether K3sPostProvision runs — a no-op
// check against the ordered candy slice the deploy-add dispatcher already
// has in scope.
func deployHasCandy(layers []*Candy, name string) bool {
	for _, l := range layers {
		if l != nil && l.Name == name {
			return true
		}
	}
	return false
}
