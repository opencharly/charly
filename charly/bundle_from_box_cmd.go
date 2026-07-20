package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// deployFromBoxCmd is the host-side orchestration for `charly bundle from-box <ref>
// [name]` — a SOURCE-LESS deploy driven entirely by an image's baked ai.opencharly.* OCI
// labels, with NO charly.yml project. Two targets:
//
//   - pod (default): generate + enable a podman quadlet from the image's labels
//     (ports, services, volumes, env, GPU auto-detect via DetectHostDevices),
//     then start the resulting systemd-user service. Reuses the project-free
//     runConfig core via BoxConfigSetupCmd.ExplicitRef — no quadlet logic is
//     duplicated.
//   - k8s (--cluster <name>): emit a Kustomize tree via the existing
//     DeployFromBox (charly/k8s_deploy_from_box.go) — unifying the from-box
//     surface across both targets.
//
// This is the in-guest leg of the nested-pod-in-VM capability: a VM guest has
// `charly` + a cp-box'd image but no project, so the host orchestrates
// `ssh guest 'charly bundle from-box <ref> <name>'` to bring a nested pod up as a
// persistent quadlet (it survives reboot via the quadlet [Install] section once
// the guest user has lingering enabled — the orchestrator handles that).
// The CLI GRAMMAR lives in the command:bundle plugin (candy/plugin-bundle); this struct
// is reconstructed from spec.DeployFromBoxRequest by the deploy-from-box host-build seam.
type deployFromBoxCmd struct {
	Ref       string
	Name      string
	Instance  string
	Env       []string
	Port      []string
	Cluster   string
	Namespace string
}

func (c *deployFromBoxCmd) Run() error {
	if strings.TrimSpace(c.Ref) == "" {
		return fmt.Errorf("charly bundle from-box: a full image <ref> is required")
	}
	name := c.Name
	if name == "" {
		name = deriveDeploymentName(c.Ref)
	}

	// K8s path: delegate to the existing source-less K8s deployer.
	if c.Cluster != "" {
		dir, _ := os.Getwd()
		out, err := DeployFromBox(DeployFromBoxOpts{
			ImageRef:       c.Ref,
			DeploymentName: name,
			Instance:       c.Instance,
			ClusterName:    c.Cluster,
			Namespace:      c.Namespace,
			ProjectDir:     dir,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated Kustomize overlay for %q at %s\n  apply with: kubectl apply -k %s\n", name, out, out)
		return nil
	}

	// Pod path. Reuse the project-free runConfig core via ExplicitRef: it reads
	// the image's labels, builds the QuadletConfig, writes + enables the
	// quadlet, and daemon-reloads — all with no charly.yml.
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	icc := &BoxConfigSetupCmd{
		Box:         name,
		Instance:    c.Instance,
		Env:         c.Env,
		Port:        c.Port,
		ExplicitRef: c.Ref,
	}
	if err := icc.Run(); err != nil {
		return fmt.Errorf("from-box config %q: %w", name, err)
	}

	// In direct mode (no systemd-user) runConfigDirect already launched the
	// container via `podman run -d`; nothing more to do. In quadlet mode
	// runConfig only WROTE + enabled the quadlet (it starts the container
	// itself only for a post_enable hook), so start the service now. Start by
	// SERVICE name — the image ref is already baked into the quadlet from
	// ExplicitRef, and `name` may differ from the image's own short-name, so a
	// short-name re-resolution (as `charly start` does) would resolve the wrong
	// image.
	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(name, c.Instance)
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
