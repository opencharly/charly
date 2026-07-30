package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// deployFromBoxCmd is the host-side orchestration for `charly bundle from-box <ref>
// [name]` — a SOURCE-LESS deploy driven entirely by an image's baked ai.opencharly.* OCI
// labels, with NO charly.yml project. Pod-only (default): generate + enable a podman quadlet
// from the image's labels (ports, services, volumes, env, GPU auto-detect), then start the
// resulting systemd-user service. Reuses the project-free config-setup ORCHESTRATION
// (P13-KERNEL direction-flip: candy/plugin-deploy-pod's sdk.OpConfigSetup) via
// #PodConfigSetupRequest.ExplicitRef — no quadlet logic is duplicated.
//
// The k8s target (--cluster <name>) is handled ENTIRELY plugin-side now (Cone A shape 3,
// candy/plugin-bundle/deploy_from_box.go's DeployFromBox) — BundleFromBoxCmd.Run() branches
// BEFORE ever forwarding to this seam, so this struct never sees a non-empty Cluster/Namespace.
//
// This is the in-guest leg of the nested-pod-in-VM capability: a VM guest has
// `charly` + a cp-box'd image but no project, so the host orchestrates
// `ssh guest 'charly bundle from-box <ref> <name>'` to bring a nested pod up as a
// persistent quadlet (it survives reboot via the quadlet [Install] section once
// the guest user has lingering enabled — the orchestrator handles that).
// The CLI GRAMMAR lives in the command:bundle plugin (candy/plugin-bundle); this struct
// is reconstructed from spec.DeployFromBoxRequest by the deploy-from-box host-build seam.
type deployFromBoxCmd struct {
	Ref      string
	Name     string
	Instance string
	Env      []string
	Port     []string
}

func (c *deployFromBoxCmd) Run() error {
	if strings.TrimSpace(c.Ref) == "" {
		return fmt.Errorf("charly bundle from-box: a full image <ref> is required")
	}
	name := c.Name
	if name == "" {
		name = spec.DeriveDeploymentName(c.Ref)
	}

	// Pod path. Reuse the project-free config-setup ORCHESTRATION (now in candy/plugin-deploy-pod,
	// the P13-KERNEL direction-flip) via ExplicitRef: it reads the image's labels, builds the
	// QuadletConfig, writes + enables the quadlet, and daemon-reloads — all with no charly.yml.
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	// hostBuildPodConfigSetup is called directly in-process (not via the wire-registered
	// HostBuild dispatch), but its signature is fixed by the generic typedHostBuilder contract
	// every F10 host-builder shares; the buildEngineContext{} arg is unused by this handler.
	if _, err := hostBuildPodConfigSetup(context.Background(), spec.PodConfigSetupRequest{
		Box:         name,
		Instance:    c.Instance,
		Env:         c.Env,
		Port:        c.Port,
		ExplicitRef: c.Ref,
	}, buildEngineContext{}); err != nil {
		return fmt.Errorf("from-box config %q: %w", name, err)
	}

	// In direct mode (no systemd-user) the plugin's runConfigDirect already launched the
	// container via `podman run -d`; nothing more to do. In quadlet mode the plugin's
	// runConfig only WROTE + enabled the quadlet (it starts the container
	// itself only for a post_enable hook), so start the service now. Start by
	// SERVICE name — the image ref is already baked into the quadlet from
	// ExplicitRef, and `name` may differ from the image's own short-name, so a
	// short-name re-resolution (as `charly start` does) would resolve the wrong
	// image.
	if rt.RunMode == "quadlet" {
		svc := spec.ServiceNameInstance(name, c.Instance)
		start := exec.Command("systemctl", "--user", "start", svc)
		start.Stdout = os.Stderr
		start.Stderr = os.Stderr
		if err := start.Run(); err != nil {
			return fmt.Errorf("starting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Deployed (from image) %q → %s started\n", name, svc)
	}
	return nil
}
