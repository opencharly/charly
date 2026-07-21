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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

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
	// The pure text-rewrite + tree-search helpers this file used to define locally
	// moved to sdk/deploykit (Cutover B unit 5, P13-KERNEL-B) — zero loader/registry
	// dependency. What stays here (this function, deployVMForwards below) is the
	// LoadUnified-coupled orchestration deciding WHICH forwards apply, K1-permanent
	// per R-E2.
	out := deploykit.RewriteServerPorts(string(data), guestToHost)
	if out == string(data) {
		return nil
	}
	return os.WriteFile(retrievedPath, []byte(out), 0o600)
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
	// entityRef is either a "vm:<entity>" reference (the VM-deploy artifact key) or a
	// bundle key whose node carries `from: <vm entity>`. Resolve the VM entity either way,
	// through the generic "deploy-entity-resolve" host-builder (FINAL/K5 unit 6a) instead of
	// LoadUnified directly — this file is core-only, so it calls the host-builder function
	// in-process (no HostBuild/Executor round trip needed, unlike a plugin caller).
	vmEntity := ""
	if e, cut := strings.CutPrefix(entityRef, "vm:"); cut {
		vmEntity = e
	} else {
		reply, err := hostBuildDeployEntityResolve(context.Background(), spec.DeployEntityResolveRequest{Kind: "bundle", Name: entityRef}, buildEngineContext{})
		if err != nil || reply.Node == nil {
			return nil, nil
		}
		vmEntity = reply.Node.From
	}
	if vmEntity == "" {
		return nil, nil
	}
	vmReply, err := hostBuildDeployEntityResolve(context.Background(), spec.DeployEntityResolveRequest{Kind: "vm", Name: vmEntity}, buildEngineContext{})
	if err != nil || len(vmReply.EntityJSON) == 0 {
		return nil, nil
	}
	var vm spec.ResolvedVm
	if err := json.Unmarshal(vmReply.EntityJSON, &vm); err != nil {
		return nil, fmt.Errorf("deploy-entity-resolve: decode vm %q: %w", vmEntity, err)
	}
	if vm.Network == nil {
		return nil, nil
	}
	key := "vm:" + vmDomainIdentity(deployName)
	var alloc map[string]int
	if entry, ok := deploykit.LoadDeployConfigForRead("k3s kubeconfig forward").LookupKey(key); ok && entry.VmState != nil {
		alloc = entry.VmState.PortForwards
	}
	resolved, rerr := deploykit.ResolveDeployForwards(vm.Network.PortForwards, alloc)
	if rerr != nil {
		return nil, fmt.Errorf("deploy %q (vm_state key %q): %w", deployName, key, rerr)
	}
	return resolved, nil
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
	safe := kit.SanitizeDeployName(artifactKey)
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

// K3sPostProvision's registration for the artifact-declaration-driven dispatch
// (deploy_add_shared.go's artifactRegisterHandlers) — it must match the
// func(artifactKey, deployName string) error shape exactly. K3sPostProvision already
// does; this compile-time assertion catches a signature drift loudly.
var _ func(string, string) error = K3sPostProvision

// mergeKubeconfig merges the retrieved kubeconfig into the operator's
// ~/.kube/config under the chosen context name. The clientcmd merge itself — and
// therefore the client-go clientcmd dependency — lives in the
// out-of-tree candy/plugin-kube provider; this host-side wrapper just dispatches a
// synthetic `kube: merge-kubeconfig` #Op to it (invokeKubePlugin). Existing entries
// with the same context/cluster/user name are OVERWRITTEN by the plugin —
// deploy-add is the single source of truth for clusters it manages, so a rebuild
// cleanly picks up a fresh admin cert without stale entries.
func mergeKubeconfig(retrievedPath, contextName string) error {
	op := &spec.Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "merge-kubeconfig", "kubeconfig": retrievedPath, "kube_context": contextName,
	}}
	if _, err := invokeKubePlugin(op); err != nil {
		return err
	}
	return nil
}
