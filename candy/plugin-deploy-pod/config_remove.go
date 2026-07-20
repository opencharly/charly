package deploypod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// config_remove.go — sdk.OpConfigRemove: the former charly-core BoxConfigRemoveCmd.Run ported
// VERBATIM (no seams needed — purely resolveBoxName + IsDirectDeploy + podman/systemctl exec +
// findPodSidecarQuadlets, all portable). Distinct from `charly remove`/OpPostTeardown, which tears
// down the whole deploy; this removes only the quadlet + disables the service.
func invokeConfigRemove(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var r spec.PodConfigRemoveRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod config-remove: decode request: %w", err)
	}
	if err := runPodConfigRemove(&r); err != nil {
		return nil, err
	}
	return marshalReply(spec.PodConfigRemoveReply{})
}

func runPodConfigRemove(c *spec.PodConfigRemoveRequest) error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	boxName := resolveBoxName(c.Box)

	if rt.RunMode == "direct" || IsDirectDeploy(boxName, c.Instance) {
		name := kit.ContainerNameInstance(boxName, c.Instance)
		_ = exec.Command("podman", "stop", name).Run()
		if out, err := exec.Command("podman", "rm", "-f", name).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: podman rm %s: %v\n%s", name, err, strings.TrimSpace(string(out)))
		}
		if err := removeDirectDeployMarker(boxName, c.Instance); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing direct-mode marker: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s (direct mode)\n", name)
		return nil
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("charly config remove requires run_mode=quadlet or direct (current: %s)", rt.RunMode)
	}

	svc := kit.ServiceNameInstance(boxName, c.Instance)
	cmd := exec.Command("systemctl", "--user", "disable", "--now", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	podSvc := kit.PodNameInstance(boxName, c.Instance) + "-pod.service"
	_ = exec.Command("systemctl", "--user", "disable", "--now", podSvc).Run()

	if qdir, qErr := kit.QuadletDir(); qErr == nil {
		podName := kit.PodNameInstance(boxName, c.Instance)
		mainFile := kit.ContainerNameInstance(boxName, c.Instance) + ".container"
		if sidecars, dErr := findPodSidecarQuadlets(qdir, podName, mainFile); dErr == nil {
			for _, name := range sidecars {
				scSvc := strings.TrimSuffix(name, ".container") + ".service"
				fmt.Fprintf(os.Stderr, "Disabling sidecar %s\n", scSvc)
				_ = exec.Command("systemctl", "--user", "disable", "--now", scSvc).Run()
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Disabled %s\n", svc)
	return nil
}
