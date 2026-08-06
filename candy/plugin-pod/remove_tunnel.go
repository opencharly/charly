package pod

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// remove_tunnel.go — the `charly remove` tunnel-teardown leg (Cutover B unit 2 remove-verb
// completion). Mirrors candy/plugin-deploy-pod/lifecycle.go's podTunnelOp exactly (R3): both reach
// the SAME compiled-in, placement-agnostic verb:tunnel provider over the SAME
// {plugin_input:{method,config}} envelope.
// The tunnel CONFIG itself is still resolved HOST-side, via the EXISTING pod-config-container-tunnel
// seam (charly/host_build_pod_config_seams.go's hostBuildPodConfigContainerTunnel) — the SAME seam
// candy/plugin-deploy-pod's resolvePodStartQuadlet/resolvePodStartDirect/resolvePodStopPlan already
// call to populate PodLifecyclePlan.Tunnel for start/stop — because the resolution's true source,
// TunnelConfigFromMetadata(*BoxMetadata), takes a charly-core-only type not portable into this
// module (the running container's baked image ref + charly.yml overlay merge stay core Mechanisms).
// No new seam was invented for this: both pieces (config-resolve seam, verb:tunnel provider) already
// existed and were already load-bearing for pod start/stop before this leaf reused them for remove.

// resolveContainerTunnel resolves the running container's tunnel config PLUGIN-SIDE (#55 coneC-dsh
// — the pod-config-container-tunnel host seam is DELETED). Reads the container's baked image ref
// (container.ContainerImage — spec/container, plugin-reachable), extracts its metadata, merges the
// per-host overlay (loadPodFleetConfig — the cycle-free loaderkit read), and resolves the tunnel
// config. nil if none configured or on any resolution error — best-effort, matching the former
// host leg's own best-effort framing.
func resolveContainerTunnel(box, instance string) *spec.TunnelConfig {
	ctrName := spec.ContainerNameInstance(box, instance)
	imageRef := container.ContainerImage("podman", ctrName)
	if imageRef == "" {
		return nil
	}
	meta, err := deploykit.ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return nil
	}
	dc, _ := loadPodFleetConfig()
	deploykit.MergeDeployOntoMetadata(meta, dc, box, instance)
	if meta.Tunnel == nil {
		return nil
	}
	return deploykit.TunnelConfigFromMetadata(meta)
}

// podTunnelStop composes verb:tunnel over InvokeProvider — byte-identical in shape to
// candy/plugin-deploy-pod/lifecycle.go's podTunnelOp(ctx, exec, "stop", cfg), including decoding
// the reply's business-level Error field (R1/R3: InvokeProvider only surfaces RPC-transport
// failures, so a healthy-RPC exec failure must be pulled out of the reply payload explicitly — the
// SAME fix applied to podTunnelOp so neither call site silently swallows a real teardown failure).
func podTunnelStop(cfg *spec.TunnelConfig) error {
	if cmdExec == nil {
		return fmt.Errorf("pod remove: no host reverse channel (command not compiled-in?)")
	}
	body, err := json.Marshal(map[string]any{"plugin_input": map[string]any{"method": "stop", "config": cfg}})
	if err != nil {
		return err
	}
	resJSON, err := cmdExec.InvokeProvider(cmdCtx, "verb", "tunnel", sdk.OpRun, body, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	if len(resJSON) == 0 {
		return nil
	}
	var rep struct {
		Error string `json:"error,omitempty"`
	}
	if json.Unmarshal(resJSON, &rep) == nil && rep.Error != "" {
		return errors.New(rep.Error)
	}
	return nil
}
