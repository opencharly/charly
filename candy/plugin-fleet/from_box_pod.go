package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// from_box_pod.go — the POD path of `charly fleet from-box <ref> [name]`, relocated from the
// deleted charly/fleet_from_box_cmd.go + charly/host_build_deploy_from_box.go (K-wave 2 cone R2
// bank B). A SOURCE-LESS deploy driven entirely by an image's baked ai.opencharly.* OCI labels,
// with NO charly.yml project: reach deploy:pod's OpConfigSetup (the project-free config-setup
// ORCHESTRATION, P13-KERNEL direction-flip) DIRECTLY via exec.InvokeProvider — the exact
// peer-dispatch idiom this package's deploy_from_box.go uses for the deploy:kubernetes OpEmit leg — then,
// in quadlet mode, start the resulting systemd-user service.
//
// The former host-side seam computed spec.HostEnv in core (hostEnvJSON — os.Executable() is only
// correct in-core, R10 bed-found bug #5); with the seam gone, the host threads it as DATA on the
// generic COMMAND dispatch (OpRun) and this package forwards it verbatim into
// PodConfigSetupRequest.HostEnvJSON (deploy:pod's encrypted-mount ExecStartPre CharlyBin line).
//
// This is the in-guest leg of the nested-pod-in-VM capability: a VM guest has `charly` + a
// cp-box'd image but no project, so the host orchestrates `ssh guest 'charly fleet from-box <ref>
// <name>'` to bring a nested pod up as a persistent quadlet (it survives reboot via the quadlet
// [Install] section once the guest user has lingering enabled — the orchestrator handles that).
func runFromBoxPod(c *FleetFromBoxCmd) error {
	if strings.TrimSpace(c.Ref) == "" {
		return fmt.Errorf("charly fleet from-box: a full image <ref> is required")
	}
	name := c.Name
	if name == "" {
		name = spec.DeriveDeploymentName(c.Ref)
	}

	// Pod path. Reuse the project-free config-setup ORCHESTRATION (candy/plugin-deploy-pod, the
	// P13-KERNEL direction-flip) via ExplicitRef: it reads the image's labels, builds the
	// QuadletConfig, writes + enables the quadlet, and daemon-reloads — all with no charly.yml.
	// Reached peer-to-peer by InvokeProvider("deploy","pod",OpConfigSetup) — the same idiom
	// deploy_from_box.go:185 uses for deploy:kubernetes's OpEmit — not through a host-build round-trip.
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.PodConfigSetupRequest{
		Box:         name,
		Instance:    c.Instance,
		Env:         c.Env,
		Port:        c.Port,
		ExplicitRef: c.Ref,
		HostEnvJSON: cmdHostEnvJSON,
	})
	if err != nil {
		return fmt.Errorf("from-box config %q: %w", name, err)
	}
	// A COMPILED-IN command's reverse channel carries no venue executor by default (broker_id=0),
	// so the invoked deploy:pod OpConfigSetup — which runs host commands on the shell venue — must
	// be handed one explicitly via the S1 VenueDescriptor{Kind:"shell"} (the same opts the deleted
	// pod-config seam's forwarder passed; without it OpConfigSetup fails "no host executor attached".
	// This is the K-wave 2 cone R3 bank-3 fix for the bank-B dispatch, mirroring
	// candy/plugin-pod/host_seams.go's ConfigSetupCmd/ConfigRemoveCmd).
	opts := sdk.InvokeProviderOpts{VenueDescriptor: &spec.VenueDescriptor{Kind: "shell"}}
	if _, err := cmdExec.InvokeProvider(cmdCtx, "deploy", "pod", sdk.OpConfigSetup, reqJSON, nil, opts); err != nil {
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
