package main

import (
	"github.com/opencharly/sdk/spec"
)

// config_write_host.go — the canonical plugin-deploy-pod candy ref. P13-KERNEL direction-flip:
// the former host-side config-WRITE caller (writePodConfigViaPlugin) moved along with
// BoxConfigSetupCmd/BoxConfigRemoveCmd's orchestration to candy/plugin-deploy-pod, which now
// renders+writes the quadlet/.pod/sidecar/tunnel files in-process (config_write.go's
// renderAndWritePodConfig) instead of round-tripping through a host-invoked OpConfigWrite.
// deployPodPluginCandyRef stays — invokePodConfigOp (host_build_pod_config.go) still uses it to
// connect deploy:pod on-demand for the OpConfigSetup/OpConfigRemove forwards.

// deployPodPluginCandyRef is the canonical plugin-deploy-pod candy ref (analogous to
// vmPluginCandyRef): in a check bed CHARLY_REPO_OVERRIDE redirects it to the local superproject
// under development; outside a bed it fetches the published candy.
func deployPodPluginCandyRef() string {
	return "@" + spec.DefaultProjectRepo + "/candy/plugin-deploy-pod"
}
