package main

// k3s_post.go — post-provision finalization for deploys whose candies included
// k3s-server. Runs after RetrieveCandyArtifacts has pulled the kubeconfig to
// ~/.cache/charly/clusters/<deploy>/kubeconfig.yaml.
//
// S3 (FINAL/K5 unit 6, Cutover-B): the finalization LOGIC — the retrieved-path
// check, the guest-forward kubeconfig server rewrite, and the ~/.kube/config merge
// — moved wholesale into candy/plugin-kube (k3s_post.go there), reached via the
// SAME out-of-process kube provider the `kube:` check verb uses
// (k8s_plugin.go's invokeKubePluginWithBroker — an InvokeWithExecutor call, so the
// plugin's Invoke has reverse-channel broker access for its
// "deploy-entity-resolve" HostBuild leg). What stays HERE is the artifact-handler
// registration (deploy_add_shared.go's artifactRegisterHandlers) — a plain Go func
// ref is no longer possible once the body lives in a separate module, so this file
// is now the ONE-CALL dispatch shim.
//
// Called from deploy_add_cmd.go and deploy_add_cmd_vm.go (both via
// deploy_add_shared.go) after the artifact retrieve step when the deploy's candy
// list contains "k3s-server". `charly bundle add` loads the deploy's composed
// external plugins first (loadProjectPlugins), so candy/plugin-kube — required by
// the k3s-server candy — is connected before this dispatch fires.

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// K3sPostProvision dispatches the k3s-post-provision method to candy/plugin-kube and
// prints its returned status line (mirroring the deleted core version's own
// fmt.Fprintf). artifactKey is the ENTITY-scoped identity (the shared per-VM cluster
// cache dir + kubeconfig context — one k3s cluster per VM, reached by several beds);
// deployName is the real per-deploy (domain) identity the port-forward lookup keys
// off. A "not a k3s-server deploy" (or --dry-run-skipped) outcome round-trips as an
// EMPTY message (the plugin's own no-op case) — nothing is printed.
func K3sPostProvision(artifactKey, deployName string) error {
	op := &spec.Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "k3s-post-provision", "artifact_key": artifactKey, "deploy_name": deployName,
	}}
	msg, err := invokeKubePluginWithBroker(op)
	if err != nil {
		return fmt.Errorf("k3s post-provision %q: %w", artifactKey, err)
	}
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return nil
}

// K3sPostProvision's registration for the artifact-declaration-driven dispatch
// (deploy_add_shared.go's artifactRegisterHandlers) — it must match the
// func(artifactKey, deployName string) error shape exactly. K3sPostProvision already
// does; this compile-time assertion catches a signature drift loudly.
var _ func(string, string) error = K3sPostProvision
