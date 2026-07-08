package main

import (
	"context"
	"fmt"
)

// k8s_plugin.go — the host-side invoker that routes charly's k3s kubeconfig-merge
// (K3sPostProvision, in `charly bundle add`) through the SAME out-of-process kube
// provider the `kube:` check verb uses (candy/plugin-kube). It exists so the heavy
// k8s.io/client-go/tools/clientcmd dependency lives ENTIRELY in the plugin, out of
// charly's core go.mod: the core builds a synthetic `kube: merge-kubeconfig` #Op
// (the retrieved kubeconfig path + the deploy-named context) and hands it to the
// plugin's Invoke, which performs the clientcmd merge into ~/.kube/config and
// returns a {status,message} verdict.
//
// No env struct is needed: the merge reads the kubeconfig path + context off the
// Op and uses os.UserHomeDir() plugin-side itself.

// invokeKubePlugin dispatches one synthetic kube #Op (merge-kubeconfig) to the
// registered out-of-process kube provider and returns the plugin's message (or an
// error when the plugin reports a failure). It is a swappable package-level var
// (like InspectContainer) so the deploy callers stay
// unit-testable without a live plugin. Mirrors invokeVerbProvider's Operation
// envelope + pluginCheckResult decode (R3).
var invokeKubePlugin = func(op *Op) (string, error) {
	// connectPluginByWord (not a bare ResolveVerb): lazily build-connects candy/plugin-kube if the
	// deploy path has not already (the generic host-adapter seam, F7) — strictly stronger than the
	// prior resolve-only, which failed outside the deploy loader.
	prov, ok := connectPluginByWord(ClassVerb, "kube")
	if !ok {
		return "", fmt.Errorf("kube plugin not loaded — the deploy must compose candy/plugin-kube (its provider serves the clientcmd-backed kubeconfig merge); k3s-server requires it")
	}
	pr, err := invokeTyped[*Op, pluginCheckResult](context.Background(), prov, "kube", OpRun, op)
	if err != nil {
		return "", fmt.Errorf("kube plugin: %w", err)
	}
	if pr.Status == "fail" {
		return pr.Message, fmt.Errorf("kube plugin: %s", pr.Message)
	}
	return pr.Message, nil
}
