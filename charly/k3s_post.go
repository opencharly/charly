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
	"strings"
)

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
	if _, err := invokeKubePlugin(&Op{Kube: "merge-kubeconfig", Kubeconfig: retrievedPath, KubeContext: contextName}); err != nil {
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
