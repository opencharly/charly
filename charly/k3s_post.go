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
)

// k3sServerURLRe matches an `https://<host>:<port>` server URL in a kubeconfig.
var k3sServerURLRe = regexp.MustCompile(`(https?://)([^:/\s]+):(\d+)`)

// rewriteK3sServerToForward rewrites the retrieved kubeconfig's server URL, mapping
// the guest-local k3s API port to the host-forwarded port declared on the deploy's VM
// (network.port_forwards "<host>:<guest>"). No-op when the deploy has no matching VM
// forward — a bare-metal / already-host-reachable k3s needs no rewrite.
func rewriteK3sServerToForward(retrievedPath, deployName string) error {
	forwards := deployVMForwards(deployName)
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
func deployVMForwards(deployName string) []string {
	uf, ok, err := LoadUnified(".")
	if err != nil || !ok || uf == nil {
		return nil
	}
	// deployName is either a "vm:<entity>" reference (the VM-deploy artifact key) or a
	// bundle key whose node carries `from: <vm entity>`. Resolve the VM entity either way.
	vmEntity := ""
	if e, cut := strings.CutPrefix(deployName, "vm:"); cut {
		vmEntity = e
	} else if node := findBundleNodeByName(uf.Bundle, deployName); node != nil {
		vmEntity = node.From
	}
	if vmEntity == "" {
		return nil
	}
	vm := uf.VM[vmEntity]
	if vm == nil || vm.Network == nil {
		return nil
	}
	return vm.Network.PortForwards
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
func K3sPostProvision(deployName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home: %w", err)
	}
	safe := sanitizeDeployName(deployName)
	retrieved := filepath.Join(home, ".cache", "charly", "clusters", safe, "kubeconfig.yaml")
	if _, err := os.Stat(retrieved); err != nil {
		// Not a k3s-server deploy, or retricheck was skipped. Nothing to do.
		return nil
	}

	// The retrieved kubeconfig carries k3s's GUEST-local server URL (127.0.0.1:6443);
	// the host reaches the in-guest API only through the VM's host:guest port-forward.
	// Rewrite the server to the host-forwarded port so `kubectl`/`kube:` checks work
	// host-side (without this, kubectl dials 127.0.0.1:6443 → connection refused).
	if err := rewriteK3sServerToForward(retrieved, deployName); err != nil {
		return fmt.Errorf("rewriting k3s kubeconfig server to the forwarded port: %w", err)
	}

	contextName := safe
	if err := mergeKubeconfig(retrieved, contextName); err != nil {
		return fmt.Errorf("merging kubeconfig into ~/.kube/config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "k3s cluster %q registered — kubectl --context=%s get nodes\n", deployName, contextName)
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
