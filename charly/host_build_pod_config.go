package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_pod_config.go — the "pod-config-<leaf>" F10 host-builders. The `charly config …`
// command's CLI GRAMMAR lives in command:config (candy/plugin-pod, the DEPLOY wave); Setup/
// Remove forward their flags via their own HostBuild("pod-config-<leaf>") seam.
//
// P13-KERNEL direction-flip: Setup/Remove's former ORCHESTRATION (BoxConfigSetupCmd/
// BoxConfigRemoveCmd, config_image.go) moved to candy/plugin-deploy-pod (sdk.OpConfigSetup/
// sdk.OpConfigRemove) — hostBuildPodConfigSetup/Remove now FORWARD onward to the plugin (resolve
// deploy:pod + InvokeWithExecutor, the SAME primitive InvokeProvider uses, S1) instead of
// running the orchestration in-core; the plugin calls back the narrow
// "pod-config-*" seams in host_build_pod_config_seams.go for the host/loader/registry/
// credential-coupled sub-steps. Status/Mount/Unmount/Passwd's seams (and their BoxConfig*Cmd
// core bodies) are DELETED (wave γ): those leaves now dispatch verb:enc/verb:credential
// DIRECTLY from candy/plugin-pod (enc_cmd.go) via InvokeProvider — the same ALREADY-LIVE pattern
// candy/plugin-deploy-pod/lifecycle.go proves for the start/stop path — no core round-trip left
// to seam.
const (
	podConfigSetupBuilderKind  = "pod-config-setup"
	podConfigRemoveBuilderKind = "pod-config-remove"
)

// invokePodConfigOp connects deploy:pod on-demand (using deployPodPluginCandyRef's
// connectPluginByWordRef pattern) and dispatches op WITH a host-local venue executor (pod's own
// venue is a no-op — see plugin-deploy-pod's Invoke doc — so the plugin's HostBuild callbacks are
// what actually do the work), so the plugin's Invoke handler can call back HostBuild for the
// narrow "pod-config-*" seams.
func invokePodConfigOp(ctx context.Context, op string, reqJSON []byte) ([]byte, error) {
	prov, ok := connectPluginByWordRef(ClassDeployTarget, "pod", deployPodPluginCandyRef())
	if !ok {
		return nil, fmt.Errorf("connecting deploy:pod plugin (candy/plugin-deploy-pod) for %s", op)
	}
	inv, ok := prov.(executorInvoker)
	if !ok {
		return nil, fmt.Errorf("deploy:pod provider does not support the executor reverse channel (%s)", op)
	}
	res, err := inv.InvokeWithExecutor(ctx, &Operation{Reserved: "pod", Op: op, Params: reqJSON}, kit.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		return nil, fmt.Errorf("deploy:pod %s: %w", op, err)
	}
	if res == nil {
		return nil, nil
	}
	return res.JSON, nil
}

func hostBuildPodConfigSetup(ctx context.Context, req spec.PodConfigSetupRequest, _ buildEngineContext) (spec.PodConfigSetupReply, error) {
	var rep spec.PodConfigSetupReply
	reqJSON, err := marshalJSON(req)
	if err != nil {
		return rep, err
	}
	resJSON, err := invokePodConfigOp(ctx, sdk.OpConfigSetup, reqJSON)
	if err != nil {
		return rep, err
	}
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &rep)
	}
	return rep, nil
}

func hostBuildPodConfigRemove(ctx context.Context, req spec.PodConfigRemoveRequest, _ buildEngineContext) (spec.PodConfigRemoveReply, error) {
	var rep spec.PodConfigRemoveReply
	reqJSON, err := marshalJSON(req)
	if err != nil {
		return rep, err
	}
	resJSON, err := invokePodConfigOp(ctx, sdk.OpConfigRemove, reqJSON)
	if err != nil {
		return rep, err
	}
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &rep)
	}
	return rep, nil
}

var _ = func() bool {
	registerHostBuilder(podConfigSetupBuilderKind, typedHostBuilder(podConfigSetupBuilderKind, hostBuildPodConfigSetup))
	registerHostBuilder(podConfigRemoveBuilderKind, typedHostBuilder(podConfigRemoveBuilderKind, hostBuildPodConfigRemove))
	return true
}()
