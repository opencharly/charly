package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// k8s_plugin.go — the host-side invoker that routes charly's k3s post-provision
// finalization (K3sPostProvision, registered for `charly bundle add`'s
// artifactRegisterHandlers["kubeconfig"]) through the SAME out-of-process kube
// provider the `kube:` check verb uses (candy/plugin-kube). It exists so the heavy
// k8s.io/client-go/tools/clientcmd dependency lives ENTIRELY in the plugin, out of
// charly's core go.mod.
//
// The dispatch is WITH the reverse-channel broker (InvokeWithExecutor, kit.ShellExecutor{}
// — the "broker only, no live venue" idiom deploy_preresolve.go's wireDeployPreresolver
// also uses), because the plugin's k3s-post-provision method (S3, FINAL/K5 unit 6 —
// relocated wholesale from charly/k3s_post.go) needs the "deploy-entity-resolve"
// HostBuild seam for its LoadUnified-coupled VM-forward lookup.

// resolveKubePlugin lazily build-connects candy/plugin-kube if the deploy path has
// not already (the generic host-adapter seam, F7) and asserts it out-of-process
// (the ONE placement this plugin ships in today — its client-go dependency is never
// compiled in), returning the *grpcProvider invokeKubePluginWithBroker needs.
func resolveKubePlugin() (*grpcProvider, error) {
	prov, ok := connectPluginByWord(ClassVerb, "kube")
	if !ok {
		return nil, fmt.Errorf("kube plugin not loaded — the deploy must compose candy/plugin-kube (its provider serves the clientcmd-backed kubeconfig merge); k3s-server requires it")
	}
	gp, ok := prov.(*grpcProvider)
	if !ok {
		return nil, fmt.Errorf("kube plugin: resolved provider is not out-of-process (%T) — the k3s deploy seam requires the external candy/plugin-kube placement", prov)
	}
	return gp, nil
}

// invokeKubePluginWithBroker dispatches a synthetic kube #Op WITH the reverse-channel
// broker (InvokeWithExecutor), so the plugin's Invoke can call back HostBuild — the
// k3s-post-provision leg. kit.ShellExecutor{} is the "broker only, no live venue"
// idiom (the deploy already ran on its own executor; this finalization step runs
// entirely on the operator host's own filesystem — ~/.cache/charly + ~/.kube/config).
// It is a swappable package-level var (like InspectContainer) so K3sPostProvision's
// callers stay unit-testable without a live plugin.
var invokeKubePluginWithBroker = func(op *spec.Op) (string, error) {
	prov, err := resolveKubePlugin()
	if err != nil {
		return "", err
	}
	params, err := marshalJSON(op)
	if err != nil {
		return "", fmt.Errorf("kube plugin: marshal op: %w", err)
	}
	res, err := prov.InvokeWithExecutor(context.Background(),
		&Operation{Reserved: "kube", Op: OpRun, Params: params}, kit.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		return "", fmt.Errorf("kube plugin: %w", err)
	}
	var pr pluginCheckResult
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &pr); uerr != nil {
			return "", fmt.Errorf("kube plugin: decode reply: %w", uerr)
		}
	}
	if pr.Status == "fail" {
		return pr.Message, fmt.Errorf("kube plugin: %s", pr.Message)
	}
	return pr.Message, nil
}
