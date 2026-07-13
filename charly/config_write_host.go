package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// config_write_host.go — the HOST side of the pod config-WRITE (P11, Q1=(a)). `charly config`
// resolves the QuadletConfig + computes the exact target paths (its own filename helpers), then
// delegates the WRITE (rendering + writing the quadlet/.pod/sidecar/tunnel files) to the deploy:pod
// plugin — Ruling C: the plugin owns config-write; the host keeps resolve + side-effects.
//
// deploy:pod is an OUT-OF-PROCESS deploy substrate (like every substrate — the external-provider
// direction), so it is NOT registered at startup and NOT connected by a plain `charly config`.
// This connects it ON-DEMAND via the SAME host-out-call mechanism the credential/vm/kube adapters
// use (connectPluginByWordRef — connectBakedPlugin is registry-resolve-first, so it is
// idempotent/cached after the first connect), then Invokes OpConfigWrite. The connected subprocess
// os.WriteFiles the rendered contents to the host FS at the ABSOLUTE paths the request carries —
// same host, same bytes, same modes (0600/0600/0600/0644) as the former in-core write phase, so the
// output is byte-identical. (Compiling deploy:pod IN would be regressive vs the external direction +
// would need the still-unwired compiled-in-deploy-substrate lifecycle; out-of-process is correct.)

// deployPodPluginCandyRef is the canonical plugin-deploy-pod candy ref (analogous to
// vmPluginCandyRef): in a check bed CHARLY_REPO_OVERRIDE redirects it to the local superproject
// under development; outside a bed it fetches the published candy.
func deployPodPluginCandyRef() string {
	return "@" + DefaultProjectRepo + "/candy/plugin-deploy-pod"
}

// writePodConfigViaPlugin connects deploy:pod on-demand and Invokes its OpConfigWrite to render +
// write the pod's quadlet/.pod/sidecar/tunnel files. The single host→plugin config-write seam (R3),
// shared by `charly config`'s main + --update-all write paths.
func writePodConfigViaPlugin(req spec.PodConfigWriteRequest) (spec.PodConfigWriteReply, error) {
	if _, ok := connectPluginByWordRef(ClassDeployTarget, "pod", deployPodPluginCandyRef()); !ok {
		return spec.PodConfigWriteReply{}, fmt.Errorf("connecting deploy:pod plugin (candy/plugin-deploy-pod) for config-write")
	}
	return hostInvoke[spec.PodConfigWriteRequest, spec.PodConfigWriteReply](ClassDeployTarget, "pod", OpConfigWrite, req)
}
